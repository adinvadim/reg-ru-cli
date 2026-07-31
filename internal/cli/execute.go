package cli

import "context"

func Execute(ctx context.Context, args []string, options Options) int {
	app := newRuntime(options)
	root := newRootCommand(app)
	root.SetArgs(args)

	executed, err := root.ExecuteContextC(ctx)
	if app.command == "" && executed != nil {
		app.command = canonicalCommand(executed)
	}
	if err != nil {
		cliErr := classifyError(err)
		_ = writeError(
			app.errOut,
			app.outputMode(),
			app.command,
			cliErr,
			app.colorEnabled(),
		)
		return cliErr.ExitCode
	}

	return ExitOK
}
