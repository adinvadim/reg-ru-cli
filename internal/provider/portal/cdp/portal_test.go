package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

func TestRefreshReturnsProviderLoginFromBoundedIdentityProbe(t *testing.T) {
	t.Parallel()

	digest := make([]byte, session.IdentityDigestBytes)
	for index := range digest {
		digest[index] = byte(index)
	}
	page := &observationPageExecutor{result: json.RawMessage(`{
		"state":"authenticated",
		"digest":"` + base64.RawStdEncoding.EncodeToString(digest) + `",
		"login":"portal-login@example.test",
		"userId":"provider-user-id-must-not-escape"
	}`)}
	browser := &browser{page: page}

	observation, err := browser.Refresh(context.Background(), make([]byte, 32))
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if observation.State != session.ObservedAuthenticated ||
		observation.ProviderLogin != "portal-login@example.test" {
		t.Errorf("observation = %+v, want authenticated provider login", observation)
	}
	if len(observation.IdentityDigest) != session.IdentityDigestBytes {
		t.Errorf("identity digest length = %d", len(observation.IdentityDigest))
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("json.Marshal(observation): %v", err)
	}
	if strings.Contains(string(encoded), "provider-user-id-must-not-escape") {
		t.Fatalf("observation exposed provider user ID: %s", encoded)
	}
	if page.program != programAuthProbe {
		t.Errorf("program = %q, want %q", page.program, programAuthProbe)
	}
}

type observationPageExecutor struct {
	program session.ProgramID
	result  json.RawMessage
}

func (e *observationPageExecutor) RunJSON(
	_ context.Context,
	program session.ProgramID,
	_ json.RawMessage,
	result *json.RawMessage,
) error {
	e.program = program
	*result = append((*result)[:0], e.result...)
	return nil
}
