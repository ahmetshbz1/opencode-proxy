package provider

import "opencode-proxy/internal/config"

type Type string

const (
	Anthropic Type = "anthropic"
	OpenAI    Type = "openai"
)

type Provider struct {
	Name     string
	Type     Type
	BaseURL  string
	APIKey   string
	Priority int
}

func Ordered() []Provider {
	cfgs := config.Providers()
	out := make([]Provider, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, Provider{
			Name:     c.Name,
			Type:     Type(c.Type),
			BaseURL:  c.BaseURL,
			APIKey:   c.APIKey,
			Priority: c.Priority,
		})
	}
	return out
}
