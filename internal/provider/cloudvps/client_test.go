package cloudvps

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
)

func TestClientNormalizesV1ResourcesAndUsesBearerAuthentication(t *testing.T) {
	resources := fixture(t, "resources.json")
	servers := fixture(t, "servers.json")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/reglets":
			_, _ = writer.Write(servers)
		case "/v1/ips", "/v1/account/keys", "/v1/snapshots":
			_, _ = writer.Write(resources)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, nil, nil)
	defer client.Close()
	ctx := context.Background()

	list, err := client.ListServers(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "42" {
		t.Fatalf("ListServers() = %+v, %v", list, err)
	}
	addresses, err := client.ListIPs(ctx, "42")
	if err != nil || len(addresses) != 1 || addresses[0].ServerID != "42" {
		t.Fatalf("ListIPs() = %+v, %v", addresses, err)
	}
	keys, err := client.ListSSHKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].ID != "6" {
		t.Fatalf("ListSSHKeys() = %+v, %v", keys, err)
	}
	snapshots, err := client.ListSnapshots(ctx, "openstack-msk3")
	if err != nil || len(snapshots) != 1 || snapshots[0].ID != "7" {
		t.Fatalf("ListSnapshots() = %+v, %v", snapshots, err)
	}

	for _, expected := range []string{
		"GET /v1/reglets",
		"GET /v1/ips?reglet_id=42",
		"GET /v1/account/keys",
		"GET /v1/snapshots?region=openstack-msk3",
	} {
		if !contains(requests, expected) {
			t.Errorf("requests = %v, missing %q", requests, expected)
		}
	}
}

