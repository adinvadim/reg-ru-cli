package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/credentialprocess"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/cdp"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

type ExecutorOptions struct {
	Endpoint      string
	SigningRegion string
	HTTPClient    HTTPDoer
	DataPlanes    DataPlaneFactory
	ControlPlane  ControlPlane
}

type Executor struct {
	profiles profile.Repository
	options  ExecutorOptions
	fallback cli.Executor
}

func NewExecutor(
	profiles profile.Repository,
	options ExecutorOptions,
	fallback cli.Executor,
) *Executor {
	if fallback == nil {
		fallback = cli.UnavailableExecutor{}
	}
	if options.DataPlanes == nil {
		options.DataPlanes = AWSDataPlaneFactory{}
	}
	return &Executor{profiles: profiles, options: options, fallback: fallback}
}

func (e *Executor) Execute(
	ctx context.Context,
	operation cli.Operation,
) (cli.Result, error) {
	if !strings.HasPrefix(operation.Action, "s3.") {
		return e.fallback.Execute(ctx, operation)
	}
	account, err := e.account(operation)
	if err != nil {
		return cli.Result{}, err
	}
	var result cli.Result
	switch operation.Action {
	case "s3.service.show":
		result, err = e.showService(ctx, account)
	case "s3.service.quota.set":
		result, err = e.setServiceQuota(ctx, account, int32Parameter(operation, "quota-gb"))
	case "s3.bucket.list":
		result, err = e.listBuckets(ctx, account)
	case "s3.bucket.show":
		result, err = e.showBucket(ctx, account, argument(operation, 0))
	case "s3.bucket.create":
		result, err = e.createBucket(ctx, account, operation)
	case "s3.bucket.update":
		result, err = e.updateBucket(ctx, account, operation)
	case "s3.bucket.delete":
		result, err = e.deleteBucket(ctx, account, argument(operation, 0))
	case "s3.credentials.list":
		result, err = e.listCredentials(ctx, account)
	case "s3.credentials.create":
		result, err = e.createCredential(ctx, account, parameter(operation, "name"))
	case "s3.credentials.revoke":
		result, err = e.revokeCredential(ctx, account, argument(operation, 0))
	default:
		result, err = e.executeDataPlane(ctx, account, operation)
	}
	if err != nil {
		return cli.Result{}, e.translate(operation, err)
	}
	return result, nil
}

func (e *Executor) account(operation cli.Operation) (profile.Account, error) {
	if e == nil || e.profiles == nil {
		return profile.Account{}, cli.ConfigurationError("S3 integration is not configured")
	}
	config, err := e.profiles.Load()
	if err != nil {
		return profile.Account{}, cli.ConfigurationError("profile configuration is invalid")
	}
	account, exists := config.Accounts[operation.Account]
	if !exists || account.ID != operation.ProfileID {
		return profile.Account{}, cli.AccountNotFound(operation.Account)
	}
	return account, nil
}

func (e *Executor) control() (ControlPlane, error) {
	if e.options.ControlPlane == nil {
		return nil, &PortalError{Kind: PortalUnavailable, Code: "portal_adapter_unavailable"}
	}
	return e.options.ControlPlane, nil
}

func (e *Executor) showService(ctx context.Context, account profile.Account) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	store, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	return renderService(store), nil
}

func (e *Executor) listBuckets(ctx context.Context, account profile.Account) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	store, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	sort.Slice(store.Buckets, func(first, second int) bool {
		return store.Buckets[first].Name < store.Buckets[second].Name
	})
	return renderBuckets(store.Buckets), nil
}

func (e *Executor) showBucket(ctx context.Context, account profile.Account, name string) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	store, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	bucket, exists := store.Bucket(name)
	if !exists {
		return cli.Result{}, &PortalError{Kind: PortalDomain, Code: "BucketNotFound"}
	}
	return renderBucket(bucket), nil
}

