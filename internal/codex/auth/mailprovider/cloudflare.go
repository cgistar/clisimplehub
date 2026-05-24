package mailprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CloudflareProvider struct {
	email     string
	jwt       string
	addressID int
}

func (c *CloudflareProvider) Name() string { return "cloudflare" }

func (c *CloudflareProvider) RestoreState(params map[string]string) {
	if j := params["_jwt"]; j != "" {
		c.jwt = j
	}
	if id := params["_address_id"]; id != "" {
		fmt.Sscanf(id, "%d", &c.addressID)
	}
	if e := params["_email"]; e != "" {
		c.email = e
	}
}

func (c *CloudflareProvider) CreateEmail(params map[string]string) (string, string, error) {
	workerDomain := normalizeCloudflareWorkerDomain(params["cf_worker_domain"])
	if workerDomain == "" {
		return "", "", fmt.Errorf("cf_worker_domain is required")
	}
	emailDomain := params["cf_email_domain"]
	if emailDomain == "" {
		return "", "", fmt.Errorf("cf_email_domain is required")
	}
	adminPwd := params["cf_admin_password"]
	if adminPwd == "" {
		return "", "", fmt.Errorf("cf_admin_password is required")
	}

	// 若前端指定了邮箱，提取 local part 并验证域名；否则随机生成
	var name string
	if e := params["_email"]; e != "" {
		if at := strings.Index(e, "@"); at > 0 {
			name = e[:at]
			// 验证邮箱域名是否匹配 cf_email_domain
			inputDomain := e[at+1:]
			if inputDomain != emailDomain {
				return "", "", fmt.Errorf("email domain mismatch: input=%s, expected=%s", inputDomain, emailDomain)
			}
		} else {
			name = e
		}
	} else {
		name = randomEmailName()
	}

	client := &http.Client{Timeout: 15 * time.Second}
	body, _ := json.Marshal(map[string]any{
		"enablePrefix": true,
		"name":         name,
		"domain":       emailDomain,
	})
	req, _ := http.NewRequest("POST", "https://"+workerDomain+"/admin/new_address", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-admin-auth", adminPwd)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("cloudflare create email: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("cloudflare create email: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Address   string `json:"address"`
		JWT       string `json:"jwt"`
		Password  string `json:"password"`
		AddressID int    `json:"address_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", fmt.Errorf("cloudflare parse response: %w", err)
	}

	// If address is not in response, construct it from name + domain
	email := result.Address
	if email == "" {
		email = name + "@" + emailDomain
	}

	if result.JWT == "" {
		return "", "", fmt.Errorf("cloudflare: empty jwt in response")
	}

	c.email = email
	c.jwt = result.JWT
	c.addressID = result.AddressID
	return email, "", nil
}

func (c *CloudflareProvider) FetchVerificationCode(ctx context.Context, params map[string]string, email string, timeoutSec int) (string, error) {
	workerDomain := normalizeCloudflareWorkerDomain(params["cf_worker_domain"])
	if workerDomain == "" {
		return "", fmt.Errorf("cf_worker_domain is required")
	}
	jwt := c.jwt
	adminPwd := params["cf_admin_password"]
	if jwt == "" && adminPwd == "" {
		return "", fmt.Errorf("cloudflare: no jwt token (CreateEmail not called?)")
	}

	client := &http.Client{Timeout: 15 * time.Second}

	// Record old mail IDs to skip
	oldIDs := map[int]bool{}
	if mails, err := c.fetchMailsForVerificationTest(client, workerDomain, jwt, adminPwd, email); err == nil {
		for _, m := range mails {
			if m.ID != 0 {
				oldIDs[m.ID] = true
				// Check existing mails for code
				if code := extractCloudflareVerificationCode(m.Subject, m.Raw); code != "" {
					return code, nil
				}
			}
		}
	}
	if !parseBoolDefault(params["mail_wait_for_new"], true) {
		return "", nil
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
			mails, err := c.fetchMailsForVerificationTest(client, workerDomain, jwt, adminPwd, email)
			if err == nil {
				for _, m := range mails {
					if oldIDs[m.ID] {
						continue
					}
					if code := extractCloudflareVerificationCode(m.Subject, m.Raw); code != "" {
						// Delete the mail after successfully getting the code
						c.deleteMail(params, m.ID)
						return code, nil
					}
				}
			}
		}
	}
}

// deleteMail deletes a specific mail by ID using user-level API
func (c *CloudflareProvider) deleteMail(params map[string]string, mailID int) {
	workerDomain := normalizeCloudflareWorkerDomain(params["cf_worker_domain"])
	if workerDomain == "" || c.jwt == "" {
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://%s/api/mails/%d", workerDomain, mailID)
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "Bearer "+c.jwt)

	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// deleteAddress deletes the email address using admin-level API (cleanup method)
func (c *CloudflareProvider) deleteAddress(params map[string]string) {
	workerDomain := normalizeCloudflareWorkerDomain(params["cf_worker_domain"])
	adminPwd := params["cf_admin_password"]
	if workerDomain == "" || adminPwd == "" || c.addressID == 0 {
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("https://%s/admin/delete_address/%d", workerDomain, c.addressID)
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("x-admin-auth", adminPwd)

	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

type cfMail struct {
	ID      int    `json:"id"`
	Raw     string `json:"raw"`
	Source  string `json:"source"`
	Subject string `json:"subject"`
}

func (c *CloudflareProvider) fetchMails(client *http.Client, workerDomain, jwt string) ([]cfMail, error) {
	workerDomain = normalizeCloudflareWorkerDomain(workerDomain)
	req, _ := http.NewRequest("GET", "https://"+workerDomain+"/api/mails?limit=10&offset=0", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch mails: status %d", resp.StatusCode)
	}
	var result struct {
		Results []cfMail `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

func (c *CloudflareProvider) fetchMailsForVerificationTest(client *http.Client, workerDomain, jwt, adminPwd, email string) ([]cfMail, error) {
	if jwt != "" {
		return c.fetchMails(client, workerDomain, jwt)
	}
	return c.fetchAdminMails(client, workerDomain, adminPwd, email)
}

func (c *CloudflareProvider) fetchAdminMails(client *http.Client, workerDomain, adminPwd, email string) ([]cfMail, error) {
	workerDomain = normalizeCloudflareWorkerDomain(workerDomain)
	req, _ := http.NewRequest("GET", cloudflareAdminMailsURL(workerDomain, email), nil)
	req.Header.Set("x-admin-auth", adminPwd)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch admin mails: status %d", resp.StatusCode)
	}
	var result struct {
		Results []cfMail `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return filterCloudflareMailsByEmail(result.Results, email), nil
}

func cloudflareAdminMailsURL(workerDomain, email string) string {
	workerDomain = normalizeCloudflareWorkerDomain(workerDomain)
	params := url.Values{
		"limit":  {"20"},
		"offset": {"0"},
	}
	if strings.TrimSpace(email) != "" {
		params.Set("address", strings.TrimSpace(email))
	}
	return "https://" + workerDomain + "/admin/mails?" + params.Encode()
}

func filterCloudflareMailsByEmail(mails []cfMail, email string) []cfMail {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return mails
	}
	filtered := make([]cfMail, 0, len(mails))
	for _, mail := range mails {
		haystack := strings.ToLower(mail.Raw + "\n" + mail.Source + "\n" + mail.Subject)
		if strings.Contains(haystack, email) {
			filtered = append(filtered, mail)
		}
	}
	return filtered
}

func normalizeCloudflareWorkerDomain(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https//")
	value = strings.TrimPrefix(value, "http//")
	value = strings.TrimLeft(value, "/")
	if slash := strings.Index(value, "/"); slash >= 0 {
		value = value[:slash]
	}
	return strings.TrimRight(value, "/")
}

func extractCloudflareVerificationCode(subject, raw string) string {
	if strings.TrimSpace(subject) == "" {
		subject = extractRawMailHeader(raw, "Subject")
	}
	if code := extractNumericVerificationCode(subject); code != "" {
		return code
	}
	if !isVerificationCodeSubject(subject) {
		return ""
	}
	if code := extractVisibleVerificationCode(raw); code != "" {
		return code
	}
	return ""
}

func extractRawMailHeader(raw, headerName string) string {
	headerName = strings.ToLower(strings.TrimSpace(headerName))
	if raw == "" || headerName == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")
	var value strings.Builder
	matched := false
	prefix := headerName + ":"
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			break
		}
		if matched {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				value.WriteString(" ")
				value.WriteString(strings.TrimSpace(line))
				continue
			}
			break
		}
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			matched = true
			value.WriteString(strings.TrimSpace(line[len(prefix):]))
		}
	}
	return value.String()
}

// randomEmailName generates a random email local part (10-14 lowercase letters with 1-2 digits inserted).
func randomEmailName() string {
	nameLen := 10 + rand.Intn(5)
	chars := make([]byte, nameLen)
	for i := range chars {
		chars[i] = 'a' + byte(rand.Intn(26))
	}
	digitCount := 1 + rand.Intn(2)
	for i := 0; i < digitCount; i++ {
		pos := 2 + rand.Intn(len(chars)-2)
		digit := byte('0' + rand.Intn(10))
		chars = append(chars[:pos+1], chars[pos:]...)
		chars[pos] = digit
	}
	return string(chars)
}
