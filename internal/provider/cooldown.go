package provider

import (
	"strings"
	"sync"
	"time"
)

const (
	exhaustedResetInterval = 5 * time.Minute
)

type ActiveTracker struct {
	mu           sync.RWMutex
	exhausted    map[string]time.Time // provider adı → ne zaman tükenildi
	lastActive   map[string]string    // model → son başarılı provider adı
}

func NewActiveTracker() *ActiveTracker {
	return &ActiveTracker{
		exhausted:  make(map[string]time.Time),
		lastActive: make(map[string]string),
	}
}

func (t *ActiveTracker) IsExhausted(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	expiry, exists := t.exhausted[name]
	if !exists {
		return false
	}
	// Süresi dolmuşsa artık tüketilmiş sayılmaz
	if time.Now().After(expiry) {
		return false
	}
	return true
}

func (t *ActiveTracker) MarkExhausted(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exhausted[name] = time.Now().Add(exhaustedResetInterval)
}

func (t *ActiveTracker) ClearExhausted(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.exhausted, name)
}

func (t *ActiveTracker) ResetAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.exhausted = make(map[string]time.Time)
}

// SetLastActive, bir model için son başarılı provider'ı kaydeder.
func (t *ActiveTracker) SetLastActive(model, providerName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastActive[model] = providerName
}

// LastActiveForModel, bir model için son başarılı provider adını döner.
// Yoksa boş string döner.
func (t *ActiveTracker) LastActiveForModel(model string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastActive[model]
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
	// HTTP durum kodları: 429 (rate limit), 402 (payment required)
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
