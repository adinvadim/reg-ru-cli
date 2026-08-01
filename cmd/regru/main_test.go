package main

import (
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/provider/capability"
)

func TestProductionOptionsEnableCapabilityProbing(t *testing.T) {
	t.Parallel()

	options := productionOptions()

	if _, ok := options.Executor.(*capability.Executor); !ok {
		t.Fatalf("Executor = %T, want *capability.Executor", options.Executor)
	}
}
