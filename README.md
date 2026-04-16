# Anthropic Proxy

Anthropic Messages API protokolünü destekleyen istemcileri (Claude Code, Cursor vb.) model bazlı provider kümelerine yönlendiren reverse proxy.

## Mimari

```text
Claude Code ──► localhost:8787 ──► glm-5.1  ──► z.ai / opencode-go
                              └──► gpt-5.4 ──► codex-oauth
```

- Routing model bazlı çalışır
- `glm-5.1` ve `glm-*` yalnız glm provider kümesine gider
- `gpt-5.4` ve `gpt-5.4-*` yalnız codex provider kümesine gider
- Farklı model kümeleri birbirine fallback yapmaz
- Aynı model kümesi içinde failover, runtime hata / limit durumuna göre çalışır
- `priority` artık karar verici değildir; config sırası + cooldown durumu kullanılır
- `config.json` dosyasından sıcak yeniden yükleme yapılır
- Structured logging (`slog`)
- Request ID tracing
- Panic recovery middleware

## Hızlı Başlangıç

```bash
git clone https://github.com/ahmet/opencode-proxy.git
cd opencode-proxy
cp config.example.json config.json
# config.json içindeki credential alanlarını doldur
make build
./opencode-proxy
make test
```

Claude Code `settings.json`:

```json
{
  "env": {
    "ANTHROPIC_API_KEY": "fake_key_for_local_testing",
    "ANTHROPIC_BASE_URL": "http://localhost:8787",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "gpt-5.4"
  }
}
```

Modeli `glm-5.1` yaparsan glm kümesine, `gpt-5.4` yaparsan codex kümesine gidersin.

## Yapılandırma

Örnek `config.json`:

```json
{
  "port": 8787,
  "providers": [
    {
      "name": "z.ai",
      "type": "anthropic",
      "base_url": "https://api.z.ai/api/anthropic",
      "api_key": "Z_AI_API_KEY_BURAYA",
      "models": ["glm-5.1", "glm-*"],
      "priority": 0
    },
    {
      "name": "opencode-go",
      "type": "openai",
      "base_url": "https://opencode.ai/zen/go/v1/chat/completions",
      "api_key": "OPENCODE_GO_API_KEY_BURAYA",
      "models": ["glm-5.1", "glm-*"],
      "priority": 0
    },
    {
      "name": "codex-oauth",
      "type": "codex",
      "base_url": "https://chatgpt.com/backend-api/codex",
      "api_key": "",
      "oauth": {
        "access_token": "CODEX_ACCESS_TOKEN_BURAYA",
        "refresh_token": "CODEX_REFRESH_TOKEN_BURAYA"
      },
      "models": ["gpt-5.4", "gpt-5.4-*"],
      "priority": 0
    }
  ]
}
```

| Alan | Açıklama |
|------|-----------|
| `port` | Proxy dinleme portu |
| `name` | Sağlayıcı adı |
| `type` | `anthropic`, `openai`, `codex`, `anthropic_passthrough` |
| `base_url` | Sağlayıcı API endpoint'i |
| `api_key` | API anahtarı |
| `oauth` | Codex OAuth bilgileri |
| `models` | Sağlayıcının kabul ettiği explicit model desenleri |
| `priority` | Geriye dönük config alanı; seçim mantığında belirleyici değil |

### Claude Code Native Abonelik ile Kullanım

`anthropic_passthrough` tipi, Claude Code'un kendi API key'ini kullanarak gerçek Anthropic API'ye yönlendirme yapar. Bu sayede:

- **Opus** modeli için Claude Code aboneliğinden kullanım
- **Sonnet/Haiku** modelleri için proxy üzerinden alternatif provider kullanımı

**Örnek config:**

```json
{
  "name": "claude-native",
  "type": "anthropic_passthrough",
  "base_url": "https://api.anthropic.com",
  "api_key": "",
  "models": ["claude-opus-4-7"]
}
```

Claude Code `settings.json`:

```json
{
  "env": {
    "ANTHROPIC_API_KEY": "fake_key",
    "ANTHROPIC_BASE_URL": "http://localhost:8787",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-20250514",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-5.4"
  }
}
```

## Routing Kuralları

- Bir model için explicit `models` eşleşmesi varsa yalnız o küme kullanılır.
- `glm-5.1` / `glm-*` istekleri yalnız `z.ai` ve `opencode-go` provider'larına gider.
- `gpt-5.4` / `gpt-5.4-*` istekleri yalnız `codex-oauth` provider'ına gider.
- Aynı model kümesinde ilk provider limitteyse veya hata verirse diğer uygun provider denenir.
- Model kümesi dışına geçiş yapılmaz.

## Web Araçları

Proxy, dahili web araçları sunar.

```bash
curl "http://localhost:8787/v1/tools/web_fetch?url=https://example.com"
curl "http://localhost:8787/v1/tools/web_search?q=go+programming"
```

## MCP Server

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

## Sağlık Kontrolü

```bash
curl http://localhost:8787/health
```

## Make Komutları

| Komut | Açıklama |
|-------|-----------|
| `make build` | Derle |
| `make run` | Çalıştır |
| `make test` | Testleri çalıştır |
| `make vet` | Statik analiz |
| `make lint` | vet + test |
| `make clean` | Derleme çıktısını temizle |

## Lisans

MIT
