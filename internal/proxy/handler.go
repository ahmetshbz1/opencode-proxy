package proxy

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/provider"
	"opencode-proxy/internal/sse"
)

func HandleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		sse.WriteError(w, http.StatusInternalServerError, "isteğin okunması başarısız: "+err.Error())
		return
	}

	var antReq anthropic.Request
	if err := json.Unmarshal(body, &antReq); err != nil {
		sse.WriteError(w, http.StatusBadRequest, "geçersiz istek: "+err.Error())
		return
	}

	// Header'dan gelen key'i yoksay — config.json'daki key'leri kullan
	providers := provider.Ordered()
	lastErr := ""

	for _, p := range providers {
		log.Printf("dene: %s (type=%s, priority=%d)", p.Name, p.Type, p.Priority)

		switch p.Type {
		case provider.Anthropic:
			cfg := config.Provider{
				Name: p.Name, Type: string(p.Type),
				BaseURL: p.BaseURL, APIKey: p.APIKey, Priority: p.Priority,
			}
			ok, errStr := tryAnthropicPassthrough(w, r, body, cfg, antReq)
			if ok {
				return
			}
			lastErr = errStr
			log.Printf("[FAIL] %s başarısız: %s → sonrakine geçiliyor", p.Name, errStr)

		case provider.OpenAI:
			cfg := config.Provider{
				Name: p.Name, Type: string(p.Type),
				BaseURL: p.BaseURL, APIKey: p.APIKey, Priority: p.Priority,
			}
			ok, errStr := tryOpenAIProxy(w, r, cfg, antReq)
			if ok {
				return
			}
			lastErr = errStr
			log.Printf("[FAIL] %s başarısız: %s → sonrakine geçiliyor", p.Name, errStr)
		}
	}

	sse.WriteError(w, http.StatusBadGateway, "tüm sağlayıcılar başarısız oldu: "+lastErr)
}