func (e *Executor) createBucket(
	ctx context.Context,
	account profile.Account,
	operation cli.Operation,
) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	before, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	name := argument(operation, 0)
	if _, exists := before.Bucket(name); exists {
		return cli.Result{}, &PortalError{Kind: PortalDomain, Code: "BucketNameConflict"}
	}
	public := strings.EqualFold(parameter(operation, "access"), "public")
	var quota *int32
	if value := int32Parameter(operation, "quota-gb"); value > 0 {
		quota = &value
	}
	request := MutationRequest{
		Action: MutationBucketCreate, ServiceID: before.ServiceID,
		ObjectStoreID: before.ID, Name: name, QuotaGB: quota, Public: &public,
	}
	_, mutationErr := control.Mutate(ctx, account, request)
	after, readErr := control.Inventory(ctx, account)
	if mutationErr != nil {
		if isPortalKind(mutationErr, PortalAmbiguous) && readErr == nil {
			if bucket, exists := after.Bucket(name); exists && bucketMatchesCreate(bucket, quota, public) {
				result := renderBucketChange("created", nil, &bucket)
				result.Warnings = append(result.Warnings, reconciledWarning())
				return result, nil
			}
		}
		return cli.Result{}, mutationErr
	}
	if readErr != nil {
		return cli.Result{}, readErr
	}
	bucket, exists := after.Bucket(name)
	if !exists || !bucketMatchesCreate(bucket, quota, public) {
		return cli.Result{}, &PortalError{Kind: PortalAmbiguous, Code: "postcondition_not_verified"}
	}
	return renderBucketChange("created", nil, &bucket), nil
}

func (e *Executor) updateBucket(
	ctx context.Context,
	account profile.Account,
	operation cli.Operation,
) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	store, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	name := argument(operation, 0)
	before, exists := store.Bucket(name)
	if !exists {
		return cli.Result{}, &PortalError{Kind: PortalDomain, Code: "BucketNotFound"}
	}
	access := parameter(operation, "access")
	quotaText := parameter(operation, "quota-gb")
	quotaUnlimited := parameter(operation, "quota-unlimited") == "true"
	reconciled := false
	if access != "" {
		public := strings.EqualFold(access, "public")
		request := MutationRequest{
			Action: MutationBucketPrivacy, ServiceID: store.ServiceID,
			ObjectStoreID: store.ID, Name: name, Public: &public,
		}
		if _, err := control.Mutate(ctx, account, request); err != nil {
			reconciledErr := e.reconcileBucketMutation(ctx, control, account, name, func(bucket Bucket) bool {
				return accessMatches(bucket.AccessType, public)
			}, err)
			if reconciledErr != nil {
				return cli.Result{}, reconciledErr
			}
			reconciled = true
		}
		store, err = control.Inventory(ctx, account)
		if err != nil {
			return cli.Result{}, err
		}
		if current, ok := store.Bucket(name); !ok || !accessMatches(current.AccessType, public) {
			return cli.Result{}, &PortalError{Kind: PortalAmbiguous, Code: "postcondition_not_verified"}
		}
	}
	if quotaText != "" || quotaUnlimited {
		var quota *int32
		if !quotaUnlimited {
			value := int32Parameter(operation, "quota-gb")
			quota = &value
		}
		request := MutationRequest{
			Action: MutationBucketQuota, ServiceID: store.ServiceID,
			ObjectStoreID: store.ID, Name: name, QuotaGB: quota,
		}
		if _, err := control.Mutate(ctx, account, request); err != nil {
			reconciledErr := e.reconcileBucketMutation(ctx, control, account, name, func(bucket Bucket) bool {
				return equalQuota(bucket.QuotaGB, quota)
			}, err)
			if reconciledErr != nil {
				return cli.Result{}, reconciledErr
			}
			reconciled = true
		}
		store, err = control.Inventory(ctx, account)
		if err != nil {
			return cli.Result{}, err
		}
		if current, ok := store.Bucket(name); !ok || !equalQuota(current.QuotaGB, quota) {
			return cli.Result{}, &PortalError{Kind: PortalAmbiguous, Code: "postcondition_not_verified"}
		}
	}
	after, _ := store.Bucket(name)
	result := renderBucketChange("updated", &before, &after)
	if reconciled {
		result.Warnings = append(result.Warnings, reconciledWarning())
	}
	return result, nil
}

