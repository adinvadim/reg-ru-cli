package s3

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

func TestPortalControlPlanePreservesStringValuedInventorySizes(t *testing.T) {
	control := NewPortalControlPlane(&fakeS3SessionBroker{
		result: readS3Fixture(t, "testdata/portal-s3-inventory-string-sizes.json"),
	})

	store, err := control.Inventory(context.Background(), s3PortalAccount())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if got := fmt.Sprint(store.Size); got != "000123.450" {
		t.Errorf("service size = %q", got)
	}
	if len(store.Buckets) != 1 || fmt.Sprint(store.Buckets[0].Size) != "001.20" {
		t.Fatalf("buckets = %#v", store.Buckets)
	}

	service := renderService(store)
	if service.Human != "S3 service: ACTIVE, 1/10 buckets, size 000123.450 bytes, quota 50 GB" {
		t.Errorf("service human output = %q", service.Human)
	}
	if len(service.Plain) != 1 || service.Plain[0] != "ACTIVE\tfalse\t50\t1\t10\t000123.450\tbytes" {
		t.Errorf("service plain output = %q", service.Plain)
	}
	encoded, err := json.Marshal(service.Data)
	if err != nil {
		t.Fatalf("Marshal(service): %v", err)
	}
	if !strings.Contains(string(encoded), `"size":"000123.450"`) {
		t.Errorf("service JSON output = %s", encoded)
	}

	buckets := renderBuckets(store.Buckets)
	if buckets.Human != "1 S3 buckets\nbucket-redacted: private_by_keys, 3 objects, size 001.20 GB" {
		t.Errorf("bucket-list human output = %q", buckets.Human)
	}
	if len(buckets.Plain) != 1 || buckets.Plain[0] != "bucket-redacted\tprivate_by_keys\t10\t3\t001.20\tGB" {
		t.Errorf("bucket-list plain output = %q", buckets.Plain)
	}

	encoded, err = json.Marshal(buckets.Data)
	if err != nil {
		t.Fatalf("Marshal(bucket list): %v", err)
	}
	if !strings.Contains(string(encoded), `"size":"001.20"`) {
		t.Errorf("bucket-list JSON output = %s", encoded)
	}

	bucket := renderBucket(store.Buckets[0])
	if bucket.Human != "S3 bucket bucket-redacted: private_by_keys, 3 objects, size 001.20 GB" {
		t.Errorf("bucket human output = %q", bucket.Human)
	}
	if len(bucket.Plain) != 1 || bucket.Plain[0] != "bucket-redacted\tprivate_by_keys\t3\t001.20\tGB" {
		t.Errorf("bucket plain output = %q", bucket.Plain)
	}
}

func TestPortalControlPlaneFailsClosedOnNonStringInventorySize(t *testing.T) {
	for _, fixture := range []string{
		"testdata/portal-s3-inventory-number-service-size.json",
		"testdata/portal-s3-inventory-number-bucket-size.json",
	} {
		t.Run(fixture, func(t *testing.T) {
			control := NewPortalControlPlane(&fakeS3SessionBroker{result: readS3Fixture(t, fixture)})
			_, err := control.Inventory(context.Background(), s3PortalAccount())
			if !isPortalKind(err, PortalContract) {
				t.Fatalf("Inventory() error = %v, want contract error", err)
			}
		})
	}
}

func readS3Fixture(t *testing.T, path string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return data
}

func s3PortalAccount() profile.Account {
	return profile.Account{
		ID:     "p_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		Cloud:  profile.Cloud{EnvironmentID: "service-redacted"},
		Portal: profile.Portal{SessionRef: "s_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
}

type fakeS3SessionBroker struct {
	result json.RawMessage
}

func (b *fakeS3SessionBroker) WithSession(
	_ context.Context,
	_ session.Profile,
	use func(session.PageExecutor) error,
) error {
	return use((*fakeS3Page)(b))
}

type fakeS3Page fakeS3SessionBroker

func (p *fakeS3Page) RunJSON(
	_ context.Context,
	_ session.ProgramID,
	_ json.RawMessage,
	result *json.RawMessage,
) error {
	*result = append((*result)[:0], p.result...)
	return nil
}
