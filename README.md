# Anthropic Proxy

Anthropic Messages API protokolünü destekleyen istemcileri (Claude Code, Cursor vb.) birden fazla LLM sağlayıcısına bağlayan reverse proxy. z.ai öncelikli çalışır, limit dolduğunda OpenCode Go'ya otomatik geçiş yapar.

## Mimari

```
Claude Code ──► localhost:8787 ──► z.ai (Anthropic passthrough)
                                    │
                                    └──► OpenCode Go (Anthropic→OpenAI çevirisi)
```

- **z.ai** doğrudan passthrough — istek çevirisi yok, sıfır ek yük
- **OpenCode Go** Anthropic→OpenAI format çevirisi ile çalışır
- Öncelik sırasına göre dener, başarısız olursa otomatik sonrakine geçer
- `config.json` dosyasından sıcak yeniden yükleme (restart gerektirmez)
- Graceful shutdown (SIGTERM/SIGINT)
- Structured logging (`slog`)
- Request ID tracing
- Panic recovery middleware

## Hızlı Başlangıç

```bash
# 1. Repoyu klonla
git clone https://github.com/ahmet/opencode-proxy.git
cd opencode-proxy

# 2. Yapılandırma dosyasını oluştur
cp config.example.json config.json
# config.json'ı düzenle, API anahtarlarını gir

# 3. Derle ve çalıştır
make build
./opencode-proxy

# 4. Testleri çalıştır
make test

# 5. Claude Code settings.json'ı güncelle
```

Claude Code `settings.json`:

```json
{
  "env": {
    "ANTHROPIC_API_KEY": "sk-proxy",
    "ANTHROPIC_BASE_URL": "http://localhost:8787",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.1",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.1",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.1"
  }
}
```

`ANTHROPIC_API_KEY` değeri önemli değil — proxy kendi `config.json`'ındaki anahtarları kullanır.

## Yapılandırma

`config.json` formatı:

```json
{
  "port": 8787,
  "providers": [
    {
      "name": "sağlayıcı adı",
      "type": "anthropic | openai",
      "base_url": "API endpoint URL",
      "api_key": "API anahtarı",
      "priority": 1
    }
  ]
}
```

| Alan | Açıklama |
|------|-----------|
| `port` | Proxy dinleme portu (varsayılan: 8787) |
| `name` | Sağlayıcı adı (loglarda görünür) |
| `type` | `anthropic` = doğrudan passthrough, `openai` = format çevirisi |
| `base_url` | Sağlayıcı API endpoint'i |
| `api_key` | API anahtarı |
| `priority` | 1 = en öncelikli, düşük sayı = önce denenir |

### Sağlayıcı Tipleri

**`anthropic`** — İsteği doğrudan iletir, çeviri yok. z.ai, Anthropic API gibi uyumlu endpoint'ler için.

**`openai`** — İsteği Anthropic Messages API'den OpenAI Chat Completions API'ye çevirir. OpenCode Go gibi OpenAI-uyumlu endpoint'ler için. Streaming dahil tam destek.

### Sıcak Yeniden Yükleme

`config.json` değiştirildiğinde proxy otomatik olarak yeniden yükler. Restart gerekmez. Sağlayıcı ekle/çıkar, öncelik değiştir — hepsi canlı güncellenir.

## Web Araçları

Proxy, dahili web araçları sunar. İnternet araştırması için kullanılabilir.

### Web Fetch

Bir URL'nin içeriğini çeker ve metin olarak döner.

```bash
# GET ile
curl "http://localhost:8787/v1/tools/web_fetch?url=https://example.com"

# POST ile
curl -X POST http://localhost:8787/v1/tools/web_fetch \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com"}'
```

Yanıt:

```json
{
  "url": "https://example.com",
  "title": "Example Domain",
  "content": "This domain is for use in illustrative examples..."
}
```

### Web Search

DuckDuckGo üzerinden web araması yapar.

