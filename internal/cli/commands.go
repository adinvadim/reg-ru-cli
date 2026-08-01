package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type commandSpec struct {
	use         string
	aliases     []string
	short       string
	capability  string
	action      string
	mutating    bool
	interactive bool
	args        cobra.PositionalArgs
	timeout     func() time.Duration
	parameters  func() map[string][]string
	input       func() InputResolver
	noWait      func() bool
}

func newOperationCommand(app *appRuntime, spec commandSpec) *cobra.Command {
	command := &cobra.Command{
		Use:     spec.use,
		Aliases: spec.aliases,
		Short:   spec.short,
		Args:    spec.args,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.requireAccount(); err != nil {
				return err
			}
			if spec.interactive &&
				!app.flags.dryRun &&
				(app.flags.noInput || !app.inputIsTTY()) {
				return InteractiveRequired(spec.action)
			}

			timeout := app.flags.timeout
			if strings.HasPrefix(spec.capability, "cloudvps.") &&
				app.flags.waitTimeout > timeout {
				timeout = app.flags.waitTimeout
			}
			if spec.timeout != nil {
				timeout = spec.timeout()
			}
			if timeout <= 0 {
				return UsageError("operation timeout must be greater than zero")
			}

			operation := Operation{
				Capability:     spec.capability,
				Action:         spec.action,
				Account:        app.flags.account,
				Arguments:      append([]string(nil), args...),
				RequestTimeout: app.flags.timeout,
				WaitTimeout:    app.flags.waitTimeout,
			}
			if spec.parameters != nil {
				operation.Parameters = spec.parameters()
			}
			if spec.input != nil {
				operation.Input = spec.input()
			}
			if spec.noWait != nil {
				operation.NoWait = spec.noWait()
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
			short:      "Refresh the selected profile's portal session",
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
	var noWait bool
	command := newGroupCommand(
		"vps",
		"Manage REG.RU CloudVPS resources",
		newOperationCommand(app, readSpec(
			"list",
			"List CloudVPS instances",
			"cloudvps.instances",
			"vps.list",
			cobra.NoArgs,
		)),
		newOperationCommand(app, commandSpec{
			use:        "get <id>",
			aliases:    []string{"show"},
			short:      "Show a CloudVPS instance",
			capability: "cloudvps.instances",
			action:     "vps.show",
			args:       positiveDecimalArgs(1),
		}),
		newCreateVPSCommand(app, &noWait),
		newOperationCommand(app, commandSpec{
			use:        "delete <id>",
			aliases:    []string{"destroy"},
			short:      "Destroy a CloudVPS instance",
			capability: "cloudvps.instances",
			action:     "vps.destroy",
			mutating:   true,
			args:       positiveDecimalArgs(1),
			noWait:     func() bool { return noWait },
		}),
		newRenameVPSCommand(app, &noWait),
		newSimpleVPSAction(app, "start <id>", "Start a CloudVPS instance", "vps.start", "start", &noWait),
		newSimpleVPSAction(app, "stop <id>", "Stop a CloudVPS instance", "vps.stop", "stop", &noWait),
		newSimpleVPSAction(app, "reboot <id>", "Reboot a CloudVPS instance", "vps.reboot", "reboot", &noWait),
		newRebuildVPSCommand(app, &noWait),
		newResizeVPSCommand(app, &noWait),
		newSimpleVPSAction(app, "password-reset <id>", "Reset a CloudVPS password", "vps.password-reset", "password_reset", &noWait),
		newCloneVPSCommand(app, &noWait),
		newVPSActionCommand(app),
		newOperationCommand(app, readSpec(
			"ips <id>",
			"List addresses assigned to a CloudVPS instance",
			"cloudvps.addresses",
			"vps.ips",
			positiveDecimalArgs(1),
		)),
		newVPSIPCommand(app, &noWait),
		newVPSSSHKeyCommand(app),
		newVPSSnapshotCommand(app, &noWait),
		newVPSBackupCommand(app, &noWait),
		newVPSPlanCommand(app),
		newVPSImageCommand(app),
	)
	command.PersistentFlags().BoolVar(
		&noWait,
		"no-wait",
		false,
		"return after a mutation is accepted instead of polling its action",
	)
	command.PersistentFlags().DurationVar(
		&app.flags.waitTimeout,
		"wait-timeout",
		defaultWaitTime,
		fmt.Sprintf("maximum end-to-end action wait (between %s and %s)", minWaitTime, maxWaitTime),
	)
	return command
}

func newCreateVPSCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var (
		name       string
		size       string
		image      string
		region     string
		sshKeys    []string
		backups    bool
		floatingIP bool
	)
	command := newOperationCommand(app, commandSpec{
		use:        "create",
		aliases:    []string{"deploy"},
		short:      "Deploy a CloudVPS instance",
		capability: "cloudvps.instances",
		action:     "vps.create",
		mutating:   true,
		args:       cobra.NoArgs,
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters(
				"name", name,
				"size", size,
				"image", image,
				"region", region,
				"ssh-key", sshKeys,
				"backups", backups,
				"floating-ip", floatingIP,
			)
		},
	})
	command.Flags().StringVar(&name, "name", "", "server name")
	command.Flags().StringVar(&size, "size", "", "v2 plan slug")
	command.Flags().StringVar(&image, "image", "", "v2 image slug or snapshot ID")
	command.Flags().StringVar(&region, "region", "", "deployment region slug")
	command.Flags().StringSliceVar(&sshKeys, "ssh-key", nil, "SSH key ID or fingerprint (repeatable)")
	command.Flags().BoolVar(&backups, "backups", false, "enable backups at creation")
	command.Flags().BoolVar(&floatingIP, "floating-ip", true, "allocate the primary floating IP")
	_ = command.MarkFlagRequired("size")
	_ = command.MarkFlagRequired("image")
	return command
}

func newRenameVPSCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var name string
	command := newOperationCommand(app, commandSpec{
		use:        "rename <id>",
		short:      "Rename a CloudVPS instance",
		capability: "cloudvps.instances",
		action:     "vps.rename",
		mutating:   true,
		args:       positiveDecimalArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters("name", name)
		},
	})
	command.Flags().StringVar(&name, "name", "", "new server name")
	_ = command.MarkFlagRequired("name")
	return command
}

func newSimpleVPSAction(
	app *appRuntime,
	use string,
	short string,
	action string,
	providerType string,
	noWait *bool,
) *cobra.Command {
	return newOperationCommand(app, commandSpec{
		use:        use,
		short:      short,
		capability: "cloudvps.lifecycle",
		action:     action,
		mutating:   true,
		args:       positiveDecimalArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters("type", providerType)
		},
	})
}

func newRebuildVPSCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var image string
	command := newOperationCommand(app, commandSpec{
		use:        "rebuild <id>",
		short:      "Rebuild a CloudVPS instance from an image",
		capability: "cloudvps.lifecycle",
		action:     "vps.rebuild",
		mutating:   true,
		args:       positiveDecimalArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters(
				"type", "rebuild",
				"image", image,
			)
		},
	})
	command.Flags().StringVar(&image, "image", "", "image slug or snapshot ID")
	_ = command.MarkFlagRequired("image")
	return command
}

func newResizeVPSCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var size string
	command := newOperationCommand(app, commandSpec{
		use:        "resize <id>",
		short:      "Resize a CloudVPS instance",
		capability: "cloudvps.lifecycle",
		action:     "vps.resize",
		mutating:   true,
		args:       positiveDecimalArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters("type", "resize", "size", size)
		},
	})
	command.Flags().StringVar(&size, "size", "", "target v2 plan slug")
	_ = command.MarkFlagRequired("size")
	return command
}

func newCloneVPSCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var name string
	var offline bool
	command := newOperationCommand(app, commandSpec{
		use:        "clone <id>",
		short:      "Clone a CloudVPS instance",
		capability: "cloudvps.lifecycle",
		action:     "vps.clone",
		mutating:   true,
		args:       positiveDecimalArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters(
				"type", "clone",
				"name", name,
				"offline", offline,
			)
		},
	})
	command.Flags().StringVar(&name, "name", "", "name for the cloned server")
	command.Flags().BoolVar(&offline, "offline", false, "stop the source while cloning")
	return command
}

func newVPSActionCommand(app *appRuntime) *cobra.Command {
	return newGroupCommand(
		"action",
		"Inspect and wait for CloudVPS asynchronous actions",
		newOperationCommand(app, readSpec(
			"show <id>",
			"Show a CloudVPS action",
			"cloudvps.actions",
			"vps.action.show",
			actionIDArgs,
		)),
		newOperationCommand(app, readSpec(
			"wait <id>",
			"Wait for a CloudVPS action to finish",
			"cloudvps.actions",
			"vps.action.wait",
			actionIDArgs,
		)),
	)
}

func newVPSIPCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var server string
	list := newOperationCommand(app, commandSpec{
		use:        "list",
		short:      "List additional CloudVPS addresses",
		capability: "cloudvps.addresses",
		action:     "vps.ip.list",
		args:       cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters("server", server)
		},
	})
	list.Flags().StringVar(&server, "server", "", "filter by CloudVPS server ID")
	list.PreRunE = func(_ *cobra.Command, _ []string) error {
		if server == "" {
			return nil
		}
		number, err := strconv.ParseUint(server, 10, 64)
		if err != nil || number == 0 {
			return UsageError("--server must be a positive decimal value")
		}
		return nil
	}

	var ipv4 int
	var ipv6 int
	add := newOperationCommand(app, commandSpec{
		use:        "add <server-id>",
		short:      "Allocate additional addresses",
		capability: "cloudvps.addresses",
		action:     "vps.ip.add",
		mutating:   true,
		args:       positiveDecimalArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters("ipv4", ipv4, "ipv6", ipv6)
		},
	})
	add.Flags().IntVar(&ipv4, "ipv4-count", 0, "number of IPv4 addresses (0-4)")
	add.Flags().IntVar(&ipv6, "ipv6-count", 0, "number of IPv6 addresses (0-4)")
	add.PreRunE = func(_ *cobra.Command, _ []string) error {
		if ipv4 < 0 || ipv4 > 4 || ipv6 < 0 || ipv6 > 4 || ipv4+ipv6 == 0 {
			return UsageError("request 1-4 IPv4 and/or 1-4 IPv6 addresses")
		}
		return nil
	}

	return newGroupCommand(
		"ip",
		"Manage CloudVPS addresses and PTR records",
		list,
		newOperationCommand(app, readSpec(
			"show <address>",
			"Show an additional address",
			"cloudvps.addresses",
			"vps.ip.show",
			cobra.ExactArgs(1),
		)),
		add,
		newOperationCommand(app, commandSpec{
			use:        "remove <address>",
			short:      "Release an additional address",
			capability: "cloudvps.addresses",
			action:     "vps.ip.remove",
			mutating:   true,
			args:       cobra.ExactArgs(1),
			noWait:     func() bool { return *noWait },
		}),
		newPTRCommand(app, noWait),
	)
}

func newPTRCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var ptr string
	command := newOperationCommand(app, commandSpec{
		use:        "ptr <address>",
		short:      "Update an address PTR record",
		capability: "cloudvps.addresses",
		action:     "vps.ip.ptr",
		mutating:   true,
		args:       cobra.ExactArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters("ptr", ptr)
		},
	})
	command.Flags().StringVar(&ptr, "ptr", "", "PTR hostname")
	_ = command.MarkFlagRequired("ptr")
	command.PreRunE = func(_ *cobra.Command, _ []string) error {
		value := strings.TrimSuffix(ptr, ".")
		if len(ptr) < 6 || len(ptr) > 63 ||
			!strings.Contains(value, ".") ||
			strings.ContainsAny(value, " \t\r\n/") {
			return UsageError("--ptr must be a 6-63 character hostname")
		}
		for _, label := range strings.Split(value, ".") {
			if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return UsageError("--ptr must be a valid hostname")
			}
		}
		return nil
	}
	return command
}

