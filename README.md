# Anthropic Proxy

Anthropic Messages API protokolünü destekleyen istemcileri (Claude Code, Cursor vb.) model bazlı provider kümelerine yönlendiren reverse proxy.

## Mimari

```text
Claude Code ──► localhost:8787 ──► claude-opus-* ──► anthropic_passthrough
                              ├──► gpt-5.4      ──► codex-oauth
                              └──► glm-5.1      ──► z.ai / opencode-go
```

- Routing model bazlı çalışır
- `claude-opus-*` istekleri yalnız `anthropic_passthrough` kümesine gider
- `gpt-5.4` ve `gpt-5.4-*` yalnız codex provider kümesine gider
- `glm-5.1` ve `glm-*` yalnız glm provider kümesine gider
- Farklı model kümeleri birbirine fallback yapmaz
- Aynı model kümesi içinde failover, runtime hata / limit durumuna göre çalışır
- `priority` artık karar verici değildir; config sırası + cooldown durumu kullanılır
- `config.json` dosyasından sıcak yeniden yükleme yapılır
- Yapısal loglama (`slog`)
- Request ID izleme
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
    "ANTHROPIC_API_KEY": "fake_key",
    "ANTHROPIC_BASE_URL": "http://localhost:8787",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "claude-opus-4-7",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "gpt-5.4",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "gpt-5.4"
  }
}
```

Bu kurulumda Opus çağrıları Claude/Anthropic aboneliği üzerinden `anthropic_passthrough` provider'ına gider. Sonnet ve Haiku slotları aynı anda `gpt-5.4` için kullanılabilir.

## Yapılandırma

Örnek `config.json`:

```json
{
  "port": 8787,
  "providers": [
    {
      "name": "claude-native",
      "type": "anthropic_passthrough",
      "base_url": "https://api.anthropic.com",
      "api_key": "",
      "models": ["claude-opus-*"],
      "priority": 0
    },
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

`anthropic_passthrough` tipi, Claude Code'un istekte taşıdığı Anthropic kimliğini gerçek Anthropic API'ye iletir. Bu sayede:

- **Opus 4.7** doğrudan Claude/Anthropic aboneliğinden kullanılabilir
- **Sonnet** ve **Haiku** slotları proxy üstünden `gpt-5.4` modeline yönlendirilebilir
- Claude Code içindeki yerleşik `WebSearch` ve `WebFetch` araçları da aynı `ANTHROPIC_BASE_URL` üstünden çalışır; proxy içinde ayrı HTTP tool endpoint'i veya MCP server gerektirmez

**Örnek config:**

```json
{
  "name": "claude-native",
  "type": "anthropic_passthrough",
  "base_url": "https://api.anthropic.com",
  "api_key": "",
  "models": ["claude-opus-*"]
}
```

## Routing Kuralları

- Bir model için explicit `models` eşleşmesi varsa yalnız o küme kullanılır.
- `claude-opus-*` istekleri yalnız `claude-native` gibi `anthropic_passthrough` provider'larına gider.
- `gpt-5.4` / `gpt-5.4-*` istekleri yalnız `codex-oauth` provider'ına gider.
- `glm-5.1` / `glm-*` istekleri yalnız `z.ai` ve `opencode-go` provider'larına gider.
- Aynı model kümesinde ilk provider limitteyse veya hata verirse diğer uygun provider denenir.
- Model kümesi dışına geçiş yapılmaz.

## Sağlık Kontrolü

```bash
curl http://localhost:8787/health
curl http://localhost:8787/ready
```

`/health` yanıtı artık yalnız `status` değil, `port`, `provider_count` ve her provider için aşağıdaki operasyonel alanları da döner:

- `models`
- `api_key_configured`
- `oauth_configured`
- `incoming_api_key_required`
- `exhausted`

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
