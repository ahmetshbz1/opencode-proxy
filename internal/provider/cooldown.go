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
	rrIndex      map[string]uint64    // model → round-robin sayaç
}

func NewActiveTracker() *ActiveTracker {
	return &ActiveTracker{
		exhausted: make(map[string]time.Time),
		rrIndex:   make(map[string]uint64),
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

// NextRRIndex, model için round-robin sayacını artırıp bir sonraki başlangıç indeksini döner.
// Her çağrıda sayaç artar, böylece istekler provider'lar arasında dağıtılır.
func (t *ActiveTracker) NextRRIndex(model string, providerCount int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	idx := t.rrIndex[model]
	t.rrIndex[model] = idx + 1
	return int(idx % uint64(providerCount))
}

// SetRRIndex, round-robin sayacını belirli bir değere set eder (başarılı istekten sonra).
func (t *ActiveTracker) SetRRIndex(model string, idx uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rrIndex[model] = idx
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
