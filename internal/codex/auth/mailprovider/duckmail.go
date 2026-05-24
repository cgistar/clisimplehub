package mailprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type DuckMailProvider struct {
	mailToken string
}

func (d *DuckMailProvider) Name() string { return "duckmail" }

func (d *DuckMailProvider) RestoreState(params map[string]string) {
	if t := params["_mail_token"]; t != "" {
		d.mailToken = t
	}
}

func (d *DuckMailProvider) CreateEmail(params map[string]string) (string, string, error) {
	apiBase := strings.TrimRight(params["duckmail_api_base"], "/")
	if apiBase == "" {
		return "", "", fmt.Errorf("duckmail_api_base is required")
	}

	// 若前端指定了邮箱，使用该邮箱；否则随机生成
	var email string
	if e := params["_email"]; e != "" {
		email = e
	} else {
		const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
		length := rand.Intn(6) + 8
		prefix := make([]byte, length)
		for i := range prefix {
			prefix[i] = chars[rand.Intn(len(chars))]
		}
		email = string(prefix) + "@duckmail.sbs"
	}
	mailPwd := GeneratePassword(14)
	client := &http.Client{Timeout: 15 * time.Second}

	// 1. Create account
	payload, _ := json.Marshal(map[string]string{"address": email, "password": mailPwd})
	req, _ := http.NewRequest("POST", apiBase+"/accounts", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("create mailbox: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("create mailbox: status %d: %s", resp.StatusCode, string(body))
	}

	// 2. Get mail token
	tokenPayload, _ := json.Marshal(map[string]string{"address": email, "password": mailPwd})
	req2, _ := http.NewRequest("POST", apiBase+"/token", strings.NewReader(string(tokenPayload)))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		return "", "", fmt.Errorf("get mail token: %w", err)
	}
	defer resp2.Body.Close()
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&tokenResp); err != nil {
		return "", "", fmt.Errorf("parse mail token: %w", err)
	}
	if tokenResp.Token == "" {
		return "", "", fmt.Errorf("empty mail token")
	}
	d.mailToken = tokenResp.Token
	return email, mailPwd, nil
}

func (d *DuckMailProvider) FetchVerificationCode(ctx context.Context, params map[string]string, email string, timeoutSec int) (string, error) {
	apiBase := strings.TrimRight(params["duckmail_api_base"], "/")
	if apiBase == "" {
		apiBase = "https://api.duckmail.sbs" // Use default if not provided
	}
	if d.mailToken == "" {
		return "", fmt.Errorf("mail token not set, call CreateEmail first")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	if !parseBoolDefault(params["mail_wait_for_new"], true) {
		return d.pollOnce(client, apiBase)
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
			code, err := d.pollOnce(client, apiBase)
			if err == nil && code != "" {
				return code, nil
			}
		}
	}
}

func (d *DuckMailProvider) pollOnce(client *http.Client, apiBase string) (string, error) {
	req, _ := http.NewRequest("GET", apiBase+"/messages", nil)
	req.Header.Set("Authorization", "Bearer "+d.mailToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var msgs struct {
		Members []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"hydra:member"`
	}
	if err := json.Unmarshal(body, &msgs); err != nil {
		return "", err
	}

	// Step 1: Try to extract code from subject first (like Python implementation)
	for _, m := range msgs.Members {
		if code := extractVerificationCode(m.Subject); code != "" {
			return code, nil
		}
	}

	// Step 2: If not found in subject, read message details (limit to first 5 messages)
	limit := len(msgs.Members)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		m := msgs.Members[i]
		req2, _ := http.NewRequest("GET", fmt.Sprintf("%s/messages/%s", apiBase, m.ID), nil)
		req2.Header.Set("Authorization", "Bearer "+d.mailToken)
		resp2, err := client.Do(req2)
		if err != nil {
			continue
		}
		msgBody, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()

		var detail struct {
			Text string `json:"text"`
			HTML string `json:"html"`
		}
		if err := json.Unmarshal(msgBody, &detail); err != nil {
			continue
		}

		// Try text body first, then HTML
		if code := extractVerificationCode(detail.Text); code != "" {
			return code, nil
		}
		if code := extractVerificationCode(detail.HTML); code != "" {
			return code, nil
		}
	}
	return "", nil
}

// --- helpers ---

func GeneratePassword(length int) string {
	if length < 4 {
		length = 4
	}
	lower := "abcdefghijklmnopqrstuvwxyz"
	upper := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits := "0123456789"
	special := "!@#$%&*"
	all := lower + upper + digits + special

	pwd := make([]byte, length)
	pwd[0] = lower[rand.Intn(len(lower))]
	pwd[1] = upper[rand.Intn(len(upper))]
	pwd[2] = digits[rand.Intn(len(digits))]
	pwd[3] = special[rand.Intn(len(special))]
	for i := 4; i < length; i++ {
		pwd[i] = all[rand.Intn(len(all))]
	}
	rand.Shuffle(length, func(i, j int) { pwd[i], pwd[j] = pwd[j], pwd[i] })
	return string(pwd)
}

var codePatterns = []*regexp.Regexp{
	// Match "Your ChatGPT code is 360145" or similar patterns
	regexp.MustCompile(`(?i)code\s+is\s+([A-Z0-9]{6})\b`),
	// Match "ABC-123 xAI confirmation code" format (with or without hyphen)
	regexp.MustCompile(`(?i)([A-Z0-9]{3})-?([A-Z0-9]{3})\s+xAI confirmation code`),
	// Match any 6-digit number (pure digits)
	regexp.MustCompile(`\b(\d{6})\b`),
	// Match any 6-character alphanumeric code (case-insensitive)
	regexp.MustCompile(`(?i)\b([A-Z0-9]{6})\b`),
}

func extractVerificationCode(text string) string {
	if text == "" {
		return ""
	}
	for _, re := range codePatterns {
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			// If pattern has 2 groups (ABC-123 format), concatenate them
			if len(m) == 3 && m[2] != "" {
				return strings.ToUpper(m[1] + m[2])
			}
			return strings.ToUpper(m[1])
		}
	}
	return ""
}