```bash
# GET ile
curl "http://localhost:8787/v1/tools/web_search?q=go+programming"

# POST ile
curl -X POST http://localhost:8787/v1/tools/web_search \
  -H "Content-Type: application/json" \
  -d '{"query": "go programming best practices"}'
```

Yanıt:

```json
{
  "query": "go programming",
  "results": [
    {"title": "Go Programming Language", "url": "https://go.dev/", "snippet": ""},
    {"title": "A Tour of Go", "url": "https://go.dev/tour/", "snippet": ""}
  ]
}
```

## MCP Server

Proxy, Claude Code ile stdio üzerinden entegre çalışan bir MCP (Model Context Protocol) sunucusu da içerir. Claude Code'un yerleşik web araçlarını kullanamadığınız durumlarda `web_search` ve `web_fetch` araçlarını MCP üzerinden sağlar.

### Claude Code Yapılandırması

`~/.claude/settings.json` dosyasına MCP server olarak ekleyin:

```json
{
  "mcpServers": {
    "opencode-proxy": {
      "command": "/tam/yol/opencode-proxy",
      "args": ["mcp"]
    }
  }
}
```

### Kullanılabilir Araçlar

| Araç | Açıklama |
|------|-----------|
| `web_search` | DuckDuckGo üzerinden web araması |
| `web_fetch` | URL içeriğini çeker ve metin olarak döner |

## Sağlayıcı Failover Davranışı

Bir sağlayıcı aşağıdaki HTTP kodlarını dönerse sonrakine geçilir:

- `401` Unauthorized
- `402` Payment Required
- `403` Forbidden
- `429` Rate Limit
- `5xx` Sunucu Hatası

Tüm sağlayıcılar başarısız olursa `502 Bad Gateway` döner.

## Sağlık Kontrolü

```bash
curl http://localhost:8787/health
```

```json
{
  "status": "ok",
  "providers": [
    {"name": "z.ai", "type": "anthropic", "priority": "1"},
    {"name": "opencode-go", "type": "openai", "priority": "2"}
  ]
}
```

## Proje Yapısı

```
opencode-proxy/
├── main.go                              # Giriş noktası, graceful shutdown, DI
├── config.json                          # Yapılandırma (.gitignore)
├── config.example.json                  # Şablon yapılandırma
├── internal/
│   ├── config/config.go                 # Config yükleme, doğrulama, sıcak yeniden yükleme
│   ├── middleware/
│   │   ├── chain.go                     # Middleware zinciri
│   │   ├── logging.go                   # Structured request logging (slog)
│   │   ├── requestid.go                 # İstek ID üretimi (crypto/rand)
│   │   └── recovery.go                  # Panic kurtarma
│   ├── provider/
│   │   ├── provider.go                  # Provider interface, ProxyError, Registry
│   │   ├── anthropic.go                 # Anthropic passthrough implementasyonu
│   │   └── openai.go                    # OpenAI proxy + streaming implementasyonu
│   ├── proxy/
│   │   ├── handler.go                   # HTTP handler + failover mantığı
│   │   └── health.go                    # Sağlık kontrolü endpoint'i
│   ├── convert/convert.go              # Anthropic → OpenAI dönüşüm
│   ├── anthropic/types.go              # Anthropic API tipleri
│   ├── openai/types.go                 # OpenAI API tipleri
│   ├── sse/sse.go                      # SSE yardımcıları
│   └── webtools/
│       ├── fetch.go                     # Web sayfası çekme
│       └── search.go                    # Web arama (DuckDuckGo)
│   └── mcp/
│       ├── server.go                    # MCP stdio server (JSON-RPC)
│       └── types.go                     # MCP protokol tipleri
```

## Make Komutları

| Komut | Açıklama |
|-------|-----------|
| `make build` | Derle |
| `make run` | Çalıştır |
| `make test` | Testleri çalıştır (race detector dahil) |
| `make vet` | Statik analiz |
| `make lint` | vet + test |
| `make clean` | Derleme çıktısını temizle |

## Lisans

MIT
