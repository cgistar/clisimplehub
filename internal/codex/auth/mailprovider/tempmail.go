package mailprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

type TempMailProvider struct {
	registrationEmail string // 用于注册 OpenAI 的真实邮箱
	forwardEmail      string // TempMail 转发邮箱
	epin              string // 可选 PIN 码
}

func (t *TempMailProvider) Name() string { return "tempmail" }

func (t *TempMailProvider) RestoreState(params map[string]string) {
	if e := params["_registration_email"]; e != "" {
		t.registrationEmail = e
	}
	if f := params["_forward_email"]; f != "" {
		t.forwardEmail = f
	}
	if p := params["_epin"]; p != "" {
		t.epin = p
	}
}

func (t *TempMailProvider) CreateEmail(params map[string]string) (string, string, error) {
	// 获取注册邮箱（真实邮箱）
	registrationEmail := params["_email"]

	// 获取或生成 TempMail 转发邮箱
	forwardEmail := params["tempmail_forward_email"]
	if forwardEmail == "" {
		// 随机生成转发邮箱
		domains := []string{
			"mailto.plus",
			"fexpost.com",
			"fexbox.org",
			"mailbox.in.ua",
			"rover.info",
			"chitthi.in",
			"fextemp.com",
			"any.pink",
			"merepost.com",
		}

		const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
		length := rand.Intn(7) + 8 // 8-14位
		prefix := make([]byte, length)
		for i := range prefix {
			prefix[i] = chars[rand.Intn(len(chars))]
		}

		domain := domains[rand.Intn(len(domains))]
		forwardEmail = string(prefix) + "@" + domain
	}
	t.forwardEmail = forwardEmail

	// 获取可选 PIN 码
	if p := params["tempmail_epin"]; p != "" {
		t.epin = p
	}

	// 如果是随机生成模式（没有注册邮箱），返回转发邮箱
	// 这用于前端的"随机生成"按钮
	if registrationEmail == "" {
		return forwardEmail, "", nil
	}

	// 正常注册模式：保存注册邮箱，返回注册邮箱
	t.registrationEmail = registrationEmail
	return registrationEmail, "", nil
}

func (t *TempMailProvider) FetchVerificationCode(ctx context.Context, params map[string]string, email string, timeoutSec int) (string, error) {
	// 使用 TempMail 转发邮箱获取验证码
	forwardEmail := t.forwardEmail
	if forwardEmail == "" {
		return "", fmt.Errorf("tempmail forward email not set")
	}

	// 从 params 或内部状态获取 epin
	epin := t.epin
	if p := params["tempmail_epin"]; p != "" {
		epin = p
	}

	client := &http.Client{Timeout: 15 * time.Second}
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
			code, err := t.pollOnce(client, forwardEmail, epin)
			if err == nil && code != "" {
				return code, nil
			}
		}
	}
}

func (t *TempMailProvider) pollOnce(client *http.Client, email, epin string) (string, error) {
	// 构建请求 URL，对 email 进行 URL 编码
	reqURL := fmt.Sprintf("https://tempmail.plus/api/mails?email=%s", url.QueryEscape(email))
	if epin != "" {
		reqURL += "&epin=" + url.QueryEscape(epin)
	}

	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("tempmail API error: status %d: %s", resp.StatusCode, string(body))
	}

	var inbox struct {
		MailList []struct {
			MailID int `json:"mail_id"`
		} `json:"mail_list"`
	}
	if err := json.Unmarshal(body, &inbox); err != nil {
		return "", err
	}

	// 只检查最新邮件
	if len(inbox.MailList) == 0 {
		return "", nil
	}

	latestMail := inbox.MailList[0]
	return t.fetchMailDetail(client, email, epin, latestMail.MailID)
}

func (t *TempMailProvider) fetchMailDetail(client *http.Client, email, epin string, mailID int) (string, error) {
	reqURL := fmt.Sprintf("https://tempmail.plus/api/mails/%d?email=%s", mailID, url.QueryEscape(email))
	if epin != "" {
		reqURL += "&epin=" + url.QueryEscape(epin)
	}

	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("tempmail mail detail error: status %d: %s", resp.StatusCode, string(body))
	}

	var detail struct {
		Text string `json:"text"`
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		return "", err
	}

	// 优先从文本提取，降级到 HTML
	if code := extractTempMailCode(detail.Text); code != "" {
		return code, nil
	}
	if code := extractTempMailCode(detail.HTML); code != "" {
		return code, nil
	}

	return "", nil
}

var tempMailCodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\b(\d{6})\b`),     // 优先：6位纯数字
	regexp.MustCompile(`\b(\d{4,8})\b`),   // 降级：4-8位数字
}

func extractTempMailCode(text string) string {
	if text == "" {
		return ""
	}
	for _, re := range tempMailCodePatterns {
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}
