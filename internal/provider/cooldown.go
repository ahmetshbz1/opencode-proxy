package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultExhaustedResetInterval = 6 * time.Hour

type quotaErrorBody struct {
	Error quotaErrorDetails `json:"error"`
}

type quotaErrorDetails struct {
	ResetsAt        int64 `json:"resets_at"`
	ResetsInSeconds int64 `json:"resets_in_seconds"`
}

type trackerState struct {
	Exhausted map[string]string `json:"exhausted"`
	Current   map[string]string `json:"current"`
}

type ActiveTracker struct {
	mu        sync.RWMutex
	exhausted map[string]time.Time // provider adı → ne zaman tükenildi
	current   map[string]string    // model → aktif provider adı
	onChange  func()
}

func NewActiveTracker() *ActiveTracker {
	return &ActiveTracker{
		exhausted: make(map[string]time.Time),
		current:   make(map[string]string),
	}
}

func (t *ActiveTracker) IsExhausted(name string) bool {
	_, exhausted := t.ExhaustedUntil(name, time.Now())
	return exhausted
}

func (t *ActiveTracker) ExhaustedUntil(name string, now time.Time) (time.Time, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	expiry, exists := t.exhausted[name]
	if !exists || now.After(expiry) {
		return time.Time{}, false
	}
	return expiry, true
}

func (t *ActiveTracker) MarkExhausted(name string) {
	t.MarkExhaustedUntil(name, time.Now().Add(defaultExhaustedResetInterval))
}

func (t *ActiveTracker) MarkExhaustedUntil(name string, until time.Time) {
	t.mu.Lock()
	t.exhausted[name] = until
	onChange := t.onChange
	t.mu.Unlock()
	notifyTrackerChange(onChange)
}

func (t *ActiveTracker) ClearExhausted(name string) {
	t.mu.Lock()
	_, existed := t.exhausted[name]
	delete(t.exhausted, name)
	onChange := t.onChange
	t.mu.Unlock()
	if existed {
		notifyTrackerChange(onChange)
	}
}

func (t *ActiveTracker) ResetAll() {
	t.mu.Lock()
	t.exhausted = make(map[string]time.Time)
	onChange := t.onChange
	t.mu.Unlock()
	notifyTrackerChange(onChange)
}

func (t *ActiveTracker) Current(model string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current[model]
}

func (t *ActiveTracker) SetCurrent(model, name string) {
	t.mu.Lock()
	if t.current[model] == name {
		t.mu.Unlock()
		return
	}
	t.current[model] = name
	onChange := t.onChange
	t.mu.Unlock()
	notifyTrackerChange(onChange)
}

func (t *ActiveTracker) ClearCurrent(model, name string) {
	t.mu.Lock()
	if t.current[model] != name {
		t.mu.Unlock()
		return
	}
	delete(t.current, model)
	onChange := t.onChange
	t.mu.Unlock()
	notifyTrackerChange(onChange)
}

func (t *ActiveTracker) Save(path string) error {
	t.mu.RLock()
	state := trackerState{
		Exhausted: make(map[string]string, len(t.exhausted)),
		Current:   make(map[string]string, len(t.current)),
	}
	for name, until := range t.exhausted {
		state.Exhausted[name] = until.Format(time.RFC3339)
	}
	for model, name := range t.current {
		state.Current[model] = name
	}
	t.mu.RUnlock()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (t *ActiveTracker) SetChangeHandler(fn func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.onChange = fn
}

func notifyTrackerChange(fn func()) {
	if fn != nil {
		fn()
	}
}

func (t *ActiveTracker) Load(path string, now time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var state trackerState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exhausted = make(map[string]time.Time, len(state.Exhausted))
	for name, raw := range state.Exhausted {
		until, err := time.Parse(time.RFC3339, raw)
		if err == nil && now.Before(until) {
			t.exhausted[name] = until
		}
	}
	t.current = make(map[string]string, len(state.Current))
	for model, name := range state.Current {
		t.current[model] = name
	}
	return nil
}

// IsQuotaError, hatanın kota/limit tükenmesi olup olmadığını kontrol eder.
func IsQuotaError(err error) bool {
	if err == nil {
		return false
	}
	pe, ok := err.(*ProxyError)
	if !ok {
		return false
	}
	// HTTP durum kodları: 429 (istek limiti), 402 (ödeme gerekli)
	if pe.StatusCode == 429 || pe.StatusCode == 402 {
		return true
	}
	// Response body'de kota tükenmesi mesajı ara
	lower := strings.ToLower(pe.Message)
	return strings.Contains(lower, "quota") ||
		strings.Contains(lower, "limit") ||
		strings.Contains(lower, "exceeded") ||
		strings.Contains(lower, "capacity")
}

func QuotaResetTime(err error, now time.Time) time.Time {
	pe, ok := err.(*ProxyError)
	if !ok {
		return now.Add(defaultExhaustedResetInterval)
	}
	var body quotaErrorBody
	if json.Unmarshal([]byte(pe.Message), &body) != nil {
		return now.Add(defaultExhaustedResetInterval)
	}
	if body.Error.ResetsInSeconds > 0 {
		return now.Add(time.Duration(body.Error.ResetsInSeconds) * time.Second)
	}
	if body.Error.ResetsAt > now.Unix() {
		return time.Unix(body.Error.ResetsAt, 0)
	}
	return now.Add(defaultExhaustedResetInterval)
}

func IsCanceledError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	pe, ok := err.(*ProxyError)
	if !ok {
		return false
	}
	return pe.StatusCode == 0 && strings.Contains(strings.ToLower(pe.Message), "context canceled")
}

func (t *ActiveTracker) ApplyUsageLimit(name string, usage *UsageSnapshot, now time.Time) bool {
	if usage == nil || usage.Allowed && !usage.LimitReached {
		return false
	}
	until := now.Add(defaultExhaustedResetInterval)
	if usage.PrimaryWindow != nil && usage.PrimaryWindow.ResetAt > now.Unix() {
		until = time.Unix(usage.PrimaryWindow.ResetAt, 0)
	}
	if usage.SecondaryWindow != nil && usage.SecondaryWindow.ResetAt > now.Unix() && time.Unix(usage.SecondaryWindow.ResetAt, 0).After(until) {
		until = time.Unix(usage.SecondaryWindow.ResetAt, 0)
	}
	t.MarkExhaustedUntil(name, until)
	return true
}

func IsRetryableError(err error) bool {
	if err == nil || IsQuotaError(err) || IsCanceledError(err) {
		return false
	}
	pe, ok := err.(*ProxyError)
	return ok && pe.Retryable
}