func (e *Executor) reconcileBucketMutation(
	ctx context.Context,
	control ControlPlane,
	account profile.Account,
	name string,
	matches func(Bucket) bool,
	mutationErr error,
) error {
	if !isPortalKind(mutationErr, PortalAmbiguous) {
		return mutationErr
	}
	store, err := control.Inventory(ctx, account)
	if err != nil {
		return mutationErr
	}
	bucket, exists := store.Bucket(name)
	if exists && matches(bucket) {
		return nil
	}
	return mutationErr
}

func (e *Executor) deleteBucket(ctx context.Context, account profile.Account, name string) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	beforeStore, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	before, exists := beforeStore.Bucket(name)
	if !exists {
		return cli.Result{}, &PortalError{Kind: PortalDomain, Code: "BucketNotFound"}
	}
	if before.ObjectsCount != 0 {
		return cli.Result{}, &PortalError{Kind: PortalDomain, Code: "BucketIsNotEmpty"}
	}
	_, mutationErr := control.Mutate(ctx, account, MutationRequest{
		Action: MutationBucketDelete, ServiceID: beforeStore.ServiceID,
		ObjectStoreID: beforeStore.ID, Name: name,
	})
	after, readErr := control.Inventory(ctx, account)
	if mutationErr != nil {
		if isPortalKind(mutationErr, PortalAmbiguous) && readErr == nil {
			if _, exists := after.Bucket(name); !exists {
				result := renderBucketChange("deleted", &before, nil)
				result.Warnings = append(result.Warnings, reconciledWarning())
				return result, nil
			}
		}
		return cli.Result{}, mutationErr
	}
	if readErr != nil {
		return cli.Result{}, readErr
	}
	if _, exists := after.Bucket(name); exists {
		return cli.Result{}, &PortalError{Kind: PortalAmbiguous, Code: "postcondition_not_verified"}
	}
	return renderBucketChange("deleted", &before, nil), nil
}

func (e *Executor) setServiceQuota(ctx context.Context, account profile.Account, quota int32) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	before, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	_, mutationErr := control.Mutate(ctx, account, MutationRequest{
		Action: MutationServiceQuota, ServiceID: before.ServiceID,
		ObjectStoreID: before.ID, QuotaGB: &quota,
	})
	after, readErr := control.Inventory(ctx, account)
	if mutationErr != nil {
		if isPortalKind(mutationErr, PortalAmbiguous) && readErr == nil && after.QuotaGB == quota {
			result := renderServiceChange(before, after)
			result.Warnings = append(result.Warnings, reconciledWarning())
			return result, nil
		}
		return cli.Result{}, mutationErr
	}
	if readErr != nil || after.QuotaGB != quota {
		return cli.Result{}, &PortalError{Kind: PortalAmbiguous, Code: "postcondition_not_verified", Err: readErr}
	}
	return renderServiceChange(before, after), nil
}

func (e *Executor) listCredentials(ctx context.Context, account profile.Account) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	store, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	sort.Slice(store.KeyPairs, func(first, second int) bool { return store.KeyPairs[first].ID < store.KeyPairs[second].ID })
	return renderKeyPairs(store.KeyPairs), nil
}

