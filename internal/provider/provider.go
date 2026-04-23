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

type UsageProvider interface {
	Usage(ctx context.Context) (*UsageSnapshot, error)
}

type UsageSnapshot struct {
	Email            string       `json:"email,omitempty"`
	PlanType         string       `json:"plan_type,omitempty"`
	Allowed          bool         `json:"allowed"`
	LimitReached     bool         `json:"limit_reached"`
	RateLimitReached string       `json:"rate_limit_reached_type,omitempty"`
	PrimaryWindow    *UsageWindow `json:"primary_window,omitempty"`
	SecondaryWindow  *UsageWindow `json:"secondary_window,omitempty"`
	FetchedAt        string       `json:"fetched_at"`
	CacheAgeSeconds  int64        `json:"cache_age_seconds,omitempty"`
}

type UsageWindow struct {
	UsedPercent        int    `json:"used_percent"`
	LimitWindowSeconds int64  `json:"limit_window_seconds"`
	ResetAfterSeconds  int64  `json:"reset_after_seconds"`
	ResetAt            int64  `json:"reset_at"`
	ResetAtFormatted   string `json:"reset_at_formatted,omitempty"`
}

type Registry struct {
	mu           sync.RWMutex
	providers    []providerEntry
	client       *http.Client
	logger       *slog.Logger
	persistOAuth func(name string, oauth config.OAuthConfig) error
	active       *ActiveTracker
	usageCache   map[string]usageCacheEntry
	usageTTL     time.Duration
}

type usageCacheEntry struct {
	snapshot *UsageSnapshot
	fetched  time.Time
}

type providerEntry struct {
	provider Provider
	models   []string
	typ      string
}

func NewRegistry(client *http.Client, logger *slog.Logger) *Registry {
	return &Registry{
		client:     client,
		logger:     logger,
		active:     NewActiveTracker(),
		usageCache: make(map[string]usageCacheEntry),
		usageTTL:   45 * time.Second,
	}
}

func (r *Registry) Active() *ActiveTracker {
	return r.active
}

func (r *Registry) RebuildFromConfig(cfgs []config.Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers = make([]providerEntry, 0, len(cfgs))
	r.usageCache = make(map[string]usageCacheEntry)
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

func (r *Registry) Usage(ctx context.Context, name string) (*UsageSnapshot, error) {
	now := time.Now()
	if usage := r.cachedUsage(name, now); usage != nil {
		return usage, nil
	}
	usageProvider := r.usageProvider(name)
	if usageProvider == nil {
		return nil, nil
	}
	usage, err := usageProvider.Usage(ctx)
	if err != nil {
		return nil, err
	}
	if usage == nil {
		return nil, nil
	}
	r.storeUsage(name, usage, now)
	return usageWithCacheAge(usage, 0), nil
}

func (r *Registry) cachedUsage(name string, now time.Time) *UsageSnapshot {
	r.mu.RLock()
	entry, ok := r.usageCache[name]
	ttl := r.usageTTL
	r.mu.RUnlock()
	if !ok || now.Sub(entry.fetched) >= ttl {
		return nil
	}
	return usageWithCacheAge(entry.snapshot, int64(now.Sub(entry.fetched).Seconds()))
}

func (r *Registry) usageProvider(name string) UsageProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.providers {
		if entry.provider.Name() != name {
			continue
		}
		usageProvider, ok := entry.provider.(UsageProvider)
		if !ok {
			return nil
		}
		return usageProvider
	}
	return nil
}

func (r *Registry) storeUsage(name string, usage *UsageSnapshot, fetched time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usageCache[name] = usageCacheEntry{snapshot: usageWithCacheAge(usage, 0), fetched: fetched}
}

func usageWithCacheAge(usage *UsageSnapshot, ageSeconds int64) *UsageSnapshot {
	if usage == nil {
		return nil
	}
	clone := *usage
	clone.CacheAgeSeconds = ageSeconds
	return &clone
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
