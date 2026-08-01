package cli

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/profile"
	"golang.org/x/term"
)

// Options contains process boundaries so command behavior can be tested without
// touching the real terminal, browser, network, or provider APIs.
type Options struct {
	In          io.Reader
	Out         io.Writer
	ErrOut      io.Writer
	Getenv      func(string) string
	InputIsTTY  func() bool
	OutputIsTTY func() bool
	ErrorIsTTY  func() bool
	Executor    Executor
	Profiles    profile.Repository
}

func DefaultOptions() Options {
	return Options{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
		Getenv: os.Getenv,
		InputIsTTY: func() bool {
			return term.IsTerminal(int(os.Stdin.Fd()))
		},
		OutputIsTTY: func() bool {
			return term.IsTerminal(int(os.Stdout.Fd()))
		},
		ErrorIsTTY: func() bool {
			return term.IsTerminal(int(os.Stderr.Fd()))
		},
		Executor: UnavailableExecutor{},
		Profiles: profile.NewDefaultRepository(),
	}
}

type Operation struct {
	Capability     string
	Action         string
	ProfileID      string
	Account        string
	Arguments      []string
	Parameters     map[string][]string
	NoWait         bool
	RequestTimeout time.Duration
	WaitTimeout    time.Duration
	Force          bool
	Credentials    CredentialResolver
	Input          InputResolver
}

type CredentialResolver interface {
	Resolve(context.Context, string) ([]byte, error)
}

// InputResolver defers reading potentially sensitive command input until a
// provider adapter has passed its capability and contract gates.
type InputResolver interface {
	Resolve(context.Context) ([]byte, error)
}

type Result struct {
	Human    string
	Plain    []string
	Data     any
	Warnings []Warning
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Executor is the only operation boundary visible to Cobra handlers. Provider
// clients, browser/CDP code, and private portal adapters live behind it. It is
// the scaffold boundary for placeholders; implementation tickets replace it
// with narrow, consumer-owned interfaces for real use cases.
type Executor interface {
	Execute(context.Context, Operation) (Result, error)
}

type UnavailableExecutor struct{}

func (UnavailableExecutor) Execute(_ context.Context, operation Operation) (Result, error) {
	return Result{}, CapabilityUnavailable(
		operation.Capability,
		"this capability is not implemented in the current build",
	)
}