func (e *Executor) createCredential(ctx context.Context, account profile.Account, name string) (cli.Result, error) {
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	before, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	result, mutationErr := control.Mutate(ctx, account, MutationRequest{
		Action: MutationCredentialsCreate, ServiceID: before.ServiceID,
		ObjectStoreID: before.ID, KeyName: name,
	})
	after, readErr := control.Inventory(ctx, account)
	if mutationErr != nil {
		if isPortalKind(mutationErr, PortalAmbiguous) && readErr == nil {
			if key, exists := keyPairNamed(after.KeyPairs, name); exists {
				output := renderKeyPairChange("created", nil, &key)
				output.Warnings = append(output.Warnings, reconciledWarning())
				return output, nil
			}
		}
		return cli.Result{}, mutationErr
	}
	if result.KeyPair == nil {
		return cli.Result{}, &PortalError{Kind: PortalContract}
	}
	if readErr == nil {
		if key, exists := keyPairID(after.KeyPairs, result.KeyPair.ID); exists {
			result.KeyPair = &key
		}
	}
	output := renderKeyPairChange("created", nil, result.KeyPair)
	output.Warnings = append(output.Warnings, cli.Warning{
		Code:    "credential_not_exported",
		Message: "secret key material stayed in the portal; select the key-pair ID in the private account profile when multiple key sets exist",
	})
	return output, nil
}

func (e *Executor) revokeCredential(ctx context.Context, account profile.Account, idText string) (cli.Result, error) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		return cli.Result{}, cli.UsageError("S3 key-pair ID must be a positive integer")
	}
	control, err := e.control()
	if err != nil {
		return cli.Result{}, err
	}
	beforeStore, err := control.Inventory(ctx, account)
	if err != nil {
		return cli.Result{}, err
	}
	before, exists := keyPairID(beforeStore.KeyPairs, id)
	if !exists {
		return cli.Result{}, &PortalError{Kind: PortalDomain, Code: "ObjectStoreKeyPairNotFound"}
	}
	_, mutationErr := control.Mutate(ctx, account, MutationRequest{
		Action: MutationCredentialsRevoke, ServiceID: beforeStore.ServiceID,
		ObjectStoreID: beforeStore.ID, KeyPairID: id,
	})
	after, readErr := control.Inventory(ctx, account)
	if mutationErr != nil {
		if isPortalKind(mutationErr, PortalAmbiguous) && readErr == nil {
			if _, exists := keyPairID(after.KeyPairs, id); !exists {
				output := renderKeyPairChange("revoked", &before, nil)
				output.Warnings = append(output.Warnings, reconciledWarning())
				return output, nil
			}
		}
		return cli.Result{}, mutationErr
	}
	if readErr != nil {
		return cli.Result{}, readErr
	}
	if _, exists := keyPairID(after.KeyPairs, id); exists {
		return cli.Result{}, &PortalError{Kind: PortalAmbiguous, Code: "postcondition_not_verified"}
	}
	return renderKeyPairChange("revoked", &before, nil), nil
}

