package authcli

import (
	"strings"
	"testing"
)

func TestDecodeREGAPIIPv4(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "public IPv4", body: `{"ip":"198.51.100.24"}`, want: "198.51.100.24"},
		{name: "private IPv4", body: `{"ip":"192.168.1.10"}`, wantErr: true},
		{name: "IPv6", body: `{"ip":"2001:db8::1"}`, wantErr: true},
		{name: "missing address", body: `{}`, wantErr: true},
		{name: "malformed response", body: `{`, wantErr: true},
		{name: "oversized response", body: strings.Repeat("x", maxMyIPResponseSize+1), wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeREGAPIIPv4(strings.NewReader(testCase.body))
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("decodeREGAPIIPv4() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeREGAPIIPv4() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("decodeREGAPIIPv4() = %q, want %q", got, testCase.want)
			}
		})
	}
}
