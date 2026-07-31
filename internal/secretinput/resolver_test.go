package secretinput

import (
	"strings"
	"testing"
)

func TestLoadResolvesSelectedAccountWithoutExposingValues(t *testing.T) {
	t.Parallel()

	const token = "super-sensitive-token"
	resolver, err := Load(strings.NewReader(`{
		"schemaVersion": "regru.secret-input/v1",
		"fields": {
			"cloudvps.token": "` + token + `",
			"regapi.username": "agent@example.test",
			"regapi.password": "synthetic-password"
		}
	}`))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	t.Cleanup(resolver.Close)

	got, ok := resolver.Resolve("cloudvps.token")
	if !ok {
		t.Fatal("cloudvps.token was not resolved")
	}
	if string(got) != token {
		t.Errorf("resolved token = %q", got)
	}
	got[0] = 'X'
	again, _ := resolver.Resolve("cloudvps.token")
	if string(again) != token {
		t.Error("Resolve returned mutable internal storage")
	}

	if !resolver.Contains(token) {
		t.Error("Contains did not detect a complete credential value")
	}
	if rendered := resolver.Redact("provider rejected " + token); strings.Contains(rendered, token) {
		t.Errorf("Redact leaked credential: %q", rendered)
	}
}

func TestLoadRejectsDuplicateAndPartialCredentialFamilies(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"schemaVersion":"regru.secret-input/v1","schemaVersion":"regru.secret-input/v1","fields":{"cloudvps.token":"value"}}`,
		`{"schemaVersion":"regru.secret-input/v1","fields":{"regapi.password":"value"}}`,
		`{"schemaVersion":"regru.secret-input/v1","fields":{"unknown.password":"value"}}`,
	}
	for _, input := range tests {
		if _, err := Load(strings.NewReader(input)); err == nil {
			t.Error("Load accepted invalid credential input")
		}
	}
}
