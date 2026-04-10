package proxy

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
)

// tryAnthropicPassthrough, z.ai gibi doğrudan Anthropic protocol destekleyen
// sağlayıcılara isteği çevirmeden iletir. Başarısız olursa failover için hata döner.
func tryAnthropicPassthrough(w http.ResponseWriter, r *http.Request, body []byte, p config.Provider, antReq anthropic.Request) (bool, string) {
	endpoint := resolveEndpoint(p.BaseURL)

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return false, err.Error()
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("x-api-key", p.APIKey)
	proxyReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Printf("[OK] %s başarılı (anthropic passthrough)", p.Name)
		for k, vv := range resp.Header {
			for _, v := range vv {
				if !strings.EqualFold(k, "content-length") {
					w.Header().Add(k, v)
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		io.Copy(w, resp.Body)
		return true, ""
	}

	errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	log.Printf("[FAIL] %s başarısız: %s → sonrakine geçiliyor", p.Name, errMsg)
	return false, errMsg
}

// resolveEndpoint, base URL'yi /v1/messages ile tamamlar.
func resolveEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1/messages") {
		baseURL += "/v1/messages"
	}
	return baseURL
}
