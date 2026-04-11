package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
)

type ProxyError struct {
	ProviderName string
	StatusCode   int
	Message      string
	Retryable    bool
}

func (e *ProxyError) Error() string {
	return fmt.Sprintf("provider %s: HTTP %d: %s", e.ProviderName, e.StatusCode, e.Message)
}

type Provider interface {
	Name() string
	Priority() int
	Proxy(ctx context.Context, w http.ResponseWriter, body []byte, req anthropic.Request) error
}

type Registry struct {
	mu        sync.RWMutex
	providers []Provider
	client    *http.Client
	logger    *slog.Logger
}

func NewRegistry(client *http.Client, logger *slog.Logger) *Registry {
	return &Registry{
		client: client,
		logger: logger,
	}
}

func (r *Registry) RebuildFromConfig(cfgs []config.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers = make([]Provider, 0, len(cfgs))
	for _, c := range cfgs {
		switch c.Type {
		case "anthropic":
			r.providers = append(r.providers, NewAnthropic(c, r.client, r.logger))
		case "openai":
			r.providers = append(r.providers, NewOpenAI(c, r.client, r.logger))
		}
	}
	slices.SortFunc(r.providers, func(a, b Provider) int {
		return a.Priority() - b.Priority()
	})
}

func (r *Registry) Ordered() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, len(r.providers))
	copy(out, r.providers)
	return out
}

func DefaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 300 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}
