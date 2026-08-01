package s3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

const (
	programInventory   session.ProgramID = "portal.s3.inventory"
	programMutation    session.ProgramID = "portal.s3.mutation"
	programCredentials session.ProgramID = "portal.s3.credentials"
)

type SessionBroker interface {
	WithSession(context.Context, session.Profile, func(session.PageExecutor) error) error
}

type ControlPlane interface {
	Inventory(context.Context, profile.Account) (ObjectStore, error)
	Mutate(context.Context, profile.Account, MutationRequest) (MutationResult, error)
	ResolveCredentials(context.Context, profile.Account) (Credentials, error)
}

type PortalControlPlane struct{ broker SessionBroker }

func NewPortalControlPlane(broker SessionBroker) *PortalControlPlane {
	return &PortalControlPlane{broker: broker}
}

type portalEnvelope struct {
	State       string          `json:"state"`
	ServiceID   string          `json:"serviceId"`
	TypeName    string          `json:"typename"`
	Status      int             `json:"status"`
	ObjectStore json.RawMessage `json:"objectStore"`
	Result      json.RawMessage `json:"result"`
	Endpoint    string          `json:"endpoint"`
	AccessKey   string          `json:"accessKey"`
	SecretKey   string          `json:"secretKey"`
}

func (c *PortalControlPlane) Inventory(
	ctx context.Context,
	account profile.Account,
) (ObjectStore, error) {
	var envelope portalEnvelope
	err := c.run(ctx, account, programInventory, map[string]any{
		"serviceId": account.Cloud.EnvironmentID,
	}, &envelope)
	if err != nil {
		return ObjectStore{}, err
	}
	if err := portalStateError(envelope); err != nil {
		return ObjectStore{}, err
	}
	if envelope.State != "available" {
		return ObjectStore{}, &PortalError{Kind: PortalContract}
	}
	var raw struct {
		ID          int64         `json:"id"`
		Name        string        `json:"name"`
		Status      string        `json:"status"`
		Locked      bool          `json:"isLocked"`
		BucketCount int           `json:"bucketCount"`
		BucketLimit int           `json:"bucketLimit"`
		Size        *ProviderSize `json:"size"`
		SizeUnit    string        `json:"sizeUnit"`
		MaxQuotaGB  int32         `json:"maxQuotaGb"`
		QuotaGB     int32         `json:"quotaGb"`
		Buckets     []struct {
			Name                   string        `json:"name"`
			Size                   *ProviderSize `json:"size"`
			SizeUnit               string        `json:"sizeUnit"`
			QuotaGB                *int32        `json:"quotaGb"`
			ObjectsCount           int64         `json:"objectsCount"`
			AccessType             string        `json:"accessType"`
			VersioningEnabled      bool          `json:"isVersioningEnabled"`
			PathStyleLink          string        `json:"pathStyleLink"`
			VirtualHostedStyleLink string        `json:"virtualHostedStyleLink"`
		} `json:"buckets"`
		KeyPairs []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			InstanceID string `json:"instanceId"`
			Server     string `json:"server"`
			CreatedAt  string `json:"createdAt"`
		} `json:"keypairs"`
	}
	if err := json.Unmarshal(envelope.ObjectStore, &raw); err != nil || raw.ID <= 0 || raw.Size == nil {
		return ObjectStore{}, &PortalError{Kind: PortalContract, Err: err}
	}
	store := ObjectStore{
		ServiceID: envelope.ServiceID, ID: raw.ID, Name: raw.Name,
		Status: raw.Status, Locked: raw.Locked, BucketCount: raw.BucketCount,
		BucketLimit: raw.BucketLimit, Size: *raw.Size, SizeUnit: raw.SizeUnit,
		MaxQuotaGB: raw.MaxQuotaGB, QuotaGB: raw.QuotaGB,
		Buckets:  make([]Bucket, 0, len(raw.Buckets)),
		KeyPairs: make([]KeyPair, 0, len(raw.KeyPairs)),
	}
	for _, item := range raw.Buckets {
		if item.Size == nil {
			return ObjectStore{}, &PortalError{Kind: PortalContract}
		}
		store.Buckets = append(store.Buckets, Bucket{
			Name: item.Name, Size: *item.Size, SizeUnit: item.SizeUnit,
			QuotaGB: item.QuotaGB, ObjectsCount: item.ObjectsCount,
			AccessType: item.AccessType, VersioningEnabled: item.VersioningEnabled,
			PathStyleLink:          item.PathStyleLink,
			VirtualHostedStyleLink: item.VirtualHostedStyleLink,
		})
	}
	for _, item := range raw.KeyPairs {
		if item.ID <= 0 {
			return ObjectStore{}, &PortalError{Kind: PortalContract}
		}
		createdAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
		store.KeyPairs = append(store.KeyPairs, KeyPair{
			ID: item.ID, Name: item.Name, InstanceID: item.InstanceID,
			Server: item.Server, CreatedAt: createdAt,
		})
	}
	return store, nil
}

