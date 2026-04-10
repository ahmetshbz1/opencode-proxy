package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type Config struct {
	Port      int        `json:"port"`
	Providers []Provider `json:"providers"`
}

type Provider struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Priority int    `json:"priority"`
}

var (
	cfg  Config
	mu   sync.RWMutex
	once sync.Once
	done chan struct{}
)

func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config okunamadı: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return json.Unmarshal(data, &cfg)
}

func Get() Config {
	mu.RLock()
	defer mu.RUnlock()
	return cfg
}

func Providers() []Provider {
	return Get().Providers
}

func Watch(path string) {
	done = make(chan struct{})
	lastModified := time.Now()
	for {
		select {
		case <-done:
			return
		default:
		}
		time.Sleep(2 * time.Second)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(lastModified) {
			lastModified = info.ModTime()
			if err := Load(path); err != nil {
				log.Printf("config reload hatası: %v", err)
			} else {
				log.Printf("config yeniden yüklendi")
			}
		}
	}
}

func Stop() {
	if done != nil {
		close(done)
	}
}

func Init(path string) error {
	if err := Load(path); err != nil {
		return err
	}
	go Watch(path)
	return nil
}
