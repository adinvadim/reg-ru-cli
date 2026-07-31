package cli

import (
	"fmt"
	"sort"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/spf13/cobra"
)

func newAccountCommand(app *appRuntime) *cobra.Command {
	label := ""
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a non-secret local account profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if app.flags.dryRun {
				return app.writeResult(Result{
					Human: fmt.Sprintf("Would add account %s", name),
					Plain: []string{escapePlainField(name) + "\tadd"},
					Data: map[string]any{
						"dry_run": true,
						"account": name,
						"action":  "account.add",
					},
				})
			}
			added, err := app.profiles.Add(name, profile.Account{Label: label})
			if err != nil {
				return ConfigurationError("account profile could not be added")
			}
			if err := app.loadProfiles(); err != nil {
				return err
			}
			return app.writeResult(Result{
				Human: fmt.Sprintf("Added account %s", name),
				Plain: []string{escapePlainField(name) + "\tadded"},
				Data: map[string]any{
					"account":  name,
					"label":    added.Label,
					"provider": added.Provider,
				},
			})
		},
	}
	add.Flags().StringVar(&label, "label", "", "human-readable non-secret label")

	list := &cobra.Command{
		Use:   "list",
		Short: "List configured account profiles",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			names := sortedAccountNames(app.config.Accounts)
			plain := make([]string, 0, len(names))
			data := make([]map[string]any, 0, len(names))
			for _, name := range names {
				account := app.config.Accounts[name]
				isDefault := name == app.config.DefaultAccount
				isProject := name == app.config.ProjectAccount
				plain = append(plain, fmt.Sprintf(
					"%s\t%s\t%t\t%t",
					escapePlainField(name),
					escapePlainField(account.Label),
					isDefault,
					isProject,
				))
				data = append(data, map[string]any{
					"account":         name,
					"label":           account.Label,
					"default":         isDefault,
					"projectSelected": isProject,
				})
			}
			return app.writeResult(Result{
				Human: renderAccountList(names, app.config),
				Plain: plain,
				Data:  data,
			})
		},
	}

	show := &cobra.Command{
		Use:   "show [name]",
		Short: "Show redacted account-profile metadata",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name, account, err := app.accountFromArgs(args)
			if err != nil {
				return err
			}
			return app.writeResult(accountResult("Account "+name, name, account))
		},
	}

	use := &cobra.Command{
		Use:   "use <name>",
		Short: "Select the default user account profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if _, exists := app.config.Accounts[name]; !exists {
				return AccountNotFound(name)
			}
			if app.flags.dryRun {
				return app.writeResult(Result{
					Human: fmt.Sprintf("Would select account %s", name),
					Plain: []string{escapePlainField(name) + "\tuse"},
					Data: map[string]any{
						"dry_run": true,
						"account": name,
						"action":  "account.use",
					},
				})
			}
			if err := app.profiles.SetCurrent(name); err != nil {
				return ConfigurationError("default account could not be updated")
			}
			if err := app.loadProfiles(); err != nil {
				return err
			}
			return app.writeResult(Result{
				Human: fmt.Sprintf("Default account is now %s", name),
				Plain: []string{escapePlainField(name) + "\tdefault"},
				Data:  map[string]any{"account": name, "default": true},
			})
		},
	}

	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove local profile metadata without revoking credentials",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			if _, exists := app.config.Accounts[name]; !exists {
				return AccountNotFound(name)
			}
			if app.flags.dryRun {
				return app.writeResult(Result{
					Human: fmt.Sprintf("Would remove account %s", name),
					Plain: []string{escapePlainField(name) + "\tremove"},
					Data: map[string]any{
						"dry_run": true,
						"account": name,
						"action":  "account.remove",
					},
				})
			}
			if err := app.confirm("account.remove"); err != nil {
				return err
			}
			if err := app.profiles.Remove(name); err != nil {
				return ConfigurationError("account profile could not be removed")
			}
			if err := app.loadProfiles(); err != nil {
				return err
			}
			return app.writeResult(Result{
				Human: fmt.Sprintf("Removed account %s", name),
				Plain: []string{escapePlainField(name) + "\tremoved"},
				Data:  map[string]any{"account": name, "removed": true},
			})
		},
	}

	doctor := &cobra.Command{
		Use:   "doctor [name]",
		Short: "Inspect local account readiness without contacting REG.RU",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name, account, err := app.accountFromArgs(args)
			if err != nil {
				return err
			}
			result := accountResult("Account "+name+" configuration is valid", name, account)
			data := result.Data.(map[string]any)
			data["selectionSource"] = app.accountSource
			data["nextAction"] = nextAccountAction(account)
			return app.writeResult(result)
		},
	}

	return newGroupCommand(
		"account",
		"Manage non-secret local account profiles",
		add,
		list,
		show,
		use,
		remove,
		doctor,
	)
}

