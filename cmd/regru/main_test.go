package main

import (
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/authcli"
)

func TestProductionOptionsEnablePortalAuthentication(t *testing.T) {
	t.Parallel()

	options := productionOptions()

	if _, ok := options.Executor.(*authcli.Executor); !ok {
		t.Fatalf("Executor = %T, want *authcli.Executor", options.Executor)
	}
}
