package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"opencode-proxy/internal/auth"
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/middleware"
	"opencode-proxy/internal/provider"
	"opencode-proxy/internal/proxy"
)

var runAuthCommand = func(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configPath := fs.String("config", "config.json", "yapılandırma dosyası yolu")
	name := fs.String("name", "", "sağlayıcı adı")
	baseURL := fs.String("base-url", "https://chatgpt.com/backend-api/codex", "Codex base URL")
	noBrowser := fs.Bool("no-browser", false, "tarayıcı açma")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return auth.RunCodexAuth(ctx, auth.CodexAuthOptions{
		ConfigPath: *configPath,
		Name:       *name,
		BaseURL:    *baseURL,
		NoBrowser:  *noBrowser,
		HTTPClient: provider.DefaultHTTPClient(),
		Stdout:     os.Stdout,
	})
}

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		switch args[1] {
		case "auth":
			if err := runAuthCommand(ctx, args[2:]); err != nil {
				fmt.Fprintln(stderr, err.Error())
				return 1
			}
			return 0
		}
	}

	configFile, err := parseRunFlags(args)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	logger := newLogger(stdout)

	mgr, err := config.NewManager(configFile, logger)
	if err != nil {
		logger.Error("config yüklenemedi", slog.String("error", err.Error()))
		return 1
	}

	httpClient := provider.DefaultHTTPClient()
	registry := provider.NewRegistry(httpClient, logger)
	registry.SetOAuthPersister(mgr.UpdateProviderOAuth)
	registry.RebuildFromConfig(mgr.Get().Providers)

	mgr.OnChange(func(cfg *config.Config) {
		registry.RebuildFromConfig(cfg.Providers)
		logger.Info("sağlayıcılar güncellendi",
			slog.Int("count", len(cfg.Providers)),
		)
	})

	go mgr.Watch()

	cfg := mgr.Get()
	logger.Info("anthropic proxy başlatılıyor",
		slog.Int("port", cfg.Port),
		slog.Int("providers", len(cfg.Providers)),
	)
	for _, p := range cfg.Providers {
		logger.Info("sağlayıcı",
			slog.String("name", p.Name),
			slog.String("type", p.Type),
			slog.Int("priority", p.Priority),
		)
	}

	mux := http.NewServeMux()
	mux.Handle("POST /v1/messages", proxy.NewHandler(registry, logger))
	mux.Handle("POST /v1/messages/", proxy.NewHandler(registry, logger))
	mux.Handle("GET /health", proxy.NewHealthHandler(mgr))

	handler := middleware.Chain(mux,
		middleware.Recovery(logger),
		middleware.RequestID,
		middleware.Logging(logger),
	)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       310 * time.Second,
		WriteTimeout:      310 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("sunucu hatası", slog.String("error", err.Error()))
		}
	}()

	<-done
	logger.Info("kapatma sinyali alındı, düzgün kapatma başlatılıyor...")

	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	mgr.Close()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("düzgün kapatma başarısız", slog.String("error", err.Error()))
		return 1
	}

	logger.Info("sunucu düzgün şekilde kapatıldı")
	return 0
}

func parseRunFlags(args []string) (string, error) {
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "config.json", "yapılandırma dosyası yolu")
	if err := fs.Parse(args[1:]); err != nil {
		return "", err
	}
	return *configPath, nil
}

func newLogger(w io.Writer) *slog.Logger {
	if file, ok := w.(*os.File); ok && isTerminal(file) {
		return slog.New(tint.NewHandler(w, &tint.Options{
			Level:      slog.LevelInfo,
			TimeFormat: time.Kitchen,
		}))
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return (info.Mode() & os.ModeCharDevice) != 0
}
