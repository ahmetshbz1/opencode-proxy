package provider

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/middleware"
)

type AnthropicProvider struct {
	name     string
	priority int
	baseURL  string
	apiKey   string
	client   *http.Client
	logger   *slog.Logger
}

func NewAnthropic(cfg config.Provider, client *http.Client, logger *slog.Logger) *AnthropicProvider {
	return &AnthropicProvider{
		name:     cfg.Name,
		priority: cfg.Priority,
		baseURL:  cfg.BaseURL,
		apiKey:   cfg.APIKey,
		client:   client,
		logger:   logger,
	}
}

func (p *AnthropicProvider) Name() string     { return p.name }
func (p *AnthropicProvider) Priority() int    { return p.priority }

func (p *AnthropicProvider) Proxy(ctx context.Context, w http.ResponseWriter, body []byte, _ anthropic.Request) error {
	endpoint := resolveEndpoint(p.baseURL)
	reqID := middleware.GetRequestID(ctx)

	proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("x-api-key", p.apiKey)
	proxyReq.Header.Set("anthropic-version", "2023-06-01")

	p.logger.Debug("anthropic istek gönderiliyor",
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
		p.logger.Warn("anthropic sağlayıcı başarısız",
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

func resolveEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1/messages") {
		baseURL += "/v1/messages"
	}
	return baseURL
}

func isRetryable(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		status == http.StatusPaymentRequired ||
		status >= 500
}