func newVPSSSHKeyCommand(app *appRuntime) *cobra.Command {
	var (
		name          string
		publicKeyFile string
		publicKey     string
	)
	add := newOperationCommand(app, commandSpec{
		use:        "add",
		short:      "Add a CloudVPS SSH public key",
		capability: "cloudvps.ssh_keys",
		action:     "vps.ssh-key.add",
		mutating:   true,
		args:       cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters("name", name, "public-key", publicKey)
		},
	})
	add.Flags().StringVar(&name, "name", "", "SSH key name")
	add.Flags().StringVar(&publicKeyFile, "public-key-file", "", "path to an OpenSSH public key file")
	_ = add.MarkFlagRequired("name")
	_ = add.MarkFlagRequired("public-key-file")
	add.PreRunE = func(_ *cobra.Command, _ []string) error {
		value, err := readPublicKey(publicKeyFile)
		if err != nil {
			return UsageError(err.Error())
		}
		publicKey = value
		return nil
	}

	var renameName string
	rename := newOperationCommand(app, commandSpec{
		use:        "rename <id-or-fingerprint>",
		short:      "Rename a CloudVPS SSH key",
		capability: "cloudvps.ssh_keys",
		action:     "vps.ssh-key.rename",
		mutating:   true,
		args:       cobra.ExactArgs(1),
		parameters: func() map[string][]string {
			return operationParameters("name", renameName)
		},
	})
	rename.Flags().StringVar(&renameName, "name", "", "new SSH key name")
	_ = rename.MarkFlagRequired("name")

	return newGroupCommand(
		"ssh-key",
		"Manage CloudVPS SSH keys",
		newOperationCommand(app, readSpec(
			"list",
			"List CloudVPS SSH keys",
			"cloudvps.ssh_keys",
			"vps.ssh-key.list",
			cobra.NoArgs,
		)),
		add,
		rename,
		newOperationCommand(app, mutationSpec(
			"remove <id>",
			"Remove a CloudVPS SSH key",
			"cloudvps.ssh_keys",
			"vps.ssh-key.remove",
			cobra.ExactArgs(1),
		)),
	)
}

func readPublicKey(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read SSH public key: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect SSH public key: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("SSH public key path must be a regular file")
	}
	const limit = 16 << 10
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", fmt.Errorf("read SSH public key: %w", err)
	}
	if len(data) == 0 || len(data) > limit {
		return "", fmt.Errorf("SSH public key must be between 1 and %d bytes", limit)
	}
	value := strings.TrimSpace(string(data))
	fields := strings.Fields(value)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "ssh-") &&
		!strings.HasPrefix(fields[0], "ecdsa-sha2-") {
		return "", fmt.Errorf("file does not contain a supported OpenSSH public key")
	}
	return value, nil
}

func newVPSSnapshotCommand(app *appRuntime, noWait *bool) *cobra.Command {
	var region string
	list := newOperationCommand(app, commandSpec{
		use:        "list",
		short:      "List CloudVPS snapshots",
		capability: "cloudvps.snapshots",
		action:     "vps.snapshot.list",
		args:       cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters("region", region)
		},
	})
	list.Flags().StringVar(&region, "region", "", "filter by region slug")

	var (
		offline      bool
		snapshotName string
	)
	create := newOperationCommand(app, commandSpec{
		use:        "create <server-id>",
		short:      "Create a CloudVPS snapshot",
		capability: "cloudvps.snapshots",
		action:     "vps.snapshot.create",
		mutating:   true,
		args:       positiveDecimalArgs(1),
		noWait:     func() bool { return *noWait },
		parameters: func() map[string][]string {
			return operationParameters("type", "snapshot", "name", snapshotName, "offline", offline)
		},
	})
	create.Flags().StringVar(&snapshotName, "name", "", "snapshot name")
	create.Flags().BoolVar(&offline, "offline", false, "stop the server for a consistent snapshot")
	_ = create.MarkFlagRequired("name")

	return newGroupCommand(
		"snapshot",
		"Manage CloudVPS snapshots",
		list,
		create,
		newOperationCommand(app, mutationSpec(
			"remove <id>",
			"Remove a CloudVPS snapshot",
			"cloudvps.snapshots",
			"vps.snapshot.remove",
			positiveDecimalArgs(1),
		)),
	)
}

func newVPSBackupCommand(app *appRuntime, noWait *bool) *cobra.Command {
	return newGroupCommand(
		"backup",
		"Manage CloudVPS backups",
		newSimpleVPSAction(app, "enable <id>", "Enable CloudVPS backups", "vps.backup.enable", "enable_backups", noWait),
		newSimpleVPSAction(app, "disable <id>", "Disable CloudVPS backups", "vps.backup.disable", "disable_backups", noWait),
		newOperationCommand(app, commandSpec{
			use:        "restore <server-id> <backup-image-id>",
			short:      "Restore a selected CloudVPS backup image",
			capability: "cloudvps.lifecycle",
			action:     "vps.backup.restore",
			mutating:   true,
			args:       positiveDecimalArgs(2),
			noWait:     func() bool { return *noWait },
			parameters: func() map[string][]string {
				return operationParameters("type", "restore")
			},
		}),
	)
}

