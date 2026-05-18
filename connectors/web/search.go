package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const ddgEndpoint = "https://html.duckduckgo.com/html/"

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ddgSearch posts the query to DuckDuckGo's HTML endpoint and parses out
// title/url/snippet for up to maxResults rows. The HTML variant is used
// because it does not require JS — the lite/JSON APIs need a per-IP
// token negotiation we'd rather avoid in v1.
func ddgSearch(ctx context.Context, s *session, query string, maxResults int, userAgent string, timeout time.Duration) ([]searchResult, error) {
	transport := &stdhttp.Transport{Proxy: stdhttp.ProxyFromEnvironment}
	if p := s.cred.Values.String(fProxyURL); p != "" {
		u, err := url.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("proxy_url: %w", err)
		}
		transport.Proxy = stdhttp.ProxyURL(u)
	}
	client := &stdhttp.Client{Timeout: timeout, Transport: transport}

	form := url.Values{}
	form.Set("q", query)
	req, err := stdhttp.NewRequestWithContext(ctx, "POST", ddgEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://html.duckduckgo.com")
	req.Header.Set("Referer", "https://html.duckduckgo.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 202 || resp.StatusCode == 429:
		return nil, errors.New("ddg: rate-limited (status " + statusStr(resp.StatusCode) + ") — pause and retry")
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("ddg: http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	var results []searchResult
	doc.Find("div.result, .web-result, .results_links_deep").EachWithBreak(func(_ int, sel *goquery.Selection) bool {
		if len(results) >= maxResults {
			return false
		}
		titleSel := sel.Find("a.result__a, h2 a.result__a").First()
		title := strings.TrimSpace(titleSel.Text())
		href, _ := titleSel.Attr("href")
		href = unwrapDDGRedirect(href)
		snippet := strings.TrimSpace(sel.Find(".result__snippet, .snippet").First().Text())
		if title == "" || href == "" {
			return true
		}
		results = append(results, searchResult{Title: title, URL: href, Snippet: snippet})
		return true
	})

	if len(results) == 0 {
		return nil, errors.New("ddg: no results parsed (selectors may need updating)")
	}
	return results, nil
}

// unwrapDDGRedirect turns DDG's tracking redirect (//duckduckgo.com/l/?uddg=…)
// into the real destination URL. Leaves anything else untouched.
func unwrapDDGRedirect(href string) string {
	if href == "" {
		return href
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if !strings.Contains(u.Host, "duckduckgo.com") {
		return href
	}
	if uddg := u.Query().Get("uddg"); uddg != "" {
		if decoded, err := url.QueryUnescape(uddg); err == nil {
			return decoded
		}
	}
	return href
}

func statusStr(n int) string {
	switch n {
	case 202:
		return "202"
	case 429:
		return "429"
	default:
		return fmt.Sprintf("%d", n)
	}
}