func TestClientDecodesFinancialSnapshotsWithoutFloatConversion(t *testing.T) {
	observedAt := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/balance_data":
			_, _ = io.WriteString(writer, `{
				"balance_data":{
					"balance":2154.550,
					"bonus_balance":41736.72,
					"days_left":424,
					"detalization":[{
						"plan":"cloud-2","type":"reglet","price":"4.20",
						"price_month":"2800.00","resource_id":9007199254740993,
						"linked":[]
					}],
					"hourly_cost":4.31128,
					"hours_left":10180,
					"monthly_cost":2896.83,
					"state":"active"
				}
			}`)
		case "/v1/billing_history":
			_, _ = io.WriteString(writer, `{
				"billing_history":[
					{"amount":"100.00","date":"2026-07-30 11:22:09","description_params":{},"type":"refill"},
					{"amount":"5.50","date":"2026-07-31 12:00:00","description_params":null,"type":"refill_bonus"}
				]
			}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New([]byte("fixture-token"), ClientOptions{
		BaseURL: server.URL, HTTPClient: server.Client(), RequestTimeout: time.Second,
		Now: func() time.Time { return observedAt },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer client.Close()

	balance, err := client.GetBalanceData(context.Background())
	if err != nil {
		t.Fatalf("GetBalanceData: %v", err)
	}
	if balance.Cash != "2154.550" || balance.Bonus != "41736.72" || balance.Currency != "RUB" {
		t.Fatalf("balance = %+v", balance)
	}
	if !balance.ObservedAt.Equal(observedAt) || len(balance.Resources) != 1 {
		t.Fatalf("balance snapshot = %+v", balance)
	}
	if balance.Resources[0].ResourceID != "9007199254740993" || balance.Resources[0].Price != "4.20" {
		t.Fatalf("resource = %+v", balance.Resources[0])
	}

	history, err := client.GetBillingHistory(context.Background())
	if err != nil {
		t.Fatalf("GetBillingHistory: %v", err)
	}
	if !history.ObservedAt.Equal(observedAt) || len(history.Refills) != 2 {
		t.Fatalf("history = %+v", history)
	}
	if history.Refills[0].Kind != "cloudvps_refill" || history.Refills[1].Kind != "cloudvps_bonus_refill" {
		t.Fatalf("refill kinds = %+v", history.Refills)
	}
}

func TestClientRejectsMissingFinancialEnvelopeAndUnsafeDecimal(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"balance_data":{"balance":1e999,"bonus_balance":0,"days_left":0,"detalization":[],"hourly_cost":0,"hours_left":0,"monthly_cost":0}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, body)
		}))
		client := testClient(t, server.URL, nil, nil)
		_, err := client.GetBalanceData(context.Background())
		client.Close()
		server.Close()
		var contractErr *ContractError
		if !errors.As(err, &contractErr) {
			t.Errorf("body %s error = %T %v", body, err, err)
		}
	}
}

func TestCreateServerUsesV1AndWaitNormalizesStringAction(t *testing.T) {
	create := fixture(t, "create.json")
	completed := fixture(t, "action-completed.json")
	var createBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/reglets":
			if err := json.NewDecoder(request.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write(create)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/actions/chain_700":
			_, _ = writer.Write(completed)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, nil, nil)
	defer client.Close()
	mutation, err := client.CreateServer(context.Background(), CreateServerRequest{
		Size:    "cloud-2",
		Image:   "ubuntu-24-04-amd64",
		Name:    "created-vps",
		SSHKeys: []any{int64(6), "aa:bb"},
		Region:  "openstack-msk3",
	})
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if mutation.Resource.ID != "43" || len(mutation.Actions) != 1 {
		t.Fatalf("mutation = %+v", mutation)
	}
	if mutation.Actions[0].ID != "chain_700" ||
		mutation.Actions[0].Status != "in-progress" {
		t.Fatalf("action was not normalized: %+v", mutation.Actions[0])
	}
	if createBody["size"] != "cloud-2" || createBody["image"] != "ubuntu-24-04-amd64" {
		t.Errorf("create body = %#v", createBody)
	}

	action, err := client.WaitAction(
		context.Background(),
		string(mutation.Actions[0].ID),
		WaitOptions{},
	)
	if err != nil || action.Status != "completed" {
		t.Fatalf("WaitAction() = %+v, %v", action, err)
	}
}

func TestCatalogUsesV2RequiredPaginationAndFilters(t *testing.T) {
	catalog := fixture(t, "catalog.json")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		requests = append(requests, request.URL.RequestURI())
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(catalog)
	}))
	defer server.Close()

	client := testClient(t, server.URL, nil, nil)
	defer client.Close()
	plans, err := client.ListPlans(context.Background(), CatalogQuery{
		Region:   "openstack-msk3",
		PlanLine: "cloud",
		Unit:     "month",
	})
	if err != nil || len(plans) != 1 || plans[0].Slug != "cloud-2" {
		t.Fatalf("ListPlans() = %+v, %v", plans, err)
	}
	private := true
	images, err := client.ListImages(context.Background(), CatalogQuery{
		Region:       "openstack-msk3",
		ImageType:    "distribution",
		PrivateImage: &private,
	})
	if err != nil || len(images) != 1 || images[0].Slug != "ubuntu-24-04-amd64" {
		t.Fatalf("ListImages() = %+v, %v", images, err)
	}
	for _, request := range requests {
		if !strings.Contains(request, "page=1") ||
			!strings.Contains(request, "items_per_page=100") ||
			!strings.Contains(request, "region=openstack-msk3") {
			t.Errorf("required v2 query is missing: %s", request)
		}
	}
}

func TestReadRetriesTransientFailureButMutationDoesNotReplay(t *testing.T) {
	servers := fixture(t, "servers.json")
	var mutex sync.Mutex
	readAttempts := 0
	mutationAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		mutex.Lock()
		defer mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			readAttempts++
			if readAttempts < 3 {
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(writer, `{"code":"TEMPORARY","message":"retry"}`)
				return
			}
			_, _ = writer.Write(servers)
			return
		}
		mutationAttempts++
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"code":"TEMPORARY","message":"do not replay"}`)
	}))
	defer server.Close()

	var delays []time.Duration
	client := testClient(
		t,
		server.URL,
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
		func(delay time.Duration) time.Duration { return delay },
	)
	defer client.Close()
	if _, err := client.ListServers(context.Background()); err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if readAttempts != 3 || len(delays) != 2 {
		t.Fatalf("read attempts = %d, delays = %v", readAttempts, delays)
	}
	err := client.DeleteServer(context.Background(), "42")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || mutationAttempts != 1 {
		t.Fatalf("DeleteServer error = %v, attempts = %d", err, mutationAttempts)
	}
}

