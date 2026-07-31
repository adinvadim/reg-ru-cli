package s3

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/profile"
)

type fakeControlPlane struct {
	store       ObjectStore
	mutations   []MutationRequest
	mutationErr error
	credentials Credentials
	inventories int
}

func (f *fakeControlPlane) Inventory(context.Context, profile.Account) (ObjectStore, error) {
	f.inventories++
	return cloneStore(f.store), nil
}

func (f *fakeControlPlane) Mutate(_ context.Context, _ profile.Account, request MutationRequest) (MutationResult, error) {
	f.mutations = append(f.mutations, request)
	result := MutationResult{}
	switch request.Action {
	case MutationBucketCreate:
		access := "private_by_keys"
		if request.Public != nil && *request.Public {
			access = "public_read"
		}
		bucket := Bucket{Name: request.Name, QuotaGB: request.QuotaGB, AccessType: access}
		f.store.Buckets = append(f.store.Buckets, bucket)
		f.store.BucketCount = len(f.store.Buckets)
		result = MutationResult{TypeName: "Bucket", Bucket: &bucket}
	case MutationBucketDelete:
		remaining := f.store.Buckets[:0]
		var removed Bucket
		for _, bucket := range f.store.Buckets {
			if bucket.Name == request.Name {
				removed = bucket
				continue
			}
			remaining = append(remaining, bucket)
		}
		f.store.Buckets = remaining
		f.store.BucketCount = len(remaining)
		result = MutationResult{TypeName: "Bucket", Bucket: &removed}
	case MutationBucketPrivacy:
		for index := range f.store.Buckets {
			if f.store.Buckets[index].Name == request.Name {
				if request.Public != nil && *request.Public {
					f.store.Buckets[index].AccessType = "public_read"
				} else {
					f.store.Buckets[index].AccessType = "private_by_keys"
				}
			}
		}
		result.TypeName = "Bucket"
	case MutationBucketQuota:
		for index := range f.store.Buckets {
			if f.store.Buckets[index].Name == request.Name {
				f.store.Buckets[index].QuotaGB = request.QuotaGB
			}
		}
		result.TypeName = "Bucket"
	case MutationServiceQuota:
		f.store.QuotaGB = *request.QuotaGB
		result = MutationResult{TypeName: "ObjectStore", Store: &f.store}
	}
	return result, f.mutationErr
}

func (f *fakeControlPlane) ResolveCredentials(context.Context, profile.Account) (Credentials, error) {
	return Credentials{
		Endpoint:  f.credentials.Endpoint,
		AccessKey: append([]byte(nil), f.credentials.AccessKey...),
		SecretKey: append([]byte(nil), f.credentials.SecretKey...),
	}, nil
}

func TestExecutorCreatesAndDeletesBucketsWithReadBeforeWrite(t *testing.T) {
	quota := int32(25)
	control := &fakeControlPlane{store: ObjectStore{ServiceID: "service", ID: 7}}
	executor := newTestExecutor(control, nil)
	created, err := executor.Execute(context.Background(), testOperation("s3.bucket.create", []string{"regru-test-bucket"}, map[string][]string{
		"quota-gb": {"25"}, "access": {"private"},
	}))
	if err != nil {
		t.Fatalf("create error = %v", err)
	}
	if control.inventories != 2 || len(control.mutations) != 1 {
		t.Fatalf("create calls: inventories=%d mutations=%d", control.inventories, len(control.mutations))
	}
	request := control.mutations[0]
	if request.Action != MutationBucketCreate || request.QuotaGB == nil || *request.QuotaGB != quota {
		t.Fatalf("create request = %#v", request)
	}
	change := created.Data.(map[string]any)
	if change["before"] != (*Bucket)(nil) || change["after"] == nil {
		t.Errorf("create plan = %#v", change)
	}

	control.inventories = 0
	control.mutations = nil
	deleted, err := executor.Execute(context.Background(), testOperation("s3.bucket.delete", []string{"regru-test-bucket"}, nil))
	if err != nil {
		t.Fatalf("delete error = %v", err)
	}
	if control.inventories != 2 || len(control.mutations) != 1 {
		t.Fatalf("delete calls: inventories=%d mutations=%d", control.inventories, len(control.mutations))
	}
	if deleted.Data.(map[string]any)["after"] != (*Bucket)(nil) {
		t.Errorf("delete plan = %#v", deleted.Data)
	}
}

