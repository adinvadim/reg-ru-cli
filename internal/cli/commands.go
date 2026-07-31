package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

type commandSpec struct {
	use         string
	short       string
	capability  string
	action      string
	mutating    bool
	interactive bool
	args        cobra.PositionalArgs
	timeout     func() time.Duration
}

func newOperationCommand(app *appRuntime, spec commandSpec) *cobra.Command {
	command := &cobra.Command{
		Use:   spec.use,
		Short: spec.short,
		Args:  spec.args,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireAccount(); err != nil {
				return err
			}
			if spec.interactive &&
				!app.flags.dryRun &&
				(app.flags.noInput ||
					(!app.inputIsTTY() && !app.flags.credentialsStdin)) {
				return InteractiveRequired(spec.action)
			}

			timeout := app.flags.timeout
			if spec.timeout != nil {
				timeout = spec.timeout()
			}
			if timeout <= 0 {
				return UsageError("operation timeout must be greater than zero")
			}

			operation := Operation{
				Capability: spec.capability,
				Action:     spec.action,
				Account:    app.flags.account,
				Arguments:  append([]string(nil), args...),
			}
			return runWithTimeout(cmd.Context(), timeout, func(ctx context.Context) error {
				return app.executeOperation(ctx, operation, spec.mutating)
			})
		},
	}
	return command
}

func newGroupCommand(use, short string, children ...*cobra.Command) *cobra.Command {
	command := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	command.AddCommand(children...)
	return command
}

func newAuthCommand(app *appRuntime) *cobra.Command {
	loginTimeout := defaultLoginTime
	login := newOperationCommand(app, commandSpec{
		use:         "login",
		short:       "Open a dedicated browser and bootstrap an account profile",
		capability:  "auth.browser_session",
		action:      "auth.login",
		mutating:    true,
		interactive: true,
		args:        cobra.NoArgs,
		timeout:     func() time.Duration { return loginTimeout },
	})
	login.Flags().DurationVar(
		&loginTimeout,
		"login-timeout",
		defaultLoginTime,
		fmt.Sprintf("maximum time for browser login (up to %s)", maxLoginTime),
	)
	login.PreRunE = func(_ *cobra.Command, _ []string) error {
		if loginTimeout < minLoginTime || loginTimeout > maxLoginTime {
			return UsageError(fmt.Sprintf(
				"--login-timeout must be between %s and %s",
				minLoginTime,
				maxLoginTime,
			))
		}
		return nil
	}

	return newGroupCommand(
		"auth",
		"Manage browser-backed REG.RU authentication",
		login,
		newOperationCommand(app, commandSpec{
			use:        "status",
			short:      "Show the selected profile's authentication state",
			capability: "auth.session_status",
			action:     "auth.status",
			args:       cobra.NoArgs,
		}),
		newOperationCommand(app, commandSpec{
			use:        "refresh",
			short:      "Refresh service credentials from the portal session",
			capability: "auth.credential_refresh",
			action:     "auth.refresh",
			mutating:   true,
			args:       cobra.NoArgs,
		}),
		newOperationCommand(app, commandSpec{
			use:        "logout",
			short:      "Remove the selected profile's local portal session",
			capability: "auth.session_logout",
			action:     "auth.logout",
			mutating:   true,
			args:       cobra.NoArgs,
		}),
	)
}

