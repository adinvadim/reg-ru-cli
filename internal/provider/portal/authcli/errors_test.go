package authcli

import (
	"context"
	"errors"
	"testing"

	"github.com/adinvadim/reg-ru-cli/internal/cli"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/cdp"
	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

func TestTranslateSessionErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		action   string
		input    error
		wantCode string
	}{
		{
			name:     "missing browser",
			action:   "auth.login",
			input:    sessionFailure(session.CodeBrowser, cdp.ErrBrowserNotFound),
			wantCode: cli.CodeMissingBrowser,
		},
		{
			name:     "closed login browser",
			action:   "auth.login",
			input:    sessionFailure(session.CodeBrowser, errors.New("closed")),
			wantCode: cli.CodeLoginCancelled,
		},
		{
			name:     "interrupted status browser",
			action:   "auth.status",
			input:    sessionFailure(session.CodeBrowser, errors.New("closed")),
			wantCode: cli.CodeBrowserSession,
		},
		{
			name:     "account mismatch",
			action:   "auth.status",
			input:    sessionFailure(session.CodeAccountMismatch, errors.New("mismatch")),
			wantCode: cli.CodeAccountMismatch,
		},
		{
			name:     "profile busy",
			action:   "auth.status",
			input:    sessionFailure(session.CodeProfileBusy, errors.New("busy")),
			wantCode: cli.CodePortalProfileBusy,
		},
		{
			name:     "contract drift",
			action:   "auth.refresh",
			input:    sessionFailure(session.CodeContractDrift, errors.New("drift")),
			wantCode: cli.CodeContractDrift,
		},
		{
			name:     "lost session",
			action:   "auth.refresh",
			input:    sessionFailure(session.CodeSessionLost, errors.New("lost")),
			wantCode: cli.CodeAuthExpired,
		},
		{
			name:     "ambiguous logout",
			action:   "auth.logout",
			input:    sessionFailure(session.CodeLogoutUnknown, errors.New("unknown")),
			wantCode: cli.CodeOutcomeUnknown,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			translated := translate(cli.Operation{
				Action:     testCase.action,
				Account:    "personal",
				Capability: "auth.browser_session",
			}, testCase.input)
			var cliErr *cli.CLIError
			if !errors.As(translated, &cliErr) {
				t.Fatalf("translate() error = %T, want *cli.CLIError", translated)
			}
			if cliErr.Code != testCase.wantCode {
				t.Errorf("code = %q, want %q", cliErr.Code, testCase.wantCode)
			}
		})
	}
}

func TestTranslatePreservesCommandCancellation(t *testing.T) {
	t.Parallel()

	for _, input := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := translate(cli.Operation{}, input); !errors.Is(got, input) {
			t.Errorf("translate(%v) = %v", input, got)
		}
	}
}

func sessionFailure(code session.ErrorCode, cause error) error {
	return &session.Error{Code: code, Err: cause}
}