func TestExecutorNeverAttemptsRecursiveBucketDeletion(t *testing.T) {
	control := &fakeControlPlane{store: ObjectStore{
		ServiceID: "service", ID: 7,
		Buckets: []Bucket{{Name: "nonempty-bucket", ObjectsCount: 3}},
	}}
	executor := newTestExecutor(control, nil)
	_, err := executor.Execute(context.Background(), testOperation("s3.bucket.delete", []string{"nonempty-bucket"}, nil))
	var cliErr *cli.CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != cli.CodeNetwork {
		t.Fatalf("delete error = %#v", err)
	}
	if len(control.mutations) != 0 {
		t.Fatalf("non-empty bucket triggered %d mutations", len(control.mutations))
	}
}

func TestExecutorReconcilesAmbiguousCreateWithoutRetry(t *testing.T) {
	control := &fakeControlPlane{
		store:       ObjectStore{ServiceID: "service", ID: 7},
		mutationErr: &PortalError{Kind: PortalAmbiguous},
	}
	executor := newTestExecutor(control, nil)
	result, err := executor.Execute(context.Background(), testOperation("s3.bucket.create", []string{"reconciled-bucket"}, map[string][]string{
		"access": {"private"},
	}))
	if err != nil {
		t.Fatalf("create error = %v", err)
	}
	if len(control.mutations) != 1 {
		t.Fatalf("ambiguous mutation was retried: %d calls", len(control.mutations))
	}
	if len(result.Warnings) < 2 || result.Warnings[len(result.Warnings)-1].Code != "outcome_reconciled" {
		t.Errorf("warnings = %#v", result.Warnings)
	}
}

type fakeDataPlaneFactory struct {
	options DataPlaneOptions
	access  string
	client  *fakeDataPlane
}

func (f *fakeDataPlaneFactory) New(options DataPlaneOptions) (DataPlane, error) {
	f.options = options
	f.access = string(options.AccessKey)
	return f.client, nil
}

type fakeDataPlane struct {
	closed       bool
	policy       json.RawMessage
	putPolicyErr error
	cors         CORSConfiguration
}

func (f *fakeDataPlane) Close() { f.closed = true }
func (f *fakeDataPlane) GetPolicy(context.Context, string) (json.RawMessage, error) {
	return append(json.RawMessage(nil), f.policy...), nil
}
func (f *fakeDataPlane) PutPolicy(_ context.Context, _ string, value json.RawMessage) error {
	if f.putPolicyErr != nil {
		return f.putPolicyErr
	}
	f.policy = append(f.policy[:0], value...)
	return nil
}
func (f *fakeDataPlane) DeletePolicy(context.Context, string) error { f.policy = nil; return nil }
func (f *fakeDataPlane) GetCORS(context.Context, string) (CORSConfiguration, error) {
	return f.cors, nil
}
func (f *fakeDataPlane) PutCORS(_ context.Context, _ string, value CORSConfiguration) error {
	f.cors = value
	return nil
}
func (f *fakeDataPlane) DeleteCORS(context.Context, string) error              { return nil }
func (f *fakeDataPlane) GetVersioning(context.Context, string) (string, error) { return "Enabled", nil }
func (f *fakeDataPlane) PutVersioning(context.Context, string, string) error   { return nil }
func (f *fakeDataPlane) GetLifecycle(context.Context, string) (LifecycleConfiguration, error) {
	return LifecycleConfiguration{}, nil
}
func (f *fakeDataPlane) PutLifecycle(context.Context, string, LifecycleConfiguration) error {
	return nil
}
func (f *fakeDataPlane) DeleteLifecycle(context.Context, string) error { return nil }
func (f *fakeDataPlane) GetWebsite(context.Context, string) (WebsiteConfiguration, error) {
	return WebsiteConfiguration{}, nil
}
func (f *fakeDataPlane) PutWebsite(context.Context, string, WebsiteConfiguration) error { return nil }
func (f *fakeDataPlane) DeleteWebsite(context.Context, string) error                    { return nil }