func TestExecutorDispatchesCloudVPSAndRedactsProviderFailure(t *testing.T) {
	servers := fixture(t, "servers.json")
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/reglets" {
			_, _ = writer.Write(servers)
			return
		}
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"code":"INVALID","message":"fixture-token leaked"}`)
	}))
	defer server.Close()

	executor := NewExecutor(ExecutorOptions{BaseURL: server.URL}, nil)
	result, err := executor.Execute(context.Background(), cli.Operation{
		Action:     "vps.list",
		Capability: "cloudvps.instances",
		Credentials: resolverFunc(func(context.Context, string) ([]byte, error) {
			return []byte("fixture-token"), nil
		}),
	})
	if err != nil {
		t.Fatalf("Execute list: %v", err)
	}
	if !strings.Contains(result.Human, "1 CloudVPS") {
		t.Errorf("result = %+v", result)
	}

	_, err = executor.Execute(context.Background(), cli.Operation{
		Action:     "vps.show",
		Arguments:  []string{"missing"},
		Capability: "cloudvps.instances",
		Credentials: resolverFunc(func(context.Context, string) ([]byte, error) {
			return []byte("fixture-token"), nil
		}),
	})
	var cliErr *cli.CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != cli.CodeNetwork {
		t.Fatalf("provider error = %#v", err)
	}
	if strings.Contains(cliErr.Message, "fixture-token") {
		t.Fatalf("provider message leaked: %q", cliErr.Message)
	}
}

func TestExecutorClassifiesContractDriftAmbiguousMutationAndWaitTimeout(t *testing.T) {
	t.Run("malformed success is contract drift", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"unexpected":[]}`)
		}))
		defer server.Close()

		executor := NewExecutor(ExecutorOptions{BaseURL: server.URL}, nil)
		_, err := executor.Execute(context.Background(), operationWithToken("vps.list"))
		var cliErr *cli.CLIError
		if !errors.As(err, &cliErr) || cliErr.Code != cli.CodeProviderContract {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("mutation transport result is outcome unknown", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"code":"TEMPORARY"}`)
		}))
		defer server.Close()

		executor := NewExecutor(ExecutorOptions{BaseURL: server.URL}, nil)
		operation := operationWithToken("vps.destroy")
		operation.Arguments = []string{"42"}
		_, err := executor.Execute(context.Background(), operation)
		var cliErr *cli.CLIError
		if !errors.As(err, &cliErr) || cliErr.Code != cli.CodeOutcomeUnknown {
			t.Fatalf("error = %#v", err)
		}
	})

	t.Run("explicit wait timeout is resumable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{
				"action":{
					"id":"chain_9",
					"status":"in_progress",
					"type":"StopServerUseCase"
				}
			}`)
		}))
		defer server.Close()

		executor := NewExecutor(ExecutorOptions{BaseURL: server.URL}, nil)
		operation := operationWithToken("vps.action.wait")
		operation.Arguments = []string{"chain_9"}
		operation.RequestTimeout = 50 * time.Millisecond
		operation.WaitTimeout = time.Millisecond
		_, err := executor.Execute(context.Background(), operation)
		var cliErr *cli.CLIError
		if !errors.As(err, &cliErr) ||
			cliErr.Code != cli.CodeTimeout ||
			!cliErr.Retryable ||
			cliErr.Details["action_id"] != "chain_9" {
			t.Fatalf("error = %#v", err)
		}
	})
}

func TestLegacyBooleanVariantsStayBooleanInNormalizedOutput(t *testing.T) {
	var server Server
	if err := json.Unmarshal([]byte(`{
		"id":42,
		"backups_enabled":"1",
		"service_id":99,
		"image_id":7
	}`), &server); err != nil {
		t.Fatalf("decode legacy bool: %v", err)
	}
	if !bool(server.BackupsEnabled) {
		t.Fatal("backups_enabled was not normalized")
	}
	encoded, err := json.Marshal(server)
	if err != nil {
		t.Fatalf("encode normalized server: %v", err)
	}
	if !strings.Contains(string(encoded), `"backups_enabled":true`) {
		t.Fatalf("normalized JSON = %s", encoded)
	}
}

func operationWithToken(action string) cli.Operation {
	return cli.Operation{
		Action: action,
		Credentials: resolverFunc(func(context.Context, string) ([]byte, error) {
			return []byte("fixture-token"), nil
		}),
	}
}

type resolverFunc func(context.Context, string) ([]byte, error)

func (function resolverFunc) Resolve(
	ctx context.Context,
	key string,
) ([]byte, error) {
	return function(ctx, key)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func testClient(
	t *testing.T,
	baseURL string,
	sleep func(context.Context, time.Duration) error,
	jitter func(time.Duration) time.Duration,
) *Client {
	t.Helper()
	client, err := New([]byte("fixture-token"), ClientOptions{
		BaseURL: baseURL,
		Sleep:   sleep,
		Jitter:  jitter,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