func newVPSPlanCommand(app *appRuntime) *cobra.Command {
	var region string
	var planLine string
	var unit string
	var memory int
	var cpus int
	var disk int
	parameters := func() map[string][]string {
		return operationParameters(
			"region", region,
			"plan-line", planLine,
			"unit", unit,
			"memory", memory,
			"cpus", cpus,
			"disk", disk,
		)
	}
	list := newOperationCommand(app, commandSpec{
		use:        "list",
		short:      "List current CloudVPS v2 plans",
		capability: "cloudvps.catalog",
		action:     "vps.plan.list",
		args:       cobra.NoArgs,
		parameters: parameters,
	})
	show := newOperationCommand(app, commandSpec{
		use:        "show <slug>",
		short:      "Show a current CloudVPS v2 plan",
		capability: "cloudvps.catalog",
		action:     "vps.plan.show",
		args:       cobra.ExactArgs(1),
		parameters: parameters,
	})
	for _, command := range []*cobra.Command{list, show} {
		command.Flags().StringVar(&region, "region", "", "required region slug")
		command.Flags().StringVar(&planLine, "plan-line", "", "filter by plan line")
		command.Flags().StringVar(&unit, "unit", "", "filter by billing unit: hour or month")
		command.Flags().IntVar(&memory, "memory", 0, "filter by the provider's raw memory value")
		command.Flags().IntVar(&cpus, "cpus", 0, "filter by vCPU count")
		command.Flags().IntVar(&disk, "disk", 0, "filter by disk size in GiB")
		_ = command.MarkFlagRequired("region")
	}
	return newGroupCommand("plan", "Discover CloudVPS v2 plans", list, show)
}

func newVPSImageCommand(app *appRuntime) *cobra.Command {
	var region string
	var imageType string
	var private bool
	parameters := func() map[string][]string {
		return operationParameters(
			"region", region,
			"type", imageType,
			"private", private,
		)
	}
	list := newOperationCommand(app, commandSpec{
		use:        "list",
		short:      "List current CloudVPS v2 images",
		capability: "cloudvps.catalog",
		action:     "vps.image.list",
		args:       cobra.NoArgs,
		parameters: parameters,
	})
	show := newOperationCommand(app, commandSpec{
		use:        "show <slug>",
		short:      "Show a current CloudVPS v2 image",
		capability: "cloudvps.catalog",
		action:     "vps.image.show",
		args:       cobra.ExactArgs(1),
		parameters: parameters,
	})
	for _, command := range []*cobra.Command{list, show} {
		command.Flags().StringVar(&region, "region", "", "required region slug")
		command.Flags().StringVar(&imageType, "type", "", "filter by image type")
		command.Flags().BoolVar(&private, "private", false, "show only private images")
		_ = command.MarkFlagRequired("region")
	}
	return newGroupCommand("image", "Discover CloudVPS v2 images", list, show)
}

func positiveDecimalArgs(count int) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(count)(command, args); err != nil {
			return err
		}
		for _, value := range args {
			number, err := strconv.ParseUint(value, 10, 64)
			if err != nil || number == 0 {
				return UsageError("resource identifiers must be positive decimal values")
			}
		}
		return nil
	}
}

func actionIDArgs(command *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(command, args); err != nil {
		return err
	}
	value := args[0]
	if strings.HasPrefix(value, "chain_") {
		value = strings.TrimPrefix(value, "chain_")
	}
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil || number == 0 {
		return UsageError("action ID must be a positive decimal value or chain_<digits>")
	}
	return nil
}

func operationParameters(values ...any) map[string][]string {
	parameters := make(map[string][]string)
	for index := 0; index+1 < len(values); index += 2 {
		name, ok := values[index].(string)
		if !ok || name == "" {
			continue
		}
		switch value := values[index+1].(type) {
		case string:
			if value != "" {
				parameters[name] = []string{value}
			}
		case []string:
			if len(value) > 0 {
				parameters[name] = append([]string(nil), value...)
			}
		case bool:
			parameters[name] = []string{fmt.Sprintf("%t", value)}
		case int:
			if value != 0 {
				parameters[name] = []string{fmt.Sprintf("%d", value)}
			}
		}
	}
	return parameters
}

