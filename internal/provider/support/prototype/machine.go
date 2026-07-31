// Package prototype contains a throwaway state model for support mutations.
// It exists to answer the question documented in README.md, not as production
// support-adapter code.
package prototype

import "fmt"

type Phase string

const (
	Prepared       Phase = "prepared"
	Dispatching    Phase = "dispatching"
	NotSent        Phase = "not-sent"
	Committed      Phase = "committed"
	Rejected       Phase = "rejected"
	OutcomeUnknown Phase = "outcome-unknown"
)

type Event string

const (
	BeginDispatch   Event = "begin-dispatch"
	FailBeforeSend  Event = "fail-before-send"
	RecognizeCommit Event = "recognize-commit"
	RecognizeReject Event = "recognize-reject"
	LoseOutcome     Event = "lose-outcome"
	RetrySameIntent Event = "retry-same-intent"
	StartNewIntent  Event = "start-new-intent"
)

type State struct {
	Phase                  Phase
	IntentVersion          int
	Attempt                int
	DispatchMayHaveStarted bool
	IdenticalIntentBlocked bool
	LastTransition         string
}

func Initial() State {
	return State{
		Phase:          Prepared,
		IntentVersion:  1,
		Attempt:        1,
		LastTransition: "synthetic intent prepared; no provider I/O has occurred",
	}
}

func Apply(state State, event Event) (State, error) {
	next := state

	switch event {
	case BeginDispatch:
		if state.Phase != Prepared {
			return state, illegal(state, event)
		}
		next.Phase = Dispatching
		next.DispatchMayHaveStarted = true
		next.LastTransition = "crossed dispatch boundary; failure can no longer be called not-sent"

	case FailBeforeSend:
		if state.Phase != Prepared {
			return state, illegal(state, event)
		}
		next.Phase = NotSent
		next.LastTransition = "proved dispatch did not start; identical retry is safe"

	case RecognizeCommit:
		if state.Phase != Dispatching {
			return state, illegal(state, event)
		}
		next.Phase = Committed
		next.IdenticalIntentBlocked = true
		next.LastTransition = "recognized provider success; identical intent remains blocked"

	case RecognizeReject:
		if state.Phase != Dispatching {
			return state, illegal(state, event)
		}
		next.Phase = Rejected
		next.LastTransition = "recognized provider rejection; no commit is claimed"

	case LoseOutcome:
		if state.Phase != Dispatching {
			return state, illegal(state, event)
		}
		next.Phase = OutcomeUnknown
		next.IdenticalIntentBlocked = true
		next.LastTransition = "response became ambiguous after possible dispatch; identical intent stays blocked"

	case RetrySameIntent:
		allowed := state.Phase == NotSent || state.Phase == Rejected
		if !allowed {
			return state, illegal(state, event)
		}
		next.Phase = Prepared
		next.Attempt++
		next.DispatchMayHaveStarted = false
		next.IdenticalIntentBlocked = false
		next.LastTransition = "prepared a new local attempt for the same intent; provider idempotency is not claimed"

	case StartNewIntent:
		if state.Phase == Prepared || state.Phase == Dispatching {
			return state, illegal(state, event)
		}
		next = Initial()
		next.IntentVersion = state.IntentVersion + 1
		next.LastTransition = "prepared a distinct synthetic intent"

	default:
		return state, fmt.Errorf("unknown event %q", event)
	}

	return next, nil
}

func Fingerprint(state State) string {
	return fmt.Sprintf("demo-intent-v%d", state.IntentVersion)
}

func illegal(state State, event Event) error {
	return fmt.Errorf("%s is not legal while phase is %s", event, state.Phase)
}
