// Command prototype-support is a throwaway terminal shell for the pure support
// mutation state model in the parent package.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	prototype "github.com/adinvadim/reg-ru-cli/internal/provider/support/prototype"
)

const (
	bold  = "\x1b[1m"
	dim   = "\x1b[2m"
	reset = "\x1b[0m"
)

func main() {
	state := prototype.Initial()
	scanner := bufio.NewScanner(os.Stdin)
	message := "Choose a transition."

	for {
		render(state, message)
		if !scanner.Scan() {
			return
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "q" {
			return
		}

		event, ok := eventFor(input)
		if !ok {
			message = "Unknown key."
			continue
		}

		next, err := prototype.Apply(state, event)
		if err != nil {
			message = "BLOCKED: " + err.Error()
			continue
		}

		state = next
		message = "Applied: " + string(event)
	}
}

func render(state prototype.State, message string) {
	fmt.Print("\x1b[2J\x1b[H")
	fmt.Printf("%sPROTOTYPE — support mutation outcome%s\n\n", bold, reset)
	fmt.Printf("%sphase%s                       %s\n", bold, reset, state.Phase)
	fmt.Printf("%sintent fingerprint%s          %s%s%s\n", bold, reset, dim, prototype.Fingerprint(state), reset)
	fmt.Printf("%slocal attempt%s               %d\n", bold, reset, state.Attempt)
	fmt.Printf("%sdispatch may have started%s   %t\n", bold, reset, state.DispatchMayHaveStarted)
	fmt.Printf("%sidentical intent blocked%s    %t\n", bold, reset, state.IdenticalIntentBlocked)
	fmt.Printf("%slast transition%s             %s\n\n", bold, reset, state.LastTransition)
	fmt.Printf("%s%s%s\n\n", dim, message, reset)
	fmt.Printf("%s[d]%s dispatch  %s[n]%s prove not-sent  %s[c]%s committed  %s[r]%s rejected\n", bold, reset, bold, reset, bold, reset, bold, reset)
	fmt.Printf("%s[u]%s outcome unknown  %s[t]%s retry same intent\n", bold, reset, bold, reset)
	fmt.Printf("%s[i]%s distinct intent  %s[q]%s quit\n", bold, reset, bold, reset)
	fmt.Print("\n> ")
}

func eventFor(input string) (prototype.Event, bool) {
	events := map[string]prototype.Event{
		"d": prototype.BeginDispatch,
		"n": prototype.FailBeforeSend,
		"c": prototype.RecognizeCommit,
		"r": prototype.RecognizeReject,
		"u": prototype.LoseOutcome,
		"t": prototype.RetrySameIntent,
		"i": prototype.StartNewIntent,
	}
	event, ok := events[input]
	return event, ok
}
