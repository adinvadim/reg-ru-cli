package cdp

import (
	"context"
	"testing"
)

func TestPublicBrowserStaysOnWWWRegRu(t *testing.T) {
	browser := NewPublicBrowser(Config{BrowserPath: "/nonexistent/chrome"})
	for _, start := range []string{"https://evil.example/", "http://www.reg.ru/", "https://www.reg.ru.evil.example/"} {
		if _, err := browser.Open(context.Background(), start); err == nil {
			t.Errorf("Open(%q) succeeded", start)
		}
	}
	page := &PublicPage{}
	for _, path := range []string{"https://evil.example/x", "//evil.example/x", "relative"} {
		if _, err := page.Get(context.Background(), path); err == nil {
			t.Errorf("Get(%q) succeeded", path)
		}
	}
	programs := publicPrograms()
	for id, program := range programs {
		if len(program.allowedOrigins) != 1 || program.allowedOrigins[0] != PublicOrigin {
			t.Errorf("program %s origins = %v", id, program.allowedOrigins)
		}
	}
	if _, exposed := productionPrograms()[programPublicFetch]; exposed {
		t.Error("the public fetch program is available to account portal sessions")
	}
}
