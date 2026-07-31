package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"github.com/adinvadim/reg-ru-cli/internal/secretinput"
)

const (
	defaultTimeout    = 30 * time.Second
	maxNetworkTimeout = 5 * time.Minute
	defaultLoginTime  = 10 * time.Minute
	minLoginTime      = time.Minute
	maxLoginTime      = 30 * time.Minute
)

type globalFlags struct {
	account          string
	json             bool
	plain            bool
	noInput          bool
	dryRun           bool
	force            bool
	noColor          bool
	timeout          time.Duration
	credentialsStdin bool
}

type appRuntime struct {
	in            io.Reader
	out           io.Writer
	errOut        io.Writer
	getenv        func(string) string
	inputIsTTY    func() bool
	outputIsTTY   func() bool
	errorIsTTY    func() bool
	executor      Executor
	profiles      profile.Repository
	config        profile.Config
	profile       profile.Account
	credentials   *secretinput.Resolver
	accountSource string
	flags         globalFlags
	command       string
}

func newRuntime(options Options) *appRuntime {
	defaults := DefaultOptions()
	if options.In == nil {
		options.In = defaults.In
	}
	if options.Out == nil {
		options.Out = defaults.Out
	}
	if options.ErrOut == nil {
		options.ErrOut = defaults.ErrOut
	}
	if options.Getenv == nil {
		options.Getenv = defaults.Getenv
	}
	if options.InputIsTTY == nil {
		options.InputIsTTY = defaults.InputIsTTY
	}
	if options.OutputIsTTY == nil {
		options.OutputIsTTY = defaults.OutputIsTTY
	}
	if options.ErrorIsTTY == nil {
		options.ErrorIsTTY = defaults.ErrorIsTTY
	}
	if options.Executor == nil {
		options.Executor = defaults.Executor
	}
	if options.Profiles == nil {
		options.Profiles = defaults.Profiles
	}

	return &appRuntime{
		in:          options.In,
		out:         options.Out,
		errOut:      options.ErrOut,
		getenv:      options.Getenv,
		inputIsTTY:  options.InputIsTTY,
		outputIsTTY: options.OutputIsTTY,
		errorIsTTY:  options.ErrorIsTTY,
		executor:    options.Executor,
		profiles:    options.Profiles,
		flags: globalFlags{
			timeout: defaultTimeout,
		},
	}
}

func (r *appRuntime) outputMode() outputMode {
	if r.flags.json {
		return outputJSON
	}
	if r.flags.plain {
		return outputPlain
	}
	return outputHuman
}

func (r *appRuntime) colorEnabled() bool {
	return r.outputMode() == outputHuman &&
		!r.flags.noColor &&
		r.errorIsTTY() &&
		r.getenv("NO_COLOR") == "" &&
		r.getenv("TERM") != "dumb"
}

func (r *appRuntime) writeResult(result Result) error {
	var rendered bytes.Buffer
	if err := writeResult(&rendered, r.outputMode(), r.command, result); err != nil {
		return err
	}
	if r.credentials != nil && r.credentials.Contains(rendered.String()) {
		return SensitiveOutputBlocked()
	}
	_, err := io.Copy(r.out, &rendered)
	return err
}

func (r *appRuntime) loadProfiles() error {
	config, err := r.profiles.Load()
	if err != nil {
		return ConfigurationError("profile configuration is invalid")
	}
	r.config = config
	return nil
}

func (r *appRuntime) resolveAccount(explicit bool) error {
	if explicit {
		if strings.TrimSpace(r.flags.account) == "" {
			return UsageError("--account cannot be empty")
		}
		r.accountSource = "flag"
		return nil
	}
	if value := strings.TrimSpace(r.getenv("REGRU_ACCOUNT")); value != "" {
		r.flags.account = value
		r.accountSource = "environment"
		return nil
	}
	if r.config.ProjectAccount != "" {
		r.flags.account = r.config.ProjectAccount
		r.accountSource = "project"
		return nil
	}
	if r.config.DefaultAccount != "" {
		r.flags.account = r.config.DefaultAccount
		r.accountSource = "user-default"
	}
	return nil
}

func (r *appRuntime) requireAccount() error {
	if strings.TrimSpace(r.flags.account) == "" {
		return AccountRequired()
	}
	selected, exists := r.config.Accounts[r.flags.account]
	if !exists {
		return AccountNotFound(r.flags.account)
	}
	r.profile = selected
	return nil
}

func (r *appRuntime) loadCredentials() error {
	if !r.flags.credentialsStdin || r.credentials != nil {
		return nil
	}
	if r.inputIsTTY() {
		return SecretInputError("credential input must be piped on stdin, not read from a terminal")
	}
	resolver, err := secretinput.Load(r.in)
	if err != nil {
		return SecretInputError("credential input is invalid")
	}
	r.credentials = resolver
	return nil
}

func (r *appRuntime) close() {
	if r.credentials != nil {
		r.credentials.Close()
	}
}

func (r *appRuntime) sanitizeError(cliErr *CLIError) *CLIError {
	if cliErr == nil || r.credentials == nil {
		return cliErr
	}
	details, _ := json.Marshal(cliErr.Details)
	if r.credentials.Contains(cliErr.Code) ||
		r.credentials.Contains(cliErr.Message) ||
		r.credentials.Contains(string(details)) {
		return SensitiveOutputBlocked()
	}
	copy := *cliErr
	copy.Cause = nil
	return &copy
}

func (r *appRuntime) confirm(action string) error {
	if r.flags.force || r.flags.dryRun {
		return nil
	}
	if r.flags.noInput || !r.inputIsTTY() {
		return ConfirmationRequired(action)
	}

	if _, err := fmt.Fprintf(r.errOut, "Confirm %s? [y/N] ", action); err != nil {
		return err
	}
	answer, err := bufio.NewReader(r.in).ReadString('\n')
	if err != nil && len(answer) == 0 {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return ConfirmationDeclined(action)
	}
}

func (r *appRuntime) executeOperation(
	ctx context.Context,
	operation Operation,
	mutating bool,
) error {
	if err := r.requireAccount(); err != nil {
		return err
	}

	if r.flags.dryRun && mutating {
		return r.writeResult(Result{
			Human: fmt.Sprintf("Would run %s for account %s", operation.Action, operation.Account),
			Plain: []string{
				fmt.Sprintf(
					"%s\t%s\t%d",
					escapePlainField(operation.Account),
					escapePlainField(operation.Action),
					len(operation.Arguments),
				),
			},
			Data: map[string]any{
				"dry_run":        true,
				"account":        operation.Account,
				"action":         operation.Action,
				"argument_count": len(operation.Arguments),
			},
		})
	}

	if mutating {
		if err := r.confirm(operation.Action); err != nil {
			return err
		}
	}
	if err := r.loadCredentials(); err != nil {
		return err
	}
	operation.ProfileID = r.profile.ID
	operation.Credentials = r.credentials

	result, err := r.executor.Execute(ctx, operation)
	if err != nil {
		return err
	}
	return r.writeResult(result)
}
