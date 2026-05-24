package mailprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type GPTMailProvider struct {
	email string
}

func (g *GPTMailProvider) Name() string { return "gptmail" }

func (g *GPTMailProvider) RestoreState(params map[string]string) {
	if e := params["_email"]; e != "" {
		g.email = e
	}
}

func (g *GPTMailProvider) CreateEmail(params map[string]string) (string, string, error) {
	// 若前端指定了邮箱，直接使用，跳过 API 调用
	if e := params["_email"]; e != "" {
		g.email = e
		return e, "", nil
	}

	apiBase := strings.TrimRight(params["gptmail_api_base"], "/")
	if apiBase == "" {
		apiBase = "https://mail.chatgpt.org.uk"
	}
	apiKey := params["gptmail_api_key"]
	if apiKey == "" {
		apiKey = "gpt-test"
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", apiBase+"/api/generate-email", nil)
	req.Header.Set("X-API-Key", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("gptmail generate-email: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("gptmail generate-email: status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Email string `json:"email"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("gptmail parse response: %w", err)
	}
	if !result.Success || result.Data.Email == "" {
		return "", "", fmt.Errorf("gptmail generate-email failed: %s", result.Error)
	}

	g.email = result.Data.Email
	return result.Data.Email, "", nil
}

func (g *GPTMailProvider) FetchVerificationCode(ctx context.Context, params map[string]string, email string, timeoutSec int) (string, error) {
	apiBase := strings.TrimRight(params["gptmail_api_base"], "/")
	if apiBase == "" {
		apiBase = "https://mail.chatgpt.org.uk"
	}
	apiKey := params["gptmail_api_key"]
	if apiKey == "" {
		apiKey = "gpt-test"
	}
	if email == "" {
		email = g.email
	}

	client := &http.Client{Timeout: 15 * time.Second}
	if !parseBoolDefault(params["mail_wait_for_new"], true) {
		return g.pollOnce(client, apiBase, apiKey, email)
	}

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled")
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout waiting for verification code")
			}
			code, err := g.pollOnce(client, apiBase, apiKey, email)
			if err == nil && code != "" {
				return code, nil
			}
		}
	}
}

func (g *GPTMailProvider) pollOnce(client *http.Client, apiBase, apiKey, email string) (string, error) {
	req, _ := http.NewRequest("GET", apiBase+"/api/emails?email="+email, nil)
	req.Header.Set("X-API-Key", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Emails []struct {
				ID          string `json:"id"`
				Subject     string `json:"subject"`
				Content     string `json:"content"`
				HTMLContent string `json:"html_content"`
			} `json:"emails"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	for _, m := range result.Data.Emails {
		subj := strings.ToLower(m.Subject)
		if !strings.Contains(subj, "openai") &&
			!strings.Contains(subj, "verify") &&
			!strings.Contains(subj, "code") {
			continue
		}
		// Try content first, then html_content
		content := m.Content
		if content == "" {
			content = m.HTMLContent
		}
		if code := extractVerificationCode(content); code != "" {
			return code, nil
		}
	}
	return "", nil
}