func newVPSCommand(app *appRuntime) *cobra.Command {
	return newGroupCommand(
		"vps",
		"Manage REG.RU CloudVPS resources",
		newOperationCommand(app, readSpec("list", "List CloudVPS instances", "cloudvps.instances", "vps.list", cobra.NoArgs)),
		newOperationCommand(app, readSpec("get <id>", "Show a CloudVPS instance", "cloudvps.instances", "vps.get", cobra.ExactArgs(1))),
		newOperationCommand(app, readSpec("ips <id>", "List addresses assigned to a CloudVPS instance", "cloudvps.addresses", "vps.ips", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("create", "Create a CloudVPS instance", "cloudvps.instances", "vps.create", cobra.NoArgs)),
		newOperationCommand(app, mutationSpec("start <id>", "Start a CloudVPS instance", "cloudvps.lifecycle", "vps.start", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("stop <id>", "Stop a CloudVPS instance", "cloudvps.lifecycle", "vps.stop", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("reboot <id>", "Reboot a CloudVPS instance", "cloudvps.lifecycle", "vps.reboot", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("delete <id>", "Delete a CloudVPS instance", "cloudvps.instances", "vps.delete", cobra.ExactArgs(1))),
	)
}

func newS3Command(app *appRuntime) *cobra.Command {
	buckets := newGroupCommand(
		"bucket",
		"Manage S3 bucket lifecycle and provider configuration",
		newOperationCommand(app, readSpec("list", "List S3 buckets", "s3.bucket", "s3.bucket.list", cobra.NoArgs)),
		newOperationCommand(app, readSpec("get <name>", "Show an S3 bucket", "s3.bucket", "s3.bucket.get", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("create <name>", "Create an S3 bucket", "s3.control_plane", "s3.bucket.create", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("configure <name>", "Configure an S3 bucket", "s3.control_plane", "s3.bucket.configure", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("delete <name>", "Delete an S3 bucket", "s3.bucket", "s3.bucket.delete", cobra.ExactArgs(1))),
	)
	credentials := newGroupCommand(
		"credentials",
		"Manage S3 access-key lifecycle",
		newOperationCommand(app, readSpec("list", "List S3 credential references", "s3.credentials", "s3.credentials.list", cobra.NoArgs)),
		newOperationCommand(app, mutationSpec("create", "Create S3 credentials", "s3.credentials", "s3.credentials.create", cobra.NoArgs)),
		newOperationCommand(app, mutationSpec("revoke <id>", "Revoke S3 credentials", "s3.credentials", "s3.credentials.revoke", cobra.ExactArgs(1))),
	)
	return newGroupCommand("s3", "Manage REG.RU S3 buckets and configuration", buckets, credentials)
}

func newBillingCommand(app *appRuntime) *cobra.Command {
	return newGroupCommand(
		"billing",
		"Read REG.RU balances and invoices and hand off checkout",
		newOperationCommand(app, readSpec("balance", "Show account balances", "billing.read", "billing.balance", cobra.NoArgs)),
		newOperationCommand(app, readSpec("invoices", "List invoices", "billing.read", "billing.invoices", cobra.NoArgs)),
		newOperationCommand(app, readSpec("invoice <id>", "Show an invoice", "billing.read", "billing.invoice", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("checkout", "Open a browser checkout handoff", "billing.checkout", "billing.checkout", cobra.NoArgs)),
	)
}

func newSupportCommand(app *appRuntime) *cobra.Command {
	tickets := newGroupCommand(
		"ticket",
		"Manage experimental private support-ticket operations",
		newOperationCommand(app, readSpec("list", "List support tickets", "support.private", "support.ticket.list", cobra.NoArgs)),
		newOperationCommand(app, readSpec("get <id>", "Show a support ticket", "support.private", "support.ticket.get", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("create", "Create a support ticket", "support.private", "support.ticket.create", cobra.NoArgs)),
		newOperationCommand(app, mutationSpec("reply <id>", "Reply to a support ticket", "support.private", "support.ticket.reply", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("attach <id> <path>", "Attach a file to a support ticket", "support.private", "support.ticket.attach", cobra.ExactArgs(2))),
		newOperationCommand(app, mutationSpec("close <id>", "Close a support ticket", "support.private", "support.ticket.close", cobra.ExactArgs(1))),
		newOperationCommand(app, mutationSpec("reopen <id>", "Reopen a support ticket", "support.private", "support.ticket.reopen", cobra.ExactArgs(1))),
	)
	return newGroupCommand("support", "Use the experimental private support adapter", tickets)
}

func readSpec(
	use, short, capability, action string,
	args cobra.PositionalArgs,
) commandSpec {
	return commandSpec{
		use:        use,
		short:      short,
		capability: capability,
		action:     action,
		args:       args,
	}
}

func mutationSpec(
	use, short, capability, action string,
	args cobra.PositionalArgs,
) commandSpec {
	spec := readSpec(use, short, capability, action, args)
	spec.mutating = true
	return spec
}
