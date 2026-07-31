package cli

import (
	"fmt"
	"strings"

	"github.com/adinvadim/reg-ru-cli/internal/version"
	"github.com/spf13/cobra"
)

func newRootCommand(app *appRuntime) *cobra.Command {
	root := &cobra.Command{
		Use:   "regru",
		Short: "Manage REG.RU infrastructure and accounts safely",
		Long:  "regru is a human-friendly and agent-safe CLI for REG.RU CloudVPS, S3, billing, authentication, and support workflows.",
		Example: `  regru --account personal auth status --json
  regru --account personal --dry-run vps delete vps-id
  regru completion zsh`,
		Version:       version.String(),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			app.command = canonicalCommand(cmd)
			if app.flags.json && app.flags.plain {
				return UsageError("--json and --plain are mutually exclusive")
			}
			if app.flags.timeout <= 0 || app.flags.timeout > maxNetworkTimeout {
				return UsageError(fmt.Sprintf(
					"--timeout must be greater than zero and no more than %s",
					maxNetworkTimeout,
				))
			}
			if app.flags.waitTimeout < minWaitTime || app.flags.waitTimeout > maxWaitTime {
				return UsageError(fmt.Sprintf(
					"--wait-timeout must be between %s and %s",
					minWaitTime,
					maxWaitTime,
				))
			}
			if err := app.loadProfiles(); err != nil {
				return err
			}
			return app.resolveAccount(cmd.Root().PersistentFlags().Changed("account"))
		},
	}

	root.SetIn(app.in)
	root.SetOut(app.out)
	root.SetErr(app.errOut)
	root.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	root.Flags().Bool("version", false, "print version information and exit")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return UsageError(err.Error())
	})
	root.DisableFlagsInUseLine = true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{
		Use:    "help [command]",
		Short:  "Help about any command",
		Hidden: false,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, _, err := cmd.Root().Find(args)
			if err != nil {
				return UsageError(err.Error())
			}
			return target.Help()
		},
	})

	flags := root.PersistentFlags()
	flags.StringVar(&app.flags.account, "account", "", "REG.RU account profile (or REGRU_ACCOUNT)")
	flags.BoolVar(&app.flags.json, "json", false, "emit a versioned JSON envelope")
	flags.BoolVar(&app.flags.plain, "plain", false, "emit stable line-oriented text")
	flags.BoolVar(&app.flags.noInput, "no-input", false, "never prompt or request interactive input")
	flags.BoolVarP(&app.flags.dryRun, "dry-run", "n", false, "preview a mutation without sending a request")
	flags.BoolVarP(&app.flags.force, "force", "f", false, "skip mutation confirmation")
	flags.BoolVar(&app.flags.noColor, "no-color", false, "disable ANSI color")
	flags.DurationVar(&app.flags.timeout, "timeout", defaultTimeout, "network operation timeout")

	root.AddCommand(
		newAccountCommand(app),
		newCapabilityCommand(app),
		newAuthCommand(app),
		newVPSCommand(app),
		newS3Command(app),
		newBillingCommand(app),
		newSupportCommand(app),
		newCompletionCommand(app),
	)

	return root
}

func newCompletionCommand(app *appRuntime) *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate shell completion code",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if app.flags.json || app.flags.plain {
				return UsageError("completion emits a raw shell script and cannot use --json or --plain")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch strings.ToLower(args[0]) {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return UsageError("unsupported shell; choose bash, zsh, fish, or powershell")
			}
		},
	}
}

func canonicalCommand(command *cobra.Command) string {
	path := command.CommandPath()
	path = strings.TrimPrefix(path, command.Root().Name())
	return strings.TrimSpace(path)
}
