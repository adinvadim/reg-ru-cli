package cdp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
)

func TestPageExecutorKeepsCredentialsInsideSyntheticBFFAndGraphQLFixture(t *testing.T) {
	browserPath, err := FindBrowser("")
	if err != nil {
		t.Skipf("supported Chrome/Chromium is unavailable: %v", err)
	}

	const cookieValue = "synthetic-cookie-must-not-cross-cdp"
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/":
			http.SetCookie(writer, &http.Cookie{
				Name:     "fixture_session",
				Value:    cookieValue,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
			_, _ = writer.Write([]byte("<!doctype html><title>fixture</title>"))
		case "/bff/private":
			cookie, cookieErr := request.Cookie("fixture_session")
			if cookieErr != nil || cookie.Value != cookieValue {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ok":true,"kind":"bff"}`))
		case "/graphql":
			cookie, cookieErr := request.Cookie("fixture_session")
			if cookieErr != nil || cookie.Value != cookieValue {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			if request.Method != http.MethodPost {
				http.Error(writer, "method", http.StatusMethodNotAllowed)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(
				`{"data":{"viewer":{"__typename":"SyntheticViewer"}}}`,
			))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	profileDir := filepath.Join(t.TempDir(), "chrome")
	if err := os.Mkdir(profileDir, 0o700); err != nil {
		t.Fatalf("Mkdir(profile): %v", err)
	}
	factory := NewFactory(Config{BrowserPath: browserPath})
	factory.programs[session.ProgramID("fixture.bff")] = program{
		source: `async function(args) {
			const response = await fetch(args.url, {credentials: "include"});
			return {status: response.status, body: await response.json()};
		}`,
		maxResultBytes: 4096,
		allowedOrigins: []string{server.URL},
	}
	factory.programs[session.ProgramID("fixture.graphql")] = program{
		source: `async function(args) {
			const response = await fetch(args.url, {
				method: "POST",
				credentials: "include",
				headers: {"content-type": "application/json"},
				body: JSON.stringify({query: "query SyntheticViewer { viewer { __typename } }"})
			});
			return {status: response.status, body: await response.json()};
		}`,
		maxResultBytes: 4096,
		allowedOrigins: []string{server.URL},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	browser, err := factory.Open(ctx, session.OpenSpec{
		SessionRef: "s_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProfileDir: profileDir,
		Mode:       session.OpenCommitted,
		StartURL:   server.URL,
		StartupCap: 10 * time.Second,
		CleanupCap: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer browser.Close(context.Background())

	testCases := []struct {
		program session.ProgramID
		path    string
		want    string
	}{
		{
			program: "fixture.bff",
			path:    "/bff/private",
			want:    `{"status":200,"body":{"ok":true,"kind":"bff"}}`,
		},
		{
			program: "fixture.graphql",
			path:    "/graphql",
			want:    `{"status":200,"body":{"data":{"viewer":{"__typename":"SyntheticViewer"}}}}`,
		},
	}
	for _, testCase := range testCases {
		var result json.RawMessage
		args, _ := json.Marshal(map[string]string{"url": server.URL + testCase.path})
		if err := browser.Executor().RunJSON(
			ctx,
			testCase.program,
			args,
			&result,
		); err != nil {
			t.Fatalf("RunJSON(%q) error = %v", testCase.program, err)
		}
		if string(result) != testCase.want {
			t.Errorf("result for %q = %s", testCase.program, result)
		}
		if strings.Contains(string(result), cookieValue) ||
			strings.Contains(string(result), "fixture_session") {
			t.Fatalf("result exposed browser cookie: %s", result)
		}
	}
}
