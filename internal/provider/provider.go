package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
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
	mu           sync.RWMutex
	providers    []providerEntry
	client       *http.Client
	logger       *slog.Logger
	persistOAuth func(name string, oauth config.OAuthConfig) error
	active       *ActiveTracker
}

type providerEntry struct {
	provider Provider
	models   []string
	typ      string
}

func NewRegistry(client *http.Client, logger *slog.Logger) *Registry {
	return &Registry{
		client: client,
		logger: logger,
		active: NewActiveTracker(),
	}
}

func (r *Registry) Active() *ActiveTracker {
	return r.active
}

func (r *Registry) RebuildFromConfig(cfgs []config.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers = make([]providerEntry, 0, len(cfgs))
	for _, c := range cfgs {
		var built Provider
		switch c.Type {
		case "anthropic":
			built = NewAnthropic(c, r.client, r.logger)
		case "openai":
			built = NewOpenAI(c, r.client, r.logger)
		case "codex":
			if r.persistOAuth != nil {
				providerName := c.Name
				c.PersistOAuth = func(oauth config.OAuthConfig) error {
					return r.persistOAuth(providerName, oauth)
				}
			}
			built = NewCodex(c, r.client, r.logger)
		case "anthropic_passthrough":
			built = NewAnthropicPassthrough(c, r.client, r.logger)
		}
		if built != nil {
			r.providers = append(r.providers, providerEntry{
				provider: built,
				models:   c.Models,
				typ:      c.Type,
			})
		}
	}
}

func (r *Registry) Ordered() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.providers))
	for _, entry := range r.providers {
		out = append(out, entry.provider)
	}
	return out
}

func (r *Registry) OrderedForModel(model string) []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	trimmedModel := strings.TrimSpace(model)
	explicit := make([]Provider, 0, len(r.providers))
	catchAll := make([]Provider, 0, len(r.providers))
	for _, entry := range r.providers {
		if entry.isCatchAll() {
			catchAll = append(catchAll, entry.provider)
			continue
		}
		if entry.matchesModel(trimmedModel) {
			explicit = append(explicit, entry.provider)
		}
	}
	if len(explicit) > 0 {
		return explicit
	}
	return catchAll
}

func (r *Registry) SetOAuthPersister(fn func(name string, oauth config.OAuthConfig) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.persistOAuth = fn
}

// TypeFor, bir provider'ın config'teki type değerini döndürür (küme adı).
// Bilinmeyen provider için boş string döner.
func (r *Registry) TypeFor(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.providers {
		if entry.provider.Name() == name {
			return entry.typ
		}
	}
	return ""
}

func (e providerEntry) matchesModel(model string) bool {
	if len(e.models) == 0 || model == "" {
		return true
	}
	for _, pattern := range e.models {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, err := path.Match(pattern, model); err == nil && ok {
			return true
		}
		if pattern == model {
			return true
		}
	}
	return false
}

func (e providerEntry) isCatchAll() bool {
	return len(e.models) == 0
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
