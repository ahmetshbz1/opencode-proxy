package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() {
	f.flushed = true
	f.ResponseRecorder.Flush()
}

func TestChain(t *testing.T) {
	order := []string{}

	a := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "a")
			next.ServeHTTP(w, r)
		})
	}
	b := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "b")
			next.ServeHTTP(w, r)
		})
	}
	final := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "final")
	})

	handler := Chain(final, a, b)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if len(order) != 3 {
		t.Fatalf("çağrı sayısı = %d, want 3", len(order))
	}
	if order[0] != "a" || order[1] != "b" || order[2] != "final" {
		t.Errorf("çağrı sırası = %v, want [a b final]", order)
	}
}

func TestRequestID(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		id := GetRequestID(r.Context())
		if id == "" {
			t.Error("request ID boş")
		}
		if len(id) != 16 {
			t.Errorf("request ID uzunluğu = %d, want 16", len(id))
		}
	})

	handler := RequestID(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler çağrılmadı")
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("response header'da X-Request-ID yok")
	}
}

func TestRequestIDPassthrough(t *testing.T) {
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		id := GetRequestID(r.Context())
		if id != "existing-id" {
			t.Errorf("request ID = %q, want %q", id, "existing-id")
		}
	})

	handler := RequestID(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "existing-id")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
}

func TestLogging(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	})

	handler := Logging(logger)(next)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler çağrılmadı")
	}
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
}

func TestLoggingIgnoresDuplicateWriteHeader(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	duplicate := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.WriteHeader(http.StatusOK)
	})

	handler := Logging(logger)(duplicate)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestLoggingPreservesFlusher(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("wrapped writer http.Flusher değil")
		}
		flusher.Flush()
	})

	handler := Logging(logger)(next)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(w, req)

	if !w.flushed {
		t.Fatal("flush çağrılmadı")
	}
}

func TestRecovery(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})

	handler := Recovery(logger)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(w.Body.String(), "iç sunucu hatası") {
		t.Errorf("body = %q, hata mesajı içermiyor", w.Body.String())
	}
}
