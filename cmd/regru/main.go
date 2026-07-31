package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		// Restore the platform's default behavior so a second Ctrl-C exits
		// immediately while bounded cleanup from the first is still running.
		stop()
	}()

	exitCode := cli.Execute(ctx, os.Args[1:], cli.DefaultOptions())
	stop()
	os.Exit(exitCode)
}