func (c *PortalControlPlane) Mutate(
	ctx context.Context,
	account profile.Account,
	request MutationRequest,
) (MutationResult, error) {
	arguments := map[string]any{
		"serviceId": request.ServiceID, "action": request.Action,
		"objectStoreId": request.ObjectStoreID, "name": request.Name,
		"quotaGb": request.QuotaGB, "keyPairId": request.KeyPairID,
		"keyName": request.KeyName,
	}
	if request.Public != nil {
		arguments["isPublic"] = *request.Public
	}
	var envelope portalEnvelope
	if err := c.run(ctx, account, programMutation, arguments, &envelope); err != nil {
		return MutationResult{}, err
	}
	if err := portalStateError(envelope); err != nil {
		return MutationResult{}, err
	}
	if envelope.State != "delivered" || envelope.TypeName == "" {
		return MutationResult{}, &PortalError{Kind: PortalContract}
	}
	if !successType(request.Action, envelope.TypeName) {
		if knownDomainType(envelope.TypeName) {
			return MutationResult{}, &PortalError{Kind: PortalDomain, Code: envelope.TypeName}
		}
		return MutationResult{}, &PortalError{Kind: PortalContract, Code: envelope.TypeName}
	}
	result := MutationResult{TypeName: envelope.TypeName}
	switch envelope.TypeName {
	case "Bucket":
		var bucket Bucket
		if err := json.Unmarshal(envelope.Result, &bucket); err != nil || bucket.Name == "" {
			return MutationResult{}, &PortalError{Kind: PortalContract, Err: err}
		}
		result.Bucket = &bucket
	case "ObjectStore":
		var store ObjectStore
		if err := json.Unmarshal(envelope.Result, &store); err != nil || store.ID == 0 {
			return MutationResult{}, &PortalError{Kind: PortalContract, Err: err}
		}
		result.Store = &store
	case "KeyPair":
		var raw struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			InstanceID string `json:"instanceId"`
			CreatedAt  string `json:"createdAt"`
		}
		if err := json.Unmarshal(envelope.Result, &raw); err != nil || raw.ID == 0 {
			return MutationResult{}, &PortalError{Kind: PortalContract, Err: err}
		}
		createdAt, _ := time.Parse(time.RFC3339, raw.CreatedAt)
		result.KeyPair = &KeyPair{ID: raw.ID, Name: raw.Name, InstanceID: raw.InstanceID, CreatedAt: createdAt}
	}
	return result, nil
}

func (c *PortalControlPlane) ResolveCredentials(
	ctx context.Context,
	account profile.Account,
) (Credentials, error) {
	arguments := map[string]any{"serviceId": account.Cloud.EnvironmentID}
	if account.Cloud.S3KeyPairID != "" {
		keyPairID, err := strconv.ParseInt(account.Cloud.S3KeyPairID, 10, 64)
		if err != nil || keyPairID <= 0 {
			return Credentials{}, &PortalError{Kind: PortalUnavailable, Code: "invalid_key_pair_selection"}
		}
		arguments["keyPairId"] = keyPairID
	}
	var envelope portalEnvelope
	if err := c.run(ctx, account, programCredentials, arguments, &envelope); err != nil {
		return Credentials{}, err
	}
	if err := portalStateError(envelope); err != nil {
		return Credentials{}, err
	}
	if envelope.State != "available" || envelope.AccessKey == "" || envelope.SecretKey == "" {
		return Credentials{}, &PortalError{Kind: PortalContract}
	}
	return Credentials{
		Endpoint:  envelope.Endpoint,
		AccessKey: []byte(envelope.AccessKey),
		SecretKey: []byte(envelope.SecretKey),
	}, nil
}

func (c *PortalControlPlane) run(
	ctx context.Context,
	account profile.Account,
	program session.ProgramID,
	arguments any,
	destination any,
) error {
	if c == nil || c.broker == nil || account.Portal.SessionRef == "" {
		return &PortalError{Kind: PortalUnavailable, Code: "portal_session_required"}
	}
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return &PortalError{Kind: PortalContract, Err: err}
	}
	var result json.RawMessage
	err = c.broker.WithSession(ctx, session.Profile{
		ID: account.ID, SessionRef: account.Portal.SessionRef,
	}, func(page session.PageExecutor) error {
		return page.RunJSON(ctx, program, encoded, &result)
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(result, destination); err != nil {
		return &PortalError{Kind: PortalContract, Err: err}
	}
	return nil
}

func portalStateError(envelope portalEnvelope) error {
	switch envelope.State {
	case "available", "delivered":
		return nil
	case "unauthorized":
		return &PortalError{Kind: PortalUnauthorized}
	case "network":
		return &PortalError{Kind: PortalNetwork, Code: fmt.Sprintf("http_%d", envelope.Status)}
	case "ambiguous":
		return &PortalError{Kind: PortalAmbiguous}
	case "not-configured", "no-environment", "environment-required", "key-selection-required":
		return &PortalError{Kind: PortalUnavailable, Code: envelope.State}
	case "drift", "":
		return &PortalError{Kind: PortalContract}
	default:
		return &PortalError{Kind: PortalContract, Code: envelope.State}
	}
}

func successType(action MutationAction, typeName string) bool {
	switch action {
	case MutationBucketCreate, MutationBucketDelete, MutationBucketPrivacy, MutationBucketQuota:
		return typeName == "Bucket"
	case MutationServiceQuota:
		return typeName == "ObjectStore"
	case MutationCredentialsCreate, MutationCredentialsRevoke:
		return typeName == "KeyPair"
	default:
		return false
	}
}

func knownDomainType(typeName string) bool {
	for _, known := range []string{
		"ObjectStoreNotFound", "BucketNotFound", "BucketIsNotEmpty",
		"InvalidBucketName", "BucketNameConflict", "BucketLimitExceeded",
		"InvalidQuota", "UnavailableQuota", "ObjectStoreKeyPairAlreadyExists",
		"ObjectStoreKeyPairInvalidName", "ObjectStoreKeyPairNotFound", "Unauthorized",
	} {
		if typeName == known {
			return true
		}
	}
	return false
}

func isPortalKind(err error, kind PortalErrorKind) bool {
	var portalErr *PortalError
	return errors.As(err, &portalErr) && portalErr.Kind == kind
}
