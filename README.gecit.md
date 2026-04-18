# Geçit

Geçit, Anthropic Messages API kullanan istemcileri tek bir uç noktadan farklı model sağlayıcılarına yönlendiren günlük kullanım odaklı bir uygulamadır.

Bu belge, fork’ın kullanıcı tarafındaki kurulumunu anlatır. Proxy çekirdeğinin teknik detayları için kök [README.md](README.md) dosyasına dönün.

## Kimler için?

- Claude Code’u tek bir `ANTHROPIC_BASE_URL` ile kullanmak isteyenler
- Opus çağrılarını native Anthropic aboneliğinden geçirip diğer model slotlarını farklı sağlayıcılara eşlemek isteyenler
- Codex, z.ai veya benzeri sağlayıcıları tek bir proxy arkasında toplamak isteyenler

## Hızlı Başlangıç

```bash
git clone https://github.com/ahmet/opencode-proxy.git
cd opencode-proxy
cp config.example.json config.json
# config.json içindeki credential alanlarını doldur
make build
./opencode-proxy
```

Ayrı bir terminalde doğrulama:

```bash
make test
curl http://localhost:8787/health
curl http://localhost:8787/ready
```

## Claude Code ile Kullanım

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

Bu yapılandırmada:

- Opus çağrıları `anthropic_passthrough` üzerinden native Anthropic aboneliğine gider
- Sonnet ve Haiku slotları `gpt-5.4` gibi farklı bir sağlayıcı modeline eşlenebilir
- Claude Code araç çağrıları aynı `ANTHROPIC_BASE_URL` üstünden çalışır

## Önerilen Sağlayıcı Düzeni

Temel fikir şudur:

- `claude-opus-*` → `anthropic_passthrough`
- `gpt-5.4` ve `gpt-5.4-*` → `codex`
- `glm-5.1` ve `glm-*` → `anthropic` veya `openai` uyumlu sağlayıcılar

Örnek yapı için kök [README.md](README.md) içindeki `config.json` örneğini kullanın.

## Bu repo içindeki önemli komutlar

| Komut | Açıklama |
|-------|----------|
| `make build` | Binary üretir |
| `./opencode-proxy` | Proxy’yi başlatır |
| `make test` | Testleri çalıştırır |
| `make vet` | Go statik analizini çalıştırır |
| `make lint` | `vet` + `test` çalıştırır |

## Hangi README’yi ne zaman okumalıyım?

- Ürünü kullanacak veya kendi ortamına kuracak kişiyseniz: `README.gecit.md`
- Routing, provider tipleri, health endpoint’leri ve teknik davranışları inceleyecekseniz: `README.md`
