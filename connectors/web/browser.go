package web

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// fetchViaCDP navigates to the URL using a remote Chrome over the DevTools
// Protocol, optionally waits for a selector, and returns the rendered
// outerHTML. The CDP endpoint must already exist — pgf never spawns a
// browser.
func fetchViaCDP(ctx context.Context, s *session, target, waitFor string, timeout time.Duration, userAgent string) (*cacheEntry, error) {
	endpoint := s.cred.Values.String(fCDPEndpoint)
	if endpoint == "" {
		return nil, errors.New("js=true requires cdp_endpoint set on the credential. Run e.g. `chromium --headless --remote-debugging-port=9222` and `pgf connect web <name>` with cdp_endpoint=ws://localhost:9222")
	}

	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(ctx, endpoint, chromedp.NoModifyURL)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	tCtx, cancelT := context.WithTimeout(browserCtx, timeout)
	defer cancelT()

	var html string
	var finalURL string

	actions := []chromedp.Action{
		chromedp.EmulateViewport(1920, 1080),
		chromedp.Navigate(target),
	}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(waitFor, chromedp.ByQuery))
	}
	actions = append(actions,
		chromedp.Location(&finalURL),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)

	if err := chromedp.Run(tCtx, actions...); err != nil {
		return nil, fmt.Errorf("cdp: %w", err)
	}
	_ = userAgent // CDP-supplied browser carries its own UA; not overridden here.

	return &cacheEntry{
		FetchedAt:   time.Now().UTC(),
		Status:      200, // CDP gives us the rendered DOM but no original status code without a network listener; treat reachable as 200
		ContentType: "text/html",
		FinalURL:    finalURL,
		Body:        html,
		JS:          true,
	}, nil
}

// screenshotViaCDP captures the entire scrollable page (not just the
// initial viewport) and returns the PNG/JPEG bytes. Always re-renders;
// the cache stores page HTML, not pixels.
func screenshotViaCDP(ctx context.Context, s *session, target, waitFor string, timeout time.Duration, userAgent string, quality int) ([]byte, error) {
	endpoint := s.cred.Values.String(fCDPEndpoint)
	if endpoint == "" {
		return nil, errors.New("screenshot requires cdp_endpoint set on the credential")
	}

	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(ctx, endpoint, chromedp.NoModifyURL)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	tCtx, cancelT := context.WithTimeout(browserCtx, timeout)
	defer cancelT()

	var buf []byte
	actions := []chromedp.Action{
		chromedp.EmulateViewport(1920, 1080),
		chromedp.Navigate(target),
	}
	if waitFor != "" {
		actions = append(actions, chromedp.WaitVisible(waitFor, chromedp.ByQuery))
	}
	actions = append(actions, chromedp.FullScreenshot(&buf, quality))

	if err := chromedp.Run(tCtx, actions...); err != nil {
		return nil, fmt.Errorf("cdp screenshot: %w", err)
	}
	_ = userAgent // see fetchViaCDP for the same note
	return buf, nil
}
