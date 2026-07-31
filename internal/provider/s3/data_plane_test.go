package s3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAWSDataPlaneUsesPathStyleCustomEndpointAndRedactedSigV4(t *testing.T) {
	const (
		access = "synthetic-access"
		secret = "synthetic-secret-never-log"
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/valid-bucket" || request.URL.Query().Has("policy") == false {
			t.Errorf("request target = %s", request.URL.String())
		}
		authorization := request.Header.Get("Authorization")
		if !strings.Contains(authorization, "Credential="+access+"/") ||
			!strings.Contains(authorization, "/us-east-1/s3/aws4_request") {
			t.Errorf("unexpected authorization shape")
		}
		if strings.Contains(authorization, secret) || strings.Contains(request.URL.String(), secret) {
			t.Fatal("secret appeared in the request target or authorization header")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"Version":"2012-10-17","Statement":[]}`))
	}))
	defer server.Close()

	client, err := NewAWSDataPlane(DataPlaneOptions{
		Endpoint: server.URL, SigningRegion: "us-east-1",
		AccessKey: []byte(access), SecretKey: []byte(secret),
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewAWSDataPlane() error = %v", err)
	}
	policy, err := client.GetPolicy(context.Background(), "valid-bucket")
	if err != nil {
		t.Fatalf("GetPolicy() error = %v", err)
	}
	if !strings.Contains(string(policy), `"Version"`) {
		t.Errorf("policy = %s", policy)
	}
	provider := client.provider
	ownedAccess := provider.access
	ownedSecret := provider.secret
	client.Close()
	for _, value := range append(ownedAccess, ownedSecret...) {
		if value != 0 {
			t.Fatal("owned credential buffers were not wiped")
		}
	}
}
