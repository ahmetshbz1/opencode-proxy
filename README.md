# Anthropic Proxy

Anthropic Messages API protokolünü destekleyen istemcileri (Claude Code, Cursor vb.) birden fazla LLM sağlayıcısına bağlayan gösterge panosu ters vekil (reverse proxy). z.ai öncelikli çalışır, limit dolduğunda OpenCode Go'ya otomatik geçiş yapar.

## Ne Yapar?

```
Claude Code ──► localhost:8787 ──► z.ai (Anthropic passthrough)
                                    │
                                    └──► OpenCode Go (Anthropic→OpenAI çevirisi)
```

- **z.ai** doğrudan passthrough — istek çevirisi yok, sıfır ek yük
- **OpenCode Go** Anthropic→OpenAI format çevirisi ile çalışır
- Öncelik sırasına göre dener, başarısız olursa otomatik sonrakine geçer
- `config.json` dosyasından sıcak yeniden yükleme (restart gerektirmez)

## Hızlı Başlangıç

```bash
# 1. Repoyu klonla
git clone https://github.com/ahmet/opencode-proxy.git
cd opencode-proxy

# 2. Yapılandırma dosyasını oluştur
cp config.example.json config.json
# config.json'ı düzenle, API anahtarlarını gir

# 3. Derle ve çalıştır
go build -o opencode-proxy .
./opencode-proxy

# 4. Claude Code settings.json'ı güncelle
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

`ANTHROPIC_API_KEY` değeri önemli değil — proxy kendi `config.json`'ındaki anahtarları kullanır. Claude Code sadece bir değer olmasını bekliyor.

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

`config.json` değiştirildiğinde proxy otomatik olarak yeniden yükler. Restart gerekmez.

```bash
# Sağlayıcı ekle/çıkar, priority değiştir — hepsi canlı
```

## Desteklenen Özellikler

- ✅ Streaming (SSE) — tool kullanımı dahil
- ✅ Tool calling (function calling)
- ✅ Sistem mesajları
- ✅ Çoklu sağlayıcı failover
- ✅ Öncelik tabanlı yönlendirme
- ✅ Sıcak yapılandırma yeniden yükleme
- ✅ Anthropic passthrough (sıfır ek yük)
- ✅ OpenAI format çevirisi

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
├── main.go                         # Giriş noktası
├── config.json                     # Yapılandırma (.gitignore)
├── config.example.json             # Şablon yapılandırma
├── internal/
│   ├── config/config.go            # Yapılandırma yükleme + sıcak yeniden yükleme
│   ├── provider/provider.go        # Sağlayıcı sıralama
│   ├── anthropic/types.go          # Anthropic tipleri
│   ├── openai/types.go             # OpenAI tipleri
│   ├── convert/convert.go          # Anthropic → OpenAI dönüşüm
│   ├── proxy/
│   │   ├── handler.go              # Ana HTTP işleyici + failover mantığı
│   │   ├── anthropic.go            # Anthropic passthrough
│   │   ├── openai.go               # OpenAI format çevirisi
│   │   └── health.go               # Sağlık kontrolü
│   └── sse/sse.go                  # SSE yardımcıları
```

## Lisans

MIT