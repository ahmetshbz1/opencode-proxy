package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"opencode-proxy/internal/config"
	"opencode-proxy/internal/mcp"
	"opencode-proxy/internal/middleware"
	"opencode-proxy/internal/provider"
	"opencode-proxy/internal/proxy"
	"opencode-proxy/internal/webtools"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCP()
		return
	}

	configFile := flag.String("config", "config.json", "yapılandırma dosyası yolu")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	mgr, err := config.NewManager(*configFile, logger)
	if err != nil {
		logger.Error("config yüklenemedi", slog.String("error", err.Error()))
		os.Exit(1)
	}

	httpClient := provider.DefaultHTTPClient()
	registry := provider.NewRegistry(httpClient, logger)
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

	fetcher := webtools.NewFetcher(logger)
	searcher := webtools.NewSearcher(logger)
	mux.Handle("/v1/tools/web_fetch", webtools.HandleFetch(fetcher, logger))
	mux.Handle("/v1/tools/web_fetch/", webtools.HandleFetch(fetcher, logger))
	mux.Handle("/v1/tools/web_search", webtools.HandleSearch(searcher, logger))
	mux.Handle("/v1/tools/web_search/", webtools.HandleSearch(searcher, logger))

	handler := middleware.Chain(mux,
		middleware.Recovery(logger),
		middleware.Logging(logger),
		middleware.RequestID,
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
			os.Exit(1)
		}
	}()

	<-done
	logger.Info("kapatma sinyali alındı, düzgün kapatma başlatılıyor...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mgr.Close()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("düzgün kapatma başarısız", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("sunucu düzgün şekilde kapatıldı")
}

func runMCP() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	fetcher := webtools.NewFetcher(logger)
	searcher := webtools.NewSearcher(logger)

	srv := mcp.NewServer(fetcher, searcher, logger)
	if err := srv.Run(); err != nil {
		logger.Error("MCP server hatası", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
