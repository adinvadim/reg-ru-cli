package cli

import (
	"context"
	"time"
)

var withTimeout = context.WithTimeout

func runWithTimeout(
	parent context.Context,
	timeout time.Duration,
	run func(context.Context) error,
) error {
	ctx, cancel := withTimeout(parent, timeout)
	defer cancel()
	return run(ctx)
}
