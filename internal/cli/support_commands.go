package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const maxSupportBodyBytes = 256 << 10

type supportInput func(context.Context) ([]byte, error)

func (i supportInput) Resolve(ctx context.Context) ([]byte, error) {
	return i(ctx)
}

func supportDirectInput(value string) InputResolver {
	return supportInput(func(ctx context.Context) ([]byte, error) {
		return readSupportInput(ctx, strings.NewReader(value))
	})
}

func supportFileInput(path string) InputResolver {
	return supportInput(func(ctx context.Context) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, UsageError("could not read the --file message body")
		}
		defer file.Close()
		return readSupportInput(ctx, file)
	})
}

func supportReaderInput(reader io.Reader) InputResolver {
	return supportInput(func(ctx context.Context) ([]byte, error) {
		return readSupportInput(ctx, reader)
	})
}

func readSupportInput(ctx context.Context, reader io.Reader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := io.ReadAll(io.LimitReader(reader, maxSupportBodyBytes+1))
	if err != nil {
		return nil, UsageError("could not read the support message body")
	}
	if len(value) > maxSupportBodyBytes {
		return nil, UsageError("support message body exceeds 256 KiB")
	}
	if !utf8.Valid(value) {
		return nil, UsageError("support message body must be valid UTF-8")
	}
	if strings.TrimSpace(string(value)) == "" {
		return nil, UsageError("support message body cannot be empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return value, nil
}

func newSupportCommand(app *appRuntime) *cobra.Command {
	var (
		limit  int
		page   int
		status string
	)
	list := newOperationCommand(app, commandSpec{
		use:        "list",
		short:      "List support tickets when the private contract is verified",
		capability: "support.ticket.list",
		action:     "support.ticket.list",
		args:       cobra.NoArgs,
		parameters: func() map[string][]string {
			return operationParameters("limit", limit, "page", page, "status", status)
		},
	})
	list.Flags().IntVar(&limit, "limit", 50, "maximum tickets per page (1-100)")
	list.Flags().IntVar(&page, "page", 1, "one-based page number")
	list.Flags().StringVar(&status, "status", "all", "ticket status filter: all, open, or closed")
	list.PreRunE = func(_ *cobra.Command, _ []string) error {
		if limit < 1 || limit > 100 {
			return UsageError("--limit must be between 1 and 100")
		}
		if page < 1 {
			return UsageError("--page must be a positive integer")
		}
		if !oneOf(status, "all", "open", "closed") {
			return UsageError("--status must be all, open, or closed")
		}
		return nil
	}

	show := newOperationCommand(app, commandSpec{
		use:        "get <id>",
		aliases:    []string{"show"},
		short:      "Show a support ticket and conversation",
		capability: "support.ticket.show",
		action:     "support.ticket.get",
		args:       cobra.ExactArgs(1),
	})

	create := newSupportMessageCommand(app, "create", "Create a support ticket", "support.ticket.create", "support.ticket.create", cobra.NoArgs)
	reply := newSupportMessageCommand(app, "reply <id>", "Reply to a support ticket", "support.ticket.reply", "support.ticket.reply", cobra.ExactArgs(1))

	attach := newOperationCommand(app, commandSpec{
		use:        "attach <id> <path>",
		short:      "Attach a file to a support ticket",
		capability: "support.ticket.attachment",
		action:     "support.ticket.attach",
		mutating:   true,
		args:       cobra.ExactArgs(2),
	})

	closeTicket := newOperationCommand(app, mutationSpec(
		"close <id>", "Close a support ticket", "support.ticket.close", "support.ticket.close", cobra.ExactArgs(1),
	))
	reopen := newOperationCommand(app, mutationSpec(
		"reopen <id>", "Reopen a support ticket", "support.ticket.reopen", "support.ticket.reopen", cobra.ExactArgs(1),
	))

	tickets := newGroupCommand(
		"ticket",
		"Manage experimental private support-ticket operations",
		list, show, create, reply, attach, closeTicket, reopen,
	)
	return newGroupCommand("support", "Use the experimental private support adapter", tickets)
}

func newSupportMessageCommand(
	app *appRuntime,
	use, short, capability, action string,
	args cobra.PositionalArgs,
) *cobra.Command {
	var (
		body        string
		bodyFile    string
		attachments []string
		input       InputResolver
	)
	command := newOperationCommand(app, commandSpec{
		use:        use,
		short:      short,
		capability: capability,
		action:     action,
		mutating:   true,
		args:       args,
		parameters: func() map[string][]string {
			return operationParameters("attachments", attachments)
		},
		input: func() InputResolver { return input },
	})
	command.Flags().StringVar(&body, "body", "", "message body (visible in the process argument list)")
	command.Flags().StringVar(&bodyFile, "file", "", "read the message body from a UTF-8 file")
	command.Flags().StringArrayVar(&attachments, "attachment", nil, "attachment path (repeatable)")
	command.PreRunE = func(command *cobra.Command, _ []string) error {
		bodyChanged := command.Flags().Changed("body")
		fileChanged := command.Flags().Changed("file")
		if bodyChanged && fileChanged {
			return UsageError("--body and --file are mutually exclusive")
		}
		switch {
		case bodyChanged:
			if strings.TrimSpace(body) == "" {
				return UsageError("--body cannot be empty")
			}
			input = supportDirectInput(body)
		case fileChanged:
			if strings.TrimSpace(bodyFile) == "" {
				return UsageError("--file cannot be empty")
			}
			input = supportFileInput(bodyFile)
		default:
			if app.inputIsTTY() {
				return UsageError("pass --body or --file, or pipe the message body on stdin")
			}
			input = supportReaderInput(app.in)
		}
		for _, path := range attachments {
			if strings.TrimSpace(path) == "" {
				return UsageError("--attachment cannot be empty")
			}
		}
		return nil
	}
	return command
}

var _ InputResolver = supportInput(nil)
