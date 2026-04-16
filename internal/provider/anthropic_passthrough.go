package provider

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/middleware"
)

// Context key for API key
type apiKeyContextKey struct{}

// WithAPIKey adds the API key to the context
func WithAPIKey(ctx context.Context, apiKey string) context.Context {
	return context.WithValue(ctx, apiKeyContextKey{}, apiKey)
}

// GetAPIKey retrieves the API key from the context
func GetAPIKey(ctx context.Context) string {
	if apiKey, ok := ctx.Value(apiKeyContextKey{}).(string); ok {
		return apiKey
	}
	return ""
}

type AnthropicPassthroughProvider struct {
	name     string
	priority int
	baseURL  string
	client   *http.Client
	logger   *slog.Logger
}

func NewAnthropicPassthrough(cfg config.Provider, client *http.Client, logger *slog.Logger) *AnthropicPassthroughProvider {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicPassthroughProvider{
		name:     cfg.Name,
		priority: cfg.Priority,
		baseURL:  baseURL,
		client:   client,
		logger:   logger,
	}
}

func (p *AnthropicPassthroughProvider) Name() string  { return p.name }
func (p *AnthropicPassthroughProvider) Priority() int { return p.priority }

func (p *AnthropicPassthroughProvider) Proxy(ctx context.Context, w http.ResponseWriter, body []byte, req anthropic.Request) error {
	endpoint := resolveEndpoint(p.baseURL)
	reqID := middleware.GetRequestID(ctx)

	// Get API key from context (passed from original request)
	apiKey := GetAPIKey(ctx)
	if apiKey == "" {
		return &ProxyError{ProviderName: p.name, Message: "API key not found in request", Retryable: false}
	}

	// Filter out context_management field which is not supported in subscription API
	// Also add max_tokens if missing (required by consumer API)
	var reqMap map[string]interface{}
	if err := json.Unmarshal(body, &reqMap); err == nil {
		delete(reqMap, "context_management")
		// Consumer API requires max_tokens even for count_tokens
		if _, hasMaxTokens := reqMap["max_tokens"]; !hasMaxTokens {
			reqMap["max_tokens"] = 1
		}
		if filteredBody, err := json.Marshal(reqMap); err == nil {
			body = filteredBody
		}
	}

	proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("x-api-key", apiKey)
	proxyReq.Header.Set("anthropic-version", "2023-06-01")

	p.logger.Debug("anthropic passthrough istek gönderiliyor",
		slog.String("provider", p.name),
		slog.String("endpoint", endpoint),
		slog.String("request_id", reqID),
	)

	resp, err := p.client.Do(proxyReq)
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		p.logger.Warn("anthropic passthrough sağlayıcı başarısız",
			slog.String("provider", p.name),
			slog.Int("status", resp.StatusCode),
			slog.String("body", string(respBody)),
			slog.String("request_id", reqID),
		)
		retryable := isRetryable(resp.StatusCode)
		return &ProxyError{
			ProviderName: p.name,
			StatusCode:   resp.StatusCode,
			Message:      string(respBody),
			Retryable:    retryable,
		}
	}

	for k, vv := range resp.Header {
		for _, v := range vv {
			if !strings.EqualFold(k, "content-length") {
				w.Header().Add(k, v)
			}
		}
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)

	p.logger.Info("anthropic passthrough başarılı",
		slog.String("provider", p.name),
		slog.String("request_id", reqID),
	)
	return nil
}
