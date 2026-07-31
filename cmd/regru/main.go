package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/provider/billing"
	"github.com/adinvadim/reg-ru-cli/internal/provider/cloudvps"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/authcli"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/cdp"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
	regs3 "github.com/adinvadim/reg-ru-cli/internal/provider/s3"
)

const portalLoginURL = "https://www.reg.ru/user/account/"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		<-ctx.Done()
		// Restore the platform's default behavior so a second Ctrl-C exits
		// immediately while bounded cleanup from the first is still running.
		stop()
	}()

	exitCode := cli.Execute(ctx, os.Args[1:], productionOptions())
	stop()
	os.Exit(exitCode)
}

func productionOptions() cli.Options {
	options := cli.DefaultOptions()
	configDir, err := os.UserConfigDir()
	if err != nil {
		return options
	}
	stateRoot := filepath.Join(configDir, "regru", "portal-sessions")
	store := session.NewFileStore(stateRoot)
	broker := session.NewBroker(
		store,
		cdp.NewFactory(cdp.Config{}),
		session.Options{LoginURL: portalLoginURL},
	)
	options.Executor = cloudvps.NewExecutor(cloudvps.ExecutorOptions{}, options.Executor)
	options.Executor = billing.NewExecutor(billing.ExecutorOptions{}, options.Executor)
	options.Executor = regs3.NewExecutor(options.Profiles, regs3.ExecutorOptions{
		ControlPlane: regs3.NewPortalControlPlane(broker),
	}, options.Executor)
	options.Executor = authcli.New(options.Profiles, broker, options.Executor)
	return options
}
