package main

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

func TestRunDispatchesAuthSubcommand(t *testing.T) {
	called := false
	oldRunAuth := runAuthCommand
	runAuthCommand = func(ctx context.Context, args []string) error {
		called = true
		return nil
	}
	defer func() { runAuthCommand = oldRunAuth }()

	exitCode := run(context.Background(), []string{"opencode-proxy", "auth"}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if !called {
		t.Fatal("auth komutu çağrılmadı")
	}
}

func TestParseRunFlagsCanBeCalledTwiceWithoutFlagPanic(t *testing.T) {
	for i := 0; i < 2; i++ {
		configPath, err := parseRunFlags([]string{"opencode-proxy", "-config", "test-config.json"})
		if err != nil {
			t.Fatalf("çağrı %d için parseRunFlags hatası: %v", i, err)
		}
		if configPath != "test-config.json" {
			t.Fatalf("çağrı %d için configPath = %q, want test-config.json", i, configPath)
		}
	}
}

func TestCountTokensRoutePatternMatchesExpectedPath(t *testing.T) {
	mux := http.NewServeMux()
	called := false
	mux.HandleFunc("POST /v1/messages/count_tokens", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	req, err := http.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	if err != nil {
		t.Fatalf("request oluşturulamadı: %v", err)
	}
	w := &testResponseWriter{}
	mux.ServeHTTP(w, req)

	if !called {
		t.Fatal("count_tokens route eşleşmedi")
	}
	if w.status != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.status, http.StatusNoContent)
	}
}

type testResponseWriter struct {
	header http.Header
	status int
}

func (w *testResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *testResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(b), nil
}

func (w *testResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}