func (e *Executor) executeDataPlane(
	ctx context.Context,
	account profile.Account,
	operation cli.Operation,
) (cli.Result, error) {
	client, err := e.dataPlane(ctx, account, operation)
	if err != nil {
		return cli.Result{}, err
	}
	defer client.Close()
	bucket := argument(operation, 0)
	switch operation.Action {
	case "s3.bucket.policy.get":
		value, err := client.GetPolicy(ctx, bucket)
		return renderConfiguration(bucket, "policy", value), err
	case "s3.bucket.policy.set":
		value := json.RawMessage(parameter(operation, "document"))
		before, beforeErr := client.GetPolicy(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		if err := client.PutPolicy(ctx, bucket, value); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		after, err := client.GetPolicy(ctx, bucket)
		return renderConfigurationChange(bucket, "policy", before, after), err
	case "s3.bucket.policy.delete":
		before, beforeErr := client.GetPolicy(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		if err := client.DeletePolicy(ctx, bucket); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		return renderConfigurationChange(bucket, "policy", before, nil), nil
	case "s3.bucket.cors.get":
		value, err := client.GetCORS(ctx, bucket)
		return renderConfiguration(bucket, "cors", value), err
	case "s3.bucket.cors.set":
		var requested CORSConfiguration
		if err := decodeDocument(operation, &requested); err != nil {
			return cli.Result{}, err
		}
		before, beforeErr := client.GetCORS(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		requested, preserved := preservePortalCORS(requested)
		if err := client.PutCORS(ctx, bucket, requested); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		after, err := client.GetCORS(ctx, bucket)
		result := renderConfigurationChange(bucket, "cors", before, after)
		if preserved {
			result.Warnings = append(result.Warnings, cli.Warning{
				Code:    "regcloud_cors_preserved",
				Message: "the required https://cloud.reg.ru CORS origin was preserved",
			})
		}
		return result, err
	case "s3.bucket.cors.delete":
		before, beforeErr := client.GetCORS(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		if err := client.DeleteCORS(ctx, bucket); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		return renderConfigurationChange(bucket, "cors", before, nil), nil
	case "s3.bucket.versioning.get":
		value, err := client.GetVersioning(ctx, bucket)
		return renderConfiguration(bucket, "versioning", map[string]string{"status": value}), err
	case "s3.bucket.versioning.set":
		before, beforeErr := client.GetVersioning(ctx, bucket)
		if beforeErr != nil {
			return cli.Result{}, beforeErr
		}
		status := parameter(operation, "status")
		if err := client.PutVersioning(ctx, bucket, status); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		after, err := client.GetVersioning(ctx, bucket)
		return renderConfigurationChange(bucket, "versioning", map[string]string{"status": before}, map[string]string{"status": after}), err
	case "s3.bucket.lifecycle.get":
		value, err := client.GetLifecycle(ctx, bucket)
		return renderConfiguration(bucket, "lifecycle", value), err
	case "s3.bucket.lifecycle.set":
		var requested LifecycleConfiguration
		if err := decodeDocument(operation, &requested); err != nil {
			return cli.Result{}, err
		}
		before, beforeErr := client.GetLifecycle(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		if err := client.PutLifecycle(ctx, bucket, requested); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		after, err := client.GetLifecycle(ctx, bucket)
		return renderConfigurationChange(bucket, "lifecycle", before, after), err
	case "s3.bucket.lifecycle.delete":
		before, beforeErr := client.GetLifecycle(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		if err := client.DeleteLifecycle(ctx, bucket); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		return renderConfigurationChange(bucket, "lifecycle", before, nil), nil
	case "s3.bucket.website.get":
		value, err := client.GetWebsite(ctx, bucket)
		return renderConfiguration(bucket, "website", value), err
	case "s3.bucket.website.set":
		var requested WebsiteConfiguration
		if err := decodeDocument(operation, &requested); err != nil {
			return cli.Result{}, err
		}
		before, beforeErr := client.GetWebsite(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		if err := client.PutWebsite(ctx, bucket, requested); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		after, err := client.GetWebsite(ctx, bucket)
		return renderConfigurationChange(bucket, "website", before, after), err
	case "s3.bucket.website.delete":
		before, beforeErr := client.GetWebsite(ctx, bucket)
		if beforeErr != nil && !errors.Is(beforeErr, errNotFound) {
			return cli.Result{}, beforeErr
		}
		if err := client.DeleteWebsite(ctx, bucket); err != nil {
			return cli.Result{}, dataMutationError(err)
		}
		return renderConfigurationChange(bucket, "website", before, nil), nil
	default:
		return cli.Result{}, cli.CapabilityUnavailable(operation.Capability, "the S3 operation is not implemented")
	}
}

func (e *Executor) dataPlane(
	ctx context.Context,
	account profile.Account,
	operation cli.Operation,
) (DataPlane, error) {
	credentials, err := e.resolveCredentials(ctx, account, operation)
	if err != nil {
		return nil, err
	}
	defer credentials.Close()
	endpoint := account.Cloud.S3Endpoint
	if endpoint == "" {
		endpoint = credentials.Endpoint
	}
	if endpoint == "" {
		endpoint = e.options.Endpoint
	}
	region := account.Cloud.S3SigningRegion
	if region == "" {
		region = e.options.SigningRegion
	}
	return e.options.DataPlanes.New(DataPlaneOptions{
		Endpoint: endpoint, SigningRegion: region,
		AccessKey: credentials.AccessKey, SecretKey: credentials.SecretKey,
		HTTPClient: e.options.HTTPClient,
	})
}

func (e *Executor) resolveCredentials(
	ctx context.Context,
	account profile.Account,
	operation cli.Operation,
) (Credentials, error) {
	if len(account.CredentialProcess.Command) > 0 {
		access, err := operation.Credentials.Resolve(ctx, "s3.access_key_id")
		if err == nil {
			secret, secretErr := operation.Credentials.Resolve(ctx, "s3.secret_access_key")
			if secretErr != nil {
				wipe(access)
				return Credentials{}, secretErr
			}
			return Credentials{AccessKey: access, SecretKey: secret}, nil
		}
		var processErr *credentialprocess.ProcessError
		if !errors.As(err, &processErr) || processErr.Code != "credential_field_unavailable" {
			return Credentials{}, err
		}
	}
	control, err := e.control()
	if err != nil {
		return Credentials{}, err
	}
	return control.ResolveCredentials(ctx, account)
}

func (e *Executor) translate(operation cli.Operation, err error) error {
	var cliErr *cli.CLIError
	if errors.As(err, &cliErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch {
	case session.IsCode(err, session.CodeAccountMismatch):
		return cli.AccountMismatch(operation.Account, "")
	case session.IsCode(err, session.CodeProfileBusy):
		return cli.PortalProfileBusy()
	case session.IsCode(err, session.CodeContractDrift):
		return cli.PrivateContractDrift(operation.Capability)
	case session.IsCode(err, session.CodeSessionLost), session.IsCode(err, session.CodeNotEstablished):
		return cli.AuthenticationExpired()
	case session.IsCode(err, session.CodeBrowser):
		if errors.Is(err, cdp.ErrBrowserNotFound) {
			return cli.MissingBrowser()
		}
		return cli.BrowserSessionInterrupted()
	}
	var portalErr *PortalError
	if errors.As(err, &portalErr) {
		switch portalErr.Kind {
		case PortalUnavailable:
			return cli.CapabilityUnavailable(operation.Capability, portalUnavailableMessage(portalErr.Code))
		case PortalUnauthorized:
			return cli.AuthenticationExpired()
		case PortalContract:
			return cli.PrivateContractDrift(operation.Capability)
		case PortalAmbiguous:
			return cli.OutcomeUnknown(operation.Capability)
		case PortalDomain:
			return cli.ProviderError("REG.Cloud S3", portalErr.Code, 0, false, "")
		default:
			return cli.NetworkError("REG.Cloud S3", false)
		}
	}
	var contractErr *ContractError
	if errors.As(err, &contractErr) {
		return cli.ProviderContractDrift("REG.RU S3")
	}
	var ambiguousErr *AmbiguousMutationError
	if errors.As(err, &ambiguousErr) {
		return cli.OutcomeUnknown(operation.Capability)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden {
			return cli.ProviderAuthenticationError("REG.RU S3")
		}
		return cli.ProviderError("REG.RU S3", apiErr.Code, apiErr.StatusCode, apiErr.Retryable, apiErr.RequestID)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return cli.NetworkError("REG.RU S3", networkErr.Timeout() || networkErr.Temporary())
	}
	return cli.ConfigurationError("S3 operation configuration is invalid")
}

func portalUnavailableMessage(code string) string {
	switch code {
	case "environment-required":
		return "multiple Cloud environments exist; set cloud.environment_id in the private account profile"
	case "key-selection-required":
		return "multiple S3 key sets exist; set cloud.s3_key_pair_id in the private account profile"
	case "not-configured":
		return "REG.RU object storage is not activated for the selected Cloud environment"
	default:
		return "an active portal session and selected REG.Cloud environment are required"
	}
}

func dataMutationError(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) &&
		(apiErr.StatusCode == 0 || apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500) {
		return &AmbiguousMutationError{Err: err}
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return &AmbiguousMutationError{Err: err}
	}
	return err
}

func decodeDocument(operation cli.Operation, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(parameter(operation, "document")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return cli.UsageError("S3 configuration document has an invalid schema")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return cli.UsageError("S3 configuration document must contain one JSON value")
	}
	return nil
}

func preservePortalCORS(configuration CORSConfiguration) (CORSConfiguration, bool) {
	const portalOrigin = "https://cloud.reg.ru"
	for _, rule := range configuration.Rules {
		for _, origin := range rule.AllowedOrigins {
			if origin == portalOrigin {
				return configuration, false
			}
		}
	}
	configuration.Rules = append(configuration.Rules, CORSRule{
		ID: "regru-regcloud-panel", AllowedMethods: []string{"GET", "HEAD"},
		AllowedOrigins: []string{portalOrigin},
	})
	return configuration, true
}

func bucketMatchesCreate(bucket Bucket, quota *int32, public bool) bool {
	if !accessMatches(bucket.AccessType, public) {
		return false
	}
	return quota == nil || (bucket.QuotaGB != nil && *bucket.QuotaGB == *quota)
}

func accessMatches(value string, public bool) bool {
	normalized := strings.ToLower(value)
	if public {
		return strings.Contains(normalized, "public")
	}
	return strings.Contains(normalized, "private") || strings.Contains(normalized, "key")
}

func equalQuota(first, second *int32) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func keyPairID(values []KeyPair, id int64) (KeyPair, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return KeyPair{}, false
}

func keyPairNamed(values []KeyPair, name string) (KeyPair, bool) {
	for _, value := range values {
		if value.Name == name {
			return value, true
		}
	}
	return KeyPair{}, false
}

func argument(operation cli.Operation, index int) string {
	if index < 0 || index >= len(operation.Arguments) {
		return ""
	}
	return operation.Arguments[index]
}

func parameter(operation cli.Operation, name string) string {
	values := operation.Parameters[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func int32Parameter(operation cli.Operation, name string) int32 {
	value, _ := strconv.ParseInt(parameter(operation, name), 10, 32)
	return int32(value)
}

func reconciledWarning() cli.Warning {
	return cli.Warning{
		Code:    "outcome_reconciled",
		Message: "the private mutation response was ambiguous, but an independent read proved the requested postcondition",
	}
}

func renderService(store ObjectStore) cli.Result {
	return cli.Result{
		Human:    fmt.Sprintf("S3 service: %s, %d/%d buckets, size %s, quota %d GB", store.Status, store.BucketCount, store.BucketLimit, providerSizeText(store.Size, store.SizeUnit), store.QuotaGB),
		Plain:    []string{fmt.Sprintf("%s\t%t\t%d\t%d\t%d\t%s\t%s", plain(store.Status), store.Locked, store.QuotaGB, store.BucketCount, store.BucketLimit, plain(string(store.Size)), plain(store.SizeUnit))},
		Data:     store,
		Warnings: privatePortalWarnings(),
	}
}

func renderBuckets(buckets []Bucket) cli.Result {
	lines := make([]string, 0, len(buckets))
	human := make([]string, 0, len(buckets)+1)
	human = append(human, fmt.Sprintf("%d S3 buckets", len(buckets)))
	for _, bucket := range buckets {
		quota := int32(0)
		if bucket.QuotaGB != nil {
			quota = *bucket.QuotaGB
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%d\t%s\t%s", plain(bucket.Name), plain(bucket.AccessType), quota, bucket.ObjectsCount, plain(string(bucket.Size)), plain(bucket.SizeUnit)))
		human = append(human, fmt.Sprintf("%s: %s, %d objects, size %s", bucket.Name, bucket.AccessType, bucket.ObjectsCount, providerSizeText(bucket.Size, bucket.SizeUnit)))
	}
	return cli.Result{
		Human: strings.Join(human, "\n"), Plain: lines, Data: buckets,
		Warnings: privatePortalWarnings(),
	}
}

func renderBucket(bucket Bucket) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("S3 bucket %s: %s, %d objects, size %s", bucket.Name, bucket.AccessType, bucket.ObjectsCount, providerSizeText(bucket.Size, bucket.SizeUnit)),
		Plain: []string{fmt.Sprintf("%s\t%s\t%d\t%s\t%s", plain(bucket.Name), plain(bucket.AccessType), bucket.ObjectsCount, plain(string(bucket.Size)), plain(bucket.SizeUnit))},
		Data:  bucket, Warnings: privatePortalWarnings(),
	}
}

func providerSizeText(size ProviderSize, unit string) string {
	if unit == "" {
		return string(size)
	}
	return fmt.Sprintf("%s %s", size, unit)
}

func renderBucketChange(action string, before, after *Bucket) cli.Result {
	name := ""
	if after != nil {
		name = after.Name
	} else if before != nil {
		name = before.Name
	}
	return cli.Result{
		Human:    fmt.Sprintf("S3 bucket %s %s", name, action),
		Plain:    []string{fmt.Sprintf("%s\t%s", plain(name), action)},
		Data:     map[string]any{"action": action, "before": before, "after": after},
		Warnings: privatePortalWarnings(),
	}
}

func renderServiceChange(before, after ObjectStore) cli.Result {
	return cli.Result{
		Human:    fmt.Sprintf("S3 service quota updated from %d GB to %d GB", before.QuotaGB, after.QuotaGB),
		Plain:    []string{fmt.Sprintf("quota\t%d\t%d", before.QuotaGB, after.QuotaGB)},
		Data:     map[string]any{"action": "updated", "before": before, "after": after},
		Warnings: privatePortalWarnings(),
	}
}

func renderKeyPairs(values []KeyPair) cli.Result {
	lines := make([]string, 0, len(values))
	for _, value := range values {
		lines = append(lines, fmt.Sprintf("%d\t%s\t%s", value.ID, plain(value.Name), value.CreatedAt.Format("2006-01-02T15:04:05Z07:00")))
	}
	return cli.Result{
		Human: fmt.Sprintf("%d S3 key sets", len(values)), Plain: lines, Data: values,
		Warnings: privatePortalWarnings(),
	}
}

func renderKeyPairChange(action string, before, after *KeyPair) cli.Result {
	var selected *KeyPair
	if after != nil {
		selected = after
	} else {
		selected = before
	}
	return cli.Result{
		Human:    fmt.Sprintf("S3 key set %s %s", selected.Name, action),
		Plain:    []string{fmt.Sprintf("%d\t%s\t%s", selected.ID, plain(selected.Name), action)},
		Data:     map[string]any{"action": action, "before": before, "after": after},
		Warnings: privatePortalWarnings(),
	}
}

func renderConfiguration(bucket, kind string, value any) cli.Result {
	encoded, _ := json.Marshal(value)
	return cli.Result{
		Human: fmt.Sprintf("S3 bucket %s %s configuration", bucket, kind),
		Plain: []string{fmt.Sprintf("%s\t%s\t%s", plain(bucket), kind, plain(string(encoded)))},
		Data:  map[string]any{"bucket": bucket, "kind": kind, "configuration": value},
	}
}

func renderConfigurationChange(bucket, kind string, before, after any) cli.Result {
	return cli.Result{
		Human: fmt.Sprintf("S3 bucket %s %s configuration updated", bucket, kind),
		Plain: []string{fmt.Sprintf("%s\t%s\tupdated", plain(bucket), kind)},
		Data:  map[string]any{"bucket": bucket, "kind": kind, "before": before, "after": after},
	}
}

func privatePortalWarnings() []cli.Warning {
	return []cli.Warning{{
		Code:    "experimental_private_portal",
		Message: "REG.Cloud S3 control-plane integration is private and fails closed on schema drift",
	}}
}

func plain(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\t", "\\t")
	value = strings.ReplaceAll(value, "\r", "\\r")
	return strings.ReplaceAll(value, "\n", "\\n")
}
