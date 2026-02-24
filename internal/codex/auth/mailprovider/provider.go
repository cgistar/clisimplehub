package mailprovider

import (
	"context"
	"fmt"
)

type EmailProvider interface {
	Name() string
	CreateEmail(params map[string]string) (email string, mailPassword string, err error)
	FetchVerificationCode(ctx context.Context, params map[string]string, email string, timeoutSec int) (code string, err error)
	RestoreState(params map[string]string)
}

// ProvisionResult 随机生成邮箱+开通后的返回结果
type ProvisionResult struct {
	Email         string            `json:"email"`
	Password      string            `json:"password"`
	ProviderState map[string]string `json:"providerState"`
}

// GenerateAndProvision 生成随机邮箱、调用供应商 API 开通、返回邮箱+密码+内部状态
func GenerateAndProvision(providerName string, params map[string]string) (*ProvisionResult, error) {
	p, err := NewProvider(providerName)
	if err != nil {
		return nil, err
	}
	email, _, err := p.CreateEmail(params)
	if err != nil {
		return nil, err
	}
	state := extractProviderState(p)
	return &ProvisionResult{
		Email:         email,
		Password:      GeneratePassword(14),
		ProviderState: state,
	}, nil
}

func extractProviderState(p EmailProvider) map[string]string {
	switch v := p.(type) {
	case *DuckMailProvider:
		if v.mailToken == "" {
			return nil
		}
		return map[string]string{"_mail_token": v.mailToken}
	case *CloudflareProvider:
		if v.jwt == "" {
			return nil
		}
		return map[string]string{
			"_jwt":        v.jwt,
			"_address_id": fmt.Sprintf("%d", v.addressID),
			"_email":      v.email,
		}
	case *GPTMailProvider:
		if v.email == "" {
			return nil
		}
		return map[string]string{"_email": v.email}
	case *TempMailProvider:
		if v.registrationEmail == "" {
			return nil
		}
		state := map[string]string{
			"_registration_email": v.registrationEmail,
			"_forward_email":      v.forwardEmail,
		}
		if v.epin != "" {
			state["_epin"] = v.epin
		}
		return state
	default:
		return nil
	}
}

// HasProviderState 检查 params 中是否包含已开通的 provider 状态 token（非空值）
func HasProviderState(params map[string]string) bool {
	for k, v := range params {
		if len(k) > 0 && k[0] == '_' && v != "" {
			return true
		}
	}
	return false
}

func AvailableProviders() []string {
	return []string{"duckmail", "gptmail", "cloudflare", "outlook", "tempmail"}
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
	case "tempmail":
		return &TempMailProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown email provider: %s", name)
	}
}