func TestExecutorUsesPortalCredentialsInMemoryAndPreservesPanelCORS(t *testing.T) {
	data := &fakeDataPlane{}
	factory := &fakeDataPlaneFactory{client: data}
	control := &fakeControlPlane{credentials: Credentials{
		Endpoint: "https://s3.example.test", AccessKey: []byte("access"), SecretKey: []byte("secret"),
	}}
	executor := newTestExecutor(control, factory)
	document := `{"rules":[{"allowedMethods":["GET"],"allowedOrigins":["https://example.test"]}]}`
	result, err := executor.Execute(context.Background(), testOperation("s3.bucket.cors.set", []string{"valid-bucket"}, map[string][]string{
		"document": {document},
	}))
	if err != nil {
		t.Fatalf("CORS set error = %v", err)
	}
	if factory.options.Endpoint != "https://s3.example.test" || factory.access != "access" {
		t.Errorf("data-plane options = %#v", factory.options)
	}
	foundPortal := false
	for _, rule := range data.cors.Rules {
		for _, origin := range rule.AllowedOrigins {
			foundPortal = foundPortal || origin == "https://cloud.reg.ru"
		}
	}
	if !foundPortal {
		t.Errorf("portal CORS origin was not preserved: %#v", data.cors)
	}
	if !data.closed {
		t.Error("data-plane client was not closed")
	}
	foundWarning := false
	for _, warning := range result.Warnings {
		foundWarning = foundWarning || warning.Code == "regcloud_cors_preserved"
	}
	if !foundWarning {
		t.Errorf("warnings = %#v", result.Warnings)
	}
}

func TestExecutorReportsAmbiguousSignedMutationWithoutRetry(t *testing.T) {
	data := &fakeDataPlane{
		policy:       json.RawMessage(`{"Version":"2012-10-17"}`),
		putPolicyErr: &APIError{StatusCode: 503, Code: "ServiceUnavailable"},
	}
	factory := &fakeDataPlaneFactory{client: data}
	control := &fakeControlPlane{credentials: Credentials{
		AccessKey: []byte("access"), SecretKey: []byte("secret"),
	}}
	executor := newTestExecutor(control, factory)
	_, err := executor.Execute(context.Background(), testOperation("s3.bucket.policy.set", []string{"valid-bucket"}, map[string][]string{
		"document": {`{"Version":"2012-10-17","Statement":[]}`},
	}))
	var cliErr *cli.CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != cli.CodeOutcomeUnknown {
		t.Fatalf("mutation error = %#v", err)
	}
}

func newTestExecutor(control ControlPlane, factory DataPlaneFactory) *Executor {
	account := profile.Account{
		ID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Provider: "reg.ru",
		Portal: profile.Portal{SessionRef: "s_bbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	repository := profile.NewMemoryRepository(profile.Config{
		SchemaVersion: profile.SchemaVersion,
		Accounts:      map[string]profile.Account{"personal": account},
	})
	return NewExecutor(repository, ExecutorOptions{ControlPlane: control, DataPlanes: factory}, nil)
}

func testOperation(action string, arguments []string, parameters map[string][]string) cli.Operation {
	return cli.Operation{
		Action: action, Capability: "s3.test", Account: "personal",
		ProfileID: "p_aaaaaaaaaaaaaaaaaaaaaaaaaa", Arguments: arguments,
		Parameters: parameters,
	}
}

func cloneStore(store ObjectStore) ObjectStore {
	store.Buckets = append([]Bucket(nil), store.Buckets...)
	store.KeyPairs = append([]KeyPair(nil), store.KeyPairs...)
	return store
}
