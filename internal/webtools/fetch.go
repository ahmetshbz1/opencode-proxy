package webtools

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	html_to_markdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html"
)

type FetchResult struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	Bytes       int    `json:"bytes"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type"`
	DurationMs  int64  `json:"duration_ms"`
}

type Fetcher struct {
	client *http.Client
	logger *slog.Logger
	cache  *fetchCache
}

func NewFetcher(logger *slog.Logger) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("çok fazla yönlendirme")
				}
				return nil
			},
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     60 * time.Second,
				DisableCompression:  false,
				MaxIdleConnsPerHost: 5,
			},
		},
		logger: logger,
		cache:  newFetchCache(15 * time.Minute),
	}
}

func (f *Fetcher) Fetch(targetURL string) (*FetchResult, error) {
	return f.FetchWithPrompt(targetURL, "")
}

func (f *Fetcher) FetchWithPrompt(targetURL string, prompt string) (*FetchResult, error) {
	start := time.Now()
	f.logger.Debug("web fetch", slog.String("url", targetURL))

	parsed, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("geçersiz URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("desteklenmeyen protokol: %s", parsed.Scheme)
	}

	if cached, ok := f.cache.Get(targetURL); ok {
		cached.DurationMs = time.Since(start).Milliseconds()
		return cached, nil
	}

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("istek oluşturulamadı: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("istek başarısız: %w", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")

	// Cross-host redirect algılama
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		redirectURL := loc
		if loc != "" {
			if r, e := url.Parse(loc); e == nil && !r.IsAbs() {
				redirectURL = resp.Request.URL.ResolveReference(r).String()
			}
		}
		if redirectURL != "" && isCrossHost(targetURL, redirectURL) {
			return &FetchResult{
				URL:         targetURL,
				StatusCode:  resp.StatusCode,
				ContentType: contentType,
				DurationMs:  time.Since(start).Milliseconds(),
				Content: fmt.Sprintf(
					"YÖNLENDİRME ALGILANDI: Bu URL farklı bir host'a yönlendiriyor.\n\nOrijinal URL: %s\nYönlendirme URL: %s\nDurum: %d %s\n\nİçeriği almak için yeni URL ile tekrar deneyin.",
					targetURL, redirectURL, resp.StatusCode, resp.Status,
				),
			}, nil
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	if !isFetchableContentType(contentType) {
		return nil, fmt.Errorf("desteklenmeyen içerik tipi: %s", contentType)
	}

	const maxBodySize = 5 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("yanıt okunamadı: %w", err)
	}

	durationMs := time.Since(start).Milliseconds()

	title, content := f.convertContent(body, contentType)

	if len(content) > 100000 {
		content = content[:100000] + "\n\n... [içerik kısaltıldı]"
	}

	result := &FetchResult{
		URL:         resp.Request.URL.String(),
		Title:       title,
		Content:     content,
		Bytes:       len(body),
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		DurationMs:  durationMs,
	}

	f.cache.Set(targetURL, result)

	return result, nil
}

func (f *Fetcher) convertContent(body []byte, contentType string) (title string, content string) {
	if strings.Contains(contentType, "text/plain") ||
		strings.Contains(contentType, "application/json") {
		return "", string(body)
	}

	title = extractTitle(body)

	markdown, err := html_to_markdown.ConvertString(string(body))
	if err != nil {
		f.logger.Debug("markdown dönüşümü başarısız, düz metin kullanılıyor", slog.String("error", err.Error()))
		_, fallback := fallbackExtract(body)
		return title, fallback
	}

	markdown = cleanMarkdown(markdown)

	return title, markdown
}

func extractTitle(data []byte) string {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return ""
	}

	var title string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode {
					title = strings.TrimSpace(c.Data)
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return title
}

func cleanMarkdown(md string) string {
	md = strings.ReplaceAll(md, "\r\n", "\n")

	var b strings.Builder
	prevEmpty := false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if !prevEmpty {
				b.WriteByte('\n')
			}
			prevEmpty = true
			continue
		}
		prevEmpty = false
		b.WriteString(line)
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String())
}

func isFetchableContentType(ct string) bool {
	fetchable := []string{
		"text/html",
		"text/plain",
		"text/markdown",
		"text/xml",
		"application/xhtml",
		"application/xml",
		"application/json",
		"application/javascript",
	}
	lower := strings.ToLower(ct)
	for _, f := range fetchable {
		if strings.Contains(lower, f) {
			return true
		}
	}
	return false
}

func isCrossHost(original, redirect string) bool {
	origURL, err1 := url.Parse(original)
	redirectURL, err2 := url.Parse(redirect)
	if err1 != nil || err2 != nil {
		return false
	}
	return origURL.Host != redirectURL.Host
}

func fallbackExtract(data []byte) (title string, content string) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return "", string(data)
	}

	var titleText strings.Builder
	var bodyText strings.Builder
	inTitle := false
	inBody := false
	inScript := false

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				inTitle = true
			case "body":
				inBody = true
			case "script", "style", "nav", "footer", "header":
				inScript = true
			}
		}

		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text == "" {
				goto children
			}
			if inTitle {
				titleText.WriteString(text)
			} else if inBody && !inScript {
				bodyText.WriteString(text)
				bodyText.WriteByte('\n')
			}
		}

	children:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}

		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				inTitle = false
			case "body":
				inBody = false
			case "script", "style", "nav", "footer", "header":
				inScript = false
			case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6", "li":
				if inBody && !inScript {
					bodyText.WriteByte('\n')
				}
			}
		}
	}

	walk(doc)

	title = strings.TrimSpace(titleText.String())
	content = strings.TrimSpace(bodyText.String())

	const maxContent = 50000
	if len(content) > maxContent {
		content = content[:maxContent] + "\n... [içerik kısaltıldı]"
	}

	return title, content
}

type fetchCache struct {
	mu    sync.RWMutex
	items map[string]*cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	result *FetchResult
	expiry time.Time
}

func newFetchCache(ttl time.Duration) *fetchCache {
	c := &fetchCache{
		items: make(map[string]*cacheEntry),
		ttl:   ttl,
	}
	go c.cleanup()
	return c
}

func (c *fetchCache) Get(key string) (*FetchResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.items[key]
	if !ok || time.Now().After(entry.expiry) {
		return nil, false
	}
	return entry.result, true
}

func (c *fetchCache) Set(key string, result *FetchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = &cacheEntry{
		result: result,
		expiry: time.Now().Add(c.ttl),
	}
}

func (c *fetchCache) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.expiry) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}