package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

// PublicOrigin is the only origin an anonymous public page may read.
const PublicOrigin = "https://www.reg.ru"

const (
	programPublicReady session.ProgramID = "public.ready"
	programPublicFetch session.ProgramID = "public.fetch"

	publicStartupCap   = 30 * time.Second
	publicCleanupCap   = 10 * time.Second
	publicReadyCap     = 20 * time.Second
	publicReadyBackoff = 400 * time.Millisecond
	// PublicMaxBody bounds one public response; the backorder catalogue is
	// about 17.5 MB.
	PublicMaxBody = 48 << 20
)

// ErrPublicChallenge reports that the www.reg.ru anti-bot interstitial did not
// clear inside the page, so no public data was read.
var ErrPublicChallenge = errors.New("the www.reg.ru anti-bot page did not clear")

// ErrPublicNetwork reports a failed in-page request.
var ErrPublicNetwork = errors.New("the www.reg.ru public request failed")

// The anti-bot interstitial computes a cookie in page JavaScript and reloads.
// It is recognised by its helper names; a real browser clears it on its own.
const publicReadyProgram = `function() {
	const html = document.documentElement ? document.documentElement.outerHTML : "";
	return {ready: !!document.body && !(html.includes("get_jhash(") && html.includes("__js_p_"))};
}`

const publicFetchProgram = `async function(args) {
	if (!args || typeof args.url !== "string" || !args.url.startsWith("https://www.reg.ru/")) return {state: "invalid"};
	let response;
	try {
		response = await fetch(args.url, {credentials: "same-origin", cache: "no-store", redirect: "follow"});
	} catch (_) {
		return {state: "network"};
	}
	let body;
	try { body = await response.text(); } catch (_) { return {state: "network"}; }
	if (body.includes("get_jhash(") && body.includes("__js_p_")) return {state: "challenge"};
	let origin = "";
	try { origin = new URL(response.url).origin; } catch (_) {}
	return {
		state: "ok",
		status: response.status,
		origin,
		contentType: response.headers.get("content-type") || "",
		lastModified: response.headers.get("last-modified") || "",
		body,
	};
}`

func publicPrograms() map[session.ProgramID]program {
	origins := []string{PublicOrigin}
	return map[session.ProgramID]program{
		programPublicReady: {source: publicReadyProgram, maxResultBytes: 256, allowedOrigins: origins},
		programPublicFetch: {source: publicFetchProgram, maxResultBytes: PublicMaxBody, allowedOrigins: origins},
	}
}

// PublicBrowser opens throwaway, anonymous, headless browsers for public
// www.reg.ru pages that sit behind the anti-bot interstitial. It never uses an
// account profile or a portal session: each page gets a fresh temporary
// profile that is deleted on Close.
type PublicBrowser struct {
	factory *Factory
}

// NewPublicBrowser returns a public-page browser opener.
func NewPublicBrowser(config Config) *PublicBrowser {
	return &PublicBrowser{factory: &Factory{browserPath: config.BrowserPath, programs: publicPrograms()}}
}

// PublicPage is one open anonymous page on www.reg.ru.
type PublicPage struct {
	browser session.Browser
	dir     string
}

// PublicResponse is one in-page GET answer.
type PublicResponse struct {
	Status       int
	ContentType  string
	LastModified string
	Body         string
}

// Open starts a headless browser on startURL (a www.reg.ru page) and waits,
// bounded, until the anti-bot interstitial has cleared.
func (p *PublicBrowser) Open(ctx context.Context, startURL string) (*PublicPage, error) {
	if !strings.HasPrefix(startURL, PublicOrigin+"/") {
		return nil, errors.New("public start URL is not on www.reg.ru")
	}
	dir, err := os.MkdirTemp("", "regru-public-")
	if err != nil {
		return nil, errors.New("could not create a temporary browser profile")
	}
	opened, err := p.factory.Open(ctx, session.OpenSpec{
		ProfileDir: dir, Mode: session.OpenCommitted, StartURL: startURL,
		StartupCap: publicStartupCap, CleanupCap: publicCleanupCap,
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	page := &PublicPage{browser: opened, dir: dir}
	if err := page.waitReady(ctx); err != nil {
		_ = page.Close()
		return nil, err
	}
	return page, nil
}

func (p *PublicPage) waitReady(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, publicReadyCap)
	defer cancel()
	for {
		var raw json.RawMessage
		err := p.browser.Executor().RunJSON(readyCtx, programPublicReady, nil, &raw)
		if err == nil {
			var state struct {
				Ready bool `json:"ready"`
			}
			if json.Unmarshal(raw, &state) == nil && state.Ready {
				return nil
			}
		}
		timer := time.NewTimer(publicReadyBackoff)
		select {
		case <-readyCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrPublicChallenge
		case <-timer.C:
		}
	}
}

// Get reads one www.reg.ru path (starting with "/") from inside the page.
func (p *PublicPage) Get(ctx context.Context, path string) (PublicResponse, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return PublicResponse{}, errors.New("public path must be origin-relative")
	}
	args, err := json.Marshal(map[string]string{"url": PublicOrigin + path})
	if err != nil {
		return PublicResponse{}, err
	}
	var raw json.RawMessage
	if err := p.browser.Executor().RunJSON(ctx, programPublicFetch, args, &raw); err != nil {
		if ctx.Err() != nil {
			return PublicResponse{}, ctx.Err()
		}
		return PublicResponse{}, ErrPublicNetwork
	}
	var answer struct {
		State        string `json:"state"`
		Status       int    `json:"status"`
		Origin       string `json:"origin"`
		ContentType  string `json:"contentType"`
		LastModified string `json:"lastModified"`
		Body         string `json:"body"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return PublicResponse{}, ErrPublicNetwork
	}
	switch answer.State {
	case "ok":
	case "challenge":
		return PublicResponse{}, ErrPublicChallenge
	default:
		return PublicResponse{}, ErrPublicNetwork
	}
	if answer.Origin != PublicOrigin {
		return PublicResponse{}, ErrPublicNetwork
	}
	return PublicResponse{
		Status: answer.Status, ContentType: answer.ContentType,
		LastModified: answer.LastModified, Body: answer.Body,
	}, nil
}

// Close stops the browser and deletes its temporary profile.
func (p *PublicPage) Close() error {
	if p == nil || p.browser == nil {
		return nil
	}
	err := p.browser.Close(context.Background())
	_ = os.RemoveAll(p.dir)
	p.browser = nil
	return err
}
