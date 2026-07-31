package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	jsontext "github.com/go-json-experiment/json/jsontext"
)

type program struct {
	source         string
	maxResultBytes int
	allowedOrigins []string
}

type pageExecutor struct {
	browser *browser
}

func (e *pageExecutor) RunJSON(
	ctx context.Context,
	id session.ProgramID,
	args json.RawMessage,
	result *json.RawMessage,
) error {
	if e == nil || e.browser == nil || result == nil {
		return errors.New("page executor is unavailable")
	}
	selected, exists := e.browser.programs[id]
	if !exists {
		return errors.New("page program is not allowlisted")
	}
	if len(args) == 0 {
		args = json.RawMessage(`null`)
	}
	if !json.Valid(args) {
		return errors.New("page program arguments are invalid")
	}

	callCtx, cancel := e.browser.callContext(ctx)
	defer cancel()

	var raw json.RawMessage
	err := chromedp.Run(callCtx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		frameTree, err := page.GetFrameTree().Do(actionCtx)
		if err != nil || frameTree == nil || frameTree.Frame == nil {
			return errors.New("could not verify page origin")
		}
		parsed, err := url.Parse(frameTree.Frame.URL)
		if err != nil {
			return errors.New("page origin is invalid")
		}
		origin := parsed.Scheme + "://" + parsed.Host
		if !originAllowed(origin, selected.allowedOrigins) {
			return errors.New("page origin is not allowlisted")
		}

		worldID, err := page.CreateIsolatedWorld(frameTree.Frame.ID).
			WithWorldName("regru-portal").
			WithGrantUniveralAccess(false).
			Do(actionCtx)
		if err != nil {
			return errors.New("isolated page context is unavailable")
		}
		global, exception, err := runtime.Evaluate("globalThis").
			WithContextID(worldID).
			Do(actionCtx)
		if err != nil || exception != nil || global.ObjectID == "" {
			return errors.New("page execution context is unavailable")
		}
		defer runtime.ReleaseObject(global.ObjectID).Do(actionCtx)

		remote, exception, err := runtime.CallFunctionOn(selected.source).
			WithObjectID(global.ObjectID).
			WithArguments([]*runtime.CallArgument{{
				Value: jsontext.Value(append([]byte(nil), args...)),
			}}).
			WithAwaitPromise(true).
			WithReturnByValue(true).
			Do(actionCtx)
		if err != nil {
			return errors.New("page program failed")
		}
		if exception != nil {
			return errors.New("page program returned an exception")
		}
		if len(remote.Value) == 0 || !json.Valid(remote.Value) {
			return errors.New("page program returned invalid JSON")
		}
		if len(remote.Value) > selected.maxResultBytes {
			return fmt.Errorf("page program result exceeds its limit")
		}
		raw = append(raw[:0], remote.Value...)
		return nil
	}))
	if err != nil {
		return err
	}
	*result = raw
	return nil
}

func originAllowed(origin string, allowed []string) bool {
	for _, candidate := range allowed {
		if origin == candidate {
			return true
		}
	}
	return false
}