func newCapabilityCommand(app *appRuntime) *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "List locally configured capability states",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := app.requireAccount(); err != nil {
				return err
			}
			states := localCapabilityStates(app.profile)
			names := make([]string, 0, len(states))
			for name := range states {
				names = append(names, name)
			}
			sort.Strings(names)
			plain := make([]string, 0, len(names))
			data := make([]map[string]string, 0, len(names))
			for _, name := range names {
				plain = append(plain, fmt.Sprintf(
					"%s\t%s",
					escapePlainField(name),
					states[name],
				))
				data = append(data, map[string]string{
					"capability":   name,
					"availability": states[name],
				})
			}
			return app.writeResult(Result{
				Human: fmt.Sprintf("%d capabilities for %s", len(names), app.flags.account),
				Plain: plain,
				Data:  data,
			})
		},
	}

	probe := newOperationCommand(app, commandSpec{
		use:        "probe",
		short:      "Run bounded authenticated capability probes",
		capability: "account.capabilities",
		action:     "capability.probe",
		args:       cobra.NoArgs,
	})
	return newGroupCommand(
		"capability",
		"Inspect configured and verified provider capabilities",
		list,
		probe,
	)
}

func (r *appRuntime) accountFromArgs(args []string) (string, profile.Account, error) {
	name := r.flags.account
	if len(args) == 1 {
		name = args[0]
	}
	if name == "" {
		return "", profile.Account{}, AccountRequired()
	}
	account, exists := r.config.Accounts[name]
	if !exists {
		return "", profile.Account{}, AccountNotFound(name)
	}
	return name, account, nil
}

func sortedAccountNames(accounts map[string]profile.Account) []string {
	names := make([]string, 0, len(accounts))
	for name := range accounts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func renderAccountList(names []string, config profile.Config) string {
	if len(names) == 0 {
		return "No account profiles configured"
	}
	lines := make([]string, 0, len(names))
	for _, name := range names {
		suffix := ""
		if name == config.DefaultAccount {
			suffix += " (default)"
		}
		if name == config.ProjectAccount {
			suffix += " (project)"
		}
		lines = append(lines, name+suffix)
	}
	return joinLines(lines)
}

func accountResult(human, name string, account profile.Account) Result {
	configured := map[string]bool{
		"credentialProcess": len(account.CredentialProcess.Command) > 0,
		"portal":            account.Portal.SessionRef != "",
		"cloudvps": account.Credentials.CloudVPSRef != "" ||
			account.Portal.SessionRef != "",
		"regapi": account.Credentials.REGAPIRef != "",
		"s3": account.Credentials.S3Ref != "" ||
			account.Portal.SessionRef != "",
	}
	return Result{
		Human: human,
		Plain: []string{
			fmt.Sprintf(
				"%s\t%s\t%s\t%t\t%t\t%t\t%t",
				escapePlainField(name),
				escapePlainField(account.Label),
				escapePlainField(account.Provider),
				configured["portal"],
				configured["cloudvps"],
				configured["regapi"],
				configured["s3"],
			),
		},
		Data: map[string]any{
			"account":    name,
			"label":      account.Label,
			"provider":   account.Provider,
			"configured": configured,
		},
	}
}

func localCapabilityStates(account profile.Account) map[string]string {
	state := func(configured bool) string {
		if configured {
			return "configured"
		}
		return "not_configured"
	}
	return map[string]string{
		"auth.portal_session": state(account.Portal.SessionRef != ""),
		"cloudvps.instances": state(
			account.Credentials.CloudVPSRef != "" ||
				account.Portal.SessionRef != "",
		),
		"billing.read": state(
			account.Credentials.REGAPIRef != "" ||
				account.Credentials.CloudVPSRef != "",
		),
		"billing.checkout": state(account.Portal.SessionRef != ""),
		"s3.bucket": state(
			account.Credentials.S3Ref != "" ||
				account.Portal.SessionRef != "",
		),
		"support.private": state(account.Portal.SessionRef != ""),
	}
}

func nextAccountAction(account profile.Account) string {
	switch {
	case account.Portal.SessionRef == "":
		return "run auth login to establish a portal session"
	case account.Credentials.REGAPIRef == "" &&
		len(account.CredentialProcess.Command) == 0:
		return "configure credential_process in the user account profile when REG.API access is needed"
	default:
		return "run capability probe for authenticated reachability"
	}
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := lines[0]
	for _, line := range lines[1:] {
		result += "\n" + line
	}
	return result
}
