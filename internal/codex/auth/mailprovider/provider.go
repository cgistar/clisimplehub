package mailprovider

import (
	"context"
	"fmt"
)

type EmailProvider interface {
	Name() string
	CreateEmail(params map[string]string) (email string, mailPassword string, err error)
	FetchVerificationCode(ctx context.Context, params map[string]string, email string, timeoutSec int) (code string, err error)
}

func AvailableProviders() []string {
	return []string{"duckmail", "gptmail", "cloudflare", "outlook"}
}

func NewProvider(name string) (EmailProvider, error) {
	switch name {
	case "duckmail":
		return &DuckMailProvider{}, nil
	case "gptmail":
		return &GPTMailProvider{}, nil
	case "cloudflare":
		return &CloudflareProvider{}, nil
	case "outlook":
		return &OutlookProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown email provider: %s", name)
	}
}
