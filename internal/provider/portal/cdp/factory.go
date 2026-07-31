package cdp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/adinvadim/reg-ru-cli/internal/provider/portal/session"
	"github.com/chromedp/chromedp"
)

type Config struct {
	BrowserPath string
}

var ErrBrowserNotFound = errors.New("no supported Chrome or Chromium browser was found")

type Factory struct {
	browserPath string
	programs    map[session.ProgramID]program
}

func NewFactory(config Config) *Factory {
	return &Factory{
		browserPath: config.BrowserPath,
		programs:    productionPrograms(),
	}
}

func FindBrowser(configured string) (string, error) {
	if configured != "" {
		if !filepath.IsAbs(configured) {
			return "", errors.New("configured browser path must be absolute")
		}
		return validateBrowser(configured)
	}

	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			filepath.Join(
				os.Getenv("HOME"),
				"Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			),
		}
	case "windows":
		for _, root := range []string{
			os.Getenv("PROGRAMFILES"),
			os.Getenv("PROGRAMFILES(X86)"),
			os.Getenv("LOCALAPPDATA"),
		} {
			if root != "" {
				candidates = append(
					candidates,
					filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
					filepath.Join(root, "Chromium", "Application", "chrome.exe"),
				)
			}
		}
	default:
		for _, name := range []string{
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
		} {
			path, err := exec.LookPath(name)
			if err == nil {
				candidates = append(candidates, path)
			}
		}
	}

	for _, candidate := range candidates {
		if path, err := validateBrowser(candidate); err == nil {
			return path, nil
		}
	}
	return "", ErrBrowserNotFound
}

func validateBrowser(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("browser path is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("browser path is not executable")
	}
	return filepath.Clean(path), nil
}

func (f *Factory) Open(
	ctx context.Context,
	spec session.OpenSpec,
) (session.Browser, error) {
	browserPath, err := FindBrowser(f.browserPath)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(spec.ProfileDir) {
		return nil, errors.New("browser profile path must be absolute")
	}
	if spec.StartupCap <= 0 {
		return nil, errors.New("browser startup deadline is invalid")
	}
	if spec.CleanupCap <= 0 {
		return nil, errors.New("browser cleanup deadline is invalid")
	}

	ownerCtx, ownerCancel := context.WithCancel(context.Background())
	options := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(browserPath),
		chromedp.UserDataDir(spec.ProfileDir),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("no-sandbox", false),
		chromedp.WSURLReadTimeout(spec.StartupCap),
	}
	if spec.Mode == session.OpenCommitted {
		options = append(options, chromedp.Flag("headless", "new"))
	}
	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(ownerCtx, options...)
	browserCtx, browserCancel := chromedp.NewContext(allocatorCtx)

	started := make(chan error, 1)
	go func() {
		started <- chromedp.Run(browserCtx)
	}()
	startupTimer := time.NewTimer(spec.StartupCap)
	defer startupTimer.Stop()
	select {
	case err := <-started:
		if err != nil {
			browserCancel()
			allocatorCancel()
			ownerCancel()
			return nil, errors.New("browser startup failed")
		}
	case <-ctx.Done():
		browserCancel()
		allocatorCancel()
		ownerCancel()
		return nil, ctx.Err()
	case <-startupTimer.C:
		browserCancel()
		allocatorCancel()
		ownerCancel()
		return nil, errors.New("browser startup timed out")
	}

	instance := &browser{
		context:         browserCtx,
		browserCancel:   browserCancel,
		allocatorCancel: allocatorCancel,
		ownerCancel:     ownerCancel,
		cleanupCap:      spec.CleanupCap,
		programs:        clonePrograms(f.programs),
		done:            make(chan struct{}),
	}
	if spec.StartURL != "" {
		callCtx, cancel := instance.callContext(ctx)
		err = chromedp.Run(
			callCtx,
			chromedp.Navigate(spec.StartURL),
			chromedp.WaitReady("body", chromedp.ByQuery),
		)
		cancel()
		if err != nil {
			_ = instance.Close(context.Background())
			return nil, errors.New("browser navigation failed")
		}
	}
	return instance, nil
}

type browser struct {
	context         context.Context
	browserCancel   context.CancelFunc
	allocatorCancel context.CancelFunc
	ownerCancel     context.CancelFunc
	cleanupCap      time.Duration
	programs        map[session.ProgramID]program
	done            chan struct{}
	closeOnce       sync.Once
	closeErr        error
}

func (b *browser) Executor() session.PageExecutor {
	return &pageExecutor{browser: b}
}

func (b *browser) Close(ctx context.Context) error {
	b.closeOnce.Do(func() {
		closeCtx := context.WithoutCancel(b.context)
		var cancel context.CancelFunc
		if deadline, ok := ctx.Deadline(); ok {
			closeCtx, cancel = context.WithDeadline(closeCtx, deadline)
		} else {
			closeCtx, cancel = context.WithTimeout(closeCtx, b.cleanupCap)
		}
		defer cancel()

		closed := make(chan error, 1)
		go func() {
			closed <- chromedp.Cancel(closeCtx)
		}()
		select {
		case b.closeErr = <-closed:
		case <-closeCtx.Done():
			if current := chromedp.FromContext(b.context); current != nil &&
				current.Browser != nil &&
				current.Browser.Process() != nil {
				_ = current.Browser.Process().Kill()
			}
			b.closeErr = errors.New("browser cleanup timed out")
		}
		b.browserCancel()
		b.allocatorCancel()
		b.ownerCancel()
		close(b.done)
	})
	return b.closeErr
}

func (b *browser) callContext(caller context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(b.context)
	if deadline, ok := caller.Deadline(); ok {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithDeadline(ctx, deadline)
		previousCancel := cancel
		cancel = func() {
			deadlineCancel()
			previousCancel()
		}
	}
	stop := context.AfterFunc(caller, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func clonePrograms(input map[session.ProgramID]program) map[session.ProgramID]program {
	cloned := make(map[session.ProgramID]program, len(input))
	for id, item := range input {
		item.allowedOrigins = append([]string(nil), item.allowedOrigins...)
		cloned[id] = item
	}
	return cloned
}