func newBillingCommand(app *appRuntime) *cobra.Command {
	var (
		balanceSource   string
		balanceCurrency string
	)
	balance := newOperationCommand(app, commandSpec{
		use:        "balance",
		short:      "Show source-discriminated REG.RU and CloudVPS balances",
		capability: "billing.read",
		action:     "billing.balance",
		args:       cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters("source", balanceSource, "currency", balanceCurrency)
		},
	})
	balance.Flags().StringVar(&balanceSource, "source", "all", "balance source: all, regapi, or cloudvps")
	balance.Flags().StringVar(&balanceCurrency, "currency", "RUR", "REG.API currency: RUR, USD, EUR, or UAH")
	balance.PreRunE = func(_ *cobra.Command, _ []string) error {
		if !oneOf(balanceSource, "all", "regapi", "cloudvps") {
			return UsageError("--source must be all, regapi, or cloudvps")
		}
		if !validREGAPICurrency(balanceCurrency) {
			return UsageError("--currency must be RUR, USD, EUR, or UAH")
		}
		return nil
	}

	var (
		historySource  string
		historyStart   string
		historyEnd     string
		historyPayType string
		historyLimit   int
		historyOffset  int
		historyAll     bool
	)
	history := newOperationCommand(app, commandSpec{
		use:        "history",
		short:      "Show CloudVPS refills or partner REG.API invoice history",
		capability: "billing.read",
		action:     "billing.history",
		args:       cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters(
				"source", historySource,
				"start-date", historyStart,
				"end-date", historyEnd,
				"pay-type", historyPayType,
				"limit", historyLimit,
				"offset", historyOffset,
				"all", historyAll,
			)
		},
	})
	history.Flags().StringVar(&historySource, "source", "cloudvps", "history source: cloudvps or regapi")
	history.Flags().StringVar(&historyStart, "start-date", "", "partner REG.API start date (YYYY-MM-DD)")
	history.Flags().StringVar(&historyEnd, "end-date", "", "partner REG.API end date (YYYY-MM-DD)")
	history.Flags().StringVar(&historyPayType, "pay-type", "", "raw REG.API payment-type filter")
	history.Flags().IntVar(&historyLimit, "limit", 100, "maximum records (1-1024)")
	history.Flags().IntVar(&historyOffset, "offset", 0, "zero-based REG.API offset")
	history.Flags().BoolVar(&historyAll, "include-inactive", false, "include inactive partner invoices")
	history.PreRunE = func(_ *cobra.Command, _ []string) error {
		if !oneOf(historySource, "cloudvps", "regapi") {
			return UsageError("--source must be cloudvps or regapi")
		}
		if historyLimit < 1 || historyLimit > 1024 || historyOffset < 0 {
			return UsageError("--limit must be 1-1024 and --offset must be non-negative")
		}
		if historySource == "regapi" && (!dateOnly(historyStart) || !dateOnly(historyEnd)) {
			return UsageError("REG.API history requires --start-date and --end-date in YYYY-MM-DD format")
		}
		return nil
	}

	invoice := newBillingInvoiceCommand(app)
	return newGroupCommand(
		"billing",
		"Read REG.RU balances, refills, and invoices safely",
		balance,
		history,
		invoice,
	)
}

