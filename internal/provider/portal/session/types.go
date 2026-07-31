package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	IdentityDigestBytes = 32
	identityKeyBytes    = 32
)

type State string

const (
	StateNotEstablished State = "not-established"
	StateStaged         State = "staged"
	StateActive         State = "active"
	StateUnknown        State = "unknown"
	StateSessionLost    State = "session-lost"
	StateExplicitLogout State = "explicit-logout"
)

type ObservationState string

const (
	ObservedAuthenticated ObservationState = "authenticated"
	ObservedNoSession     ObservationState = "no-session"
	ObservedIncompatible  ObservationState = "incompatible"
)

type OpenMode string

const (
	OpenStagedLogin OpenMode = "staged-login"
	OpenCommitted   OpenMode = "committed-session"
	OpenHandoff     OpenMode = "visible-handoff"
)

type ProgramID string

type Observation struct {
	State          ObservationState
	IdentityDigest []byte
}

type Status struct {
	State  State  `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type LoginSpec struct {
	ProfileID         string
	CurrentSessionRef string
	Force             bool
}

type LoginResult struct {
	SessionRef string
	Status     Status
}

type Profile struct {
	ID         string
	SessionRef string
}

type OpenSpec struct {
	SessionRef string
	ProfileDir string
	Mode       OpenMode
	StartURL   string
	StartupCap time.Duration
	CleanupCap time.Duration
}

type PageExecutor interface {
	RunJSON(
		context.Context,
		ProgramID,
		json.RawMessage,
		*json.RawMessage,
	) error
}

type Browser interface {
	WaitForAuthentication(context.Context, []byte) (Observation, error)
	Refresh(context.Context, []byte) (Observation, error)
	Logout(context.Context, []byte) (Observation, error)
	Executor() PageExecutor
	Close(context.Context) error
}

type BrowserFactory interface {
	Open(context.Context, OpenSpec) (Browser, error)
}

type ErrorCode string

const (
	CodeAccountMismatch ErrorCode = "portal_account_mismatch"
	CodeProfileBusy     ErrorCode = "portal_profile_busy"
	CodeContractDrift   ErrorCode = "private_contract_drift"
	CodeSessionLost     ErrorCode = "portal_session_lost"
	CodeNotEstablished  ErrorCode = "portal_session_not_established"
	CodeLogoutUnknown   ErrorCode = "logout_outcome_unknown"
	CodeBrowser         ErrorCode = "browser_session_error"
	CodeState           ErrorCode = "portal_session_state_error"
)

type Error struct {
	Code ErrorCode
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsCode(err error, code ErrorCode) bool {
	var sessionErr *Error
	return errors.As(err, &sessionErr) && sessionErr.Code == code
}

func sessionError(code ErrorCode, err error) error {
	return &Error{Code: code, Err: err}
}

func validateObservation(observation Observation) error {
	switch observation.State {
	case ObservedAuthenticated:
		if len(observation.IdentityDigest) != IdentityDigestBytes {
			return sessionError(
				CodeContractDrift,
				fmt.Errorf("identity digest has an unexpected size"),
			)
		}
		return nil
	case ObservedNoSession:
		return sessionError(CodeNotEstablished, errors.New("no provider session"))
	case ObservedIncompatible:
		return sessionError(CodeContractDrift, errors.New("provider contract changed"))
	default:
		return sessionError(
			CodeContractDrift,
			fmt.Errorf("unknown observation state"),
		)
	}
}
