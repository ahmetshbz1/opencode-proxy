package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"opencode-proxy/internal/config"
	"opencode-proxy/internal/proxy"
)

func main() {
	configFile := flag.String("config", "config.json", "yapılandırma dosyası yolu")
	flag.Parse()

	if err := config.Init(*configFile); err != nil {
		log.Fatalf("config yüklenemedi: %v", err)
	}

	cfg := config.Get()
	log.Printf("🎧 Anthropic proxy başlatılıyor (port :%d)", cfg.Port)
	for _, p := range cfg.Providers {
		log.Printf("  📤 %s (type=%s, priority=%d)", p.Name, p.Type, p.Priority)
	}

	http.HandleFunc("/v1/messages", proxy.HandleMessages)
	http.HandleFunc("/v1/messages/", proxy.HandleMessages)
	http.HandleFunc("/health", proxy.HealthCheck)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), nil); err != nil {
		log.Fatalf("sunucu hatası: %v", err)
	}
}