func newBillingInvoiceCommand(app *appRuntime) *cobra.Command {
	var listLimit int
	var listOffset int
	list := newOperationCommand(app, commandSpec{
		use:        "list",
		short:      "List unpaid REG.API invoices",
		capability: "billing.invoice.read",
		action:     "billing.invoice.list",
		args:       cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters("limit", listLimit, "offset", listOffset)
		},
	})
	list.Flags().IntVar(&listLimit, "limit", 100, "maximum invoices (1-1024)")
	list.Flags().IntVar(&listOffset, "offset", 0, "zero-based provider offset")
	list.PreRunE = func(_ *cobra.Command, _ []string) error {
		if listLimit < 1 || listLimit > 1024 || listOffset < 0 {
			return UsageError("--limit must be 1-1024 and --offset must be non-negative")
		}
		return nil
	}

	create := newOperationCommand(app, mutationSpec(
		"create",
		"Report the unavailable generic-invoice creation boundary",
		"billing.invoice.create",
		"billing.invoice.create",
		cobra.NoArgs,
	))
	gateUnavailableCommand(
		app,
		create,
		"billing.invoice.create",
		"REG.API has no generic invoice-create operation; use a service-specific order or renewal workflow",
	)
	remove := newOperationCommand(app, mutationSpec(
		"delete <id> [id...]",
		"Delete one or more unpaid invoices",
		"billing.invoice.mutate",
		"billing.invoice.delete",
		positiveDecimalRangeArgs(1, 100),
	))
	show := newOperationCommand(app, readSpec(
		"show <id>",
		"Show documented invoice fields or status-only detail",
		"billing.invoice.read",
		"billing.invoice.show",
		positiveDecimalArgs(1),
	))
	status := newOperationCommand(app, readSpec(
		"status <id>",
		"Show the documented REG.API invoice status",
		"billing.invoice.read",
		"billing.invoice.status",
		positiveDecimalArgs(1),
	))
	paymentLink := newOperationCommand(app, mutationSpec(
		"payment-link <id>",
		"Open a gated browser checkout handoff when captured",
		"billing.checkout",
		"billing.invoice.payment-link",
		positiveDecimalArgs(1),
	))
	paymentLink.PreRunE = func(_ *cobra.Command, _ []string) error {
		if app.flags.noInput && !app.flags.dryRun {
			return InteractiveRequired("billing.invoice.payment-link")
		}
		return nil
	}
	paymentMethodList := newOperationCommand(app, readSpec(
		"list <id>",
		"Report bill-specific methods when the portal contract is captured",
		"billing.checkout",
		"billing.invoice.payment-method.list",
		positiveDecimalArgs(1),
	))
	gateUnavailableCommand(
		app,
		paymentMethodList,
		"billing.checkout.method_list_unavailable",
		"the captured bill-specific chooser exposed no stable payment-method list; continue in the visible checkout browser",
	)
	var paymentType string
	var paymentCurrency string
	paymentMethodSet := newOperationCommand(app, commandSpec{
		use:        "set <id> [id...]",
		short:      "Change payment type using the documented REG.API operation",
		capability: "billing.invoice.mutate",
		action:     "billing.invoice.payment-method.set",
		mutating:   true,
		args:       positiveDecimalRangeArgs(1, 100),
		parameters: func() map[string][]string {
			return operationParameters("type", paymentType, "currency", paymentCurrency)
		},
	})
	paymentMethodSet.Flags().StringVar(&paymentType, "type", "", "payment type: prepay, yamoney, or bank")
	paymentMethodSet.Flags().StringVar(&paymentCurrency, "currency", "", "payment currency: RUR or USD")
	_ = paymentMethodSet.MarkFlagRequired("type")
	_ = paymentMethodSet.MarkFlagRequired("currency")
	paymentMethodSet.PreRunE = func(_ *cobra.Command, _ []string) error {
		if !oneOf(paymentType, "prepay", "yamoney", "bank") {
			return UsageError("--type must be prepay, yamoney, or bank for bill/change_pay_type")
		}
		if !oneOf(paymentCurrency, "RUR", "USD") || (paymentType == "yamoney" && paymentCurrency != "RUR") {
			return UsageError("--currency must be RUR or USD; yamoney supports only RUR")
		}
		return nil
	}
	paymentMethod := newGroupCommand(
		"payment-method",
		"Inspect or change an invoice payment method",
		paymentMethodList,
		paymentMethodSet,
	)

	return newGroupCommand(
		"invoice",
		"Read and mutate REG.RU invoices",
		list,
		show,
		status,
		create,
		remove,
		paymentMethod,
		paymentLink,
	)
}

func positiveDecimalRangeArgs(minimum, maximum int) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if err := cobra.RangeArgs(minimum, maximum)(command, args); err != nil {
			return err
		}
		for _, value := range args {
			number, err := strconv.ParseUint(value, 10, 64)
			if err != nil || number == 0 {
				return UsageError("invoice identifiers must be positive decimal values")
			}
		}
		return nil
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validREGAPICurrency(value string) bool {
	return oneOf(value, "RUR", "USD", "EUR", "UAH")
}

func dateOnly(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func gateUnavailableCommand(
	app *appRuntime,
	command *cobra.Command,
	capability string,
	message string,
) {
	command.PreRunE = func(_ *cobra.Command, _ []string) error {
		if err := app.requireAccount(); err != nil {
			return err
		}
		return CapabilityUnavailable(capability, message)
	}
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
