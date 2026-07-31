package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	defaultTimeout    = 30 * time.Second
	maxNetworkTimeout = 5 * time.Minute
	defaultLoginTime  = 10 * time.Minute
	minLoginTime      = time.Minute
	maxLoginTime      = 30 * time.Minute
)

type globalFlags struct {
	account string
	json    bool
	plain   bool
	noInput bool
	dryRun  bool
	force   bool
	noColor bool
	timeout time.Duration
}

type appRuntime struct {
	in          io.Reader
	out         io.Writer
	errOut      io.Writer
	getenv      func(string) string
	inputIsTTY  func() bool
	outputIsTTY func() bool
	errorIsTTY  func() bool
	executor    Executor
	flags       globalFlags
	command     string
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

	return &appRuntime{
		in:          options.In,
		out:         options.Out,
		errOut:      options.ErrOut,
		getenv:      options.Getenv,
		inputIsTTY:  options.InputIsTTY,
		outputIsTTY: options.OutputIsTTY,
		errorIsTTY:  options.ErrorIsTTY,
		executor:    options.Executor,
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
	return writeResult(r.out, r.outputMode(), r.command, result)
}

func (r *appRuntime) resolveAccount() {
	if r.flags.account == "" {
		r.flags.account = r.getenv("REGRU_ACCOUNT")
	}
}

func (r *appRuntime) requireAccount() error {
	if strings.TrimSpace(r.flags.account) == "" {
		return AccountRequired()
	}
	return nil
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

	result, err := r.executor.Execute(ctx, operation)
	if err != nil {
		return err
	}
	return r.writeResult(result)
}
