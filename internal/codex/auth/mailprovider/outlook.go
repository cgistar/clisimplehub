package mailprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type OutlookProvider struct {
	email        string
	mode         string
	clientID     string
	refreshToken string
}

func (o *OutlookProvider) Name() string { return "outlook" }

func (o *OutlookProvider) CreateEmail(params map[string]string) (string, string, error) {
	o.email = params["outlook_email"]
	o.mode = params["outlook_mode"]
	o.clientID = params["outlook_client_id"]
	o.refreshToken = params["outlook_refresh_token"]

	if o.email == "" || o.clientID == "" || o.refreshToken == "" {
		return "", "", fmt.Errorf("outlook_email, outlook_client_id, outlook_refresh_token are required")
	}
	if o.mode == "" {
		o.mode = "imap"
	}

	return o.email, "", nil
}

func (o *OutlookProvider) FetchVerificationCode(ctx context.Context, params map[string]string, email string, timeoutSec int) (string, error) {
	if o.mode == "graph" {
		return o.fetchViaGraph(ctx, timeoutSec)
	}
	return o.fetchViaIMAP(ctx, timeoutSec)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (o *OutlookProvider) getAccessToken(scope string) (token, imapServer string, err error) {
	methods := []struct {
		url        string
		imapServer string
		label      string
	}{
		{
			url:        "https://login.live.com/oauth20_token.srf",
			imapServer: "outlook.office365.com",
			label:      "login.live.com",
		},
		{
			url:        "https://login.microsoftonline.com/consumers/oauth2/v2.0/token",
			imapServer: "outlook.live.com",
			label:      "consumers/oauth2",
		},
	}

	var lastErr error
	client := &http.Client{Timeout: 15 * time.Second}

	for _, method := range methods {
		data := url.Values{
			"client_id":     {o.clientID},
			"grant_type":    {"refresh_token"},
			"refresh_token": {o.refreshToken},
		}
		if scope != "" {
			data.Set("scope", scope)
		}

		resp, err := client.PostForm(method.url, data)
		if err != nil {
			lastErr = err
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result tokenResponse
		if err := json.Unmarshal(body, &result); err != nil {
			lastErr = err
			continue
		}

		if result.AccessToken != "" {
			return result.AccessToken, method.imapServer, nil
		}

		lastErr = fmt.Errorf("%s: %s", result.Error, result.ErrorDescription)
		if strings.Contains(strings.ToLower(result.ErrorDescription), "service abuse") {
			return "", "", fmt.Errorf("account banned: %s", result.ErrorDescription)
		}
	}

	return "", "", fmt.Errorf("all token methods failed: %v", lastErr)
}

// xoauth2Client implements SASL XOAUTH2 authentication
type xoauth2Client struct {
	username string
	token    string
}

func (a *xoauth2Client) Start() (mech string, ir []byte, err error) {
	authString := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.token)
	return "XOAUTH2", []byte(authString), nil
}

func (a *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	// XOAUTH2 doesn't use server challenges in the success case
	return nil, nil
}

func (o *OutlookProvider) fetchViaIMAP(ctx context.Context, timeoutSec int) (string, error) {
	accessToken, imapServer, err := o.getAccessToken("https://outlook.office.com/IMAP.AccessAsUser.All offline_access")
	if err != nil {
		return "", fmt.Errorf("get access token: %w", err)
	}

	options := &imapclient.Options{
		TLSConfig: nil,
	}
	c, err := imapclient.DialTLS(imapServer+":993", options)
	if err != nil {
		return "", fmt.Errorf("connect IMAP: %w", err)
	}
	defer c.Close()

	xoauth2 := &xoauth2Client{
		username: o.email,
		token:    accessToken,
	}
	if err := c.Authenticate(xoauth2); err != nil {
		return "", fmt.Errorf("IMAP auth: %w", err)
	}

	knownIDs, err := o.getKnownMailIDs(c)
	if err != nil {
		return "", fmt.Errorf("get known mail IDs: %w", err)
	}

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	fallbackTried := false
	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled")
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout")
			}

			allIDs, err := o.getOpenAIMailIDs(c)
			if err != nil {
				continue
			}

			newIDs := difference(allIDs, knownIDs)
			if len(newIDs) > 0 {
				for _, id := range newIDs {
					otp, err := o.extractOTPFromMail(c, id)
					if err == nil && otp != "" {
						return otp, nil
					}
				}
			}

			if !fallbackTried && time.Since(start) > 15*time.Second {
				fallbackTried = true
				for _, id := range lastN(knownIDs, 3) {
					otp, err := o.extractOTPFromMail(c, id)
					if err == nil && otp != "" {
						return otp, nil
					}
				}
			}
		}
	}
}

func (o *OutlookProvider) getKnownMailIDs(c *imapclient.Client) ([]imap.UID, error) {
	selectData, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		return nil, err
	}

	if selectData.NumMessages == 0 {
		return []imap.UID{}, nil
	}

	// Outlook IMAP SEARCH doesn't work reliably, so we FETCH messages
	// and filter on the client side. To optimize, only fetch the last 5 messages
	// (most recent emails are more likely to contain the OTP we need)
	seqSet := imap.SeqSet{}

	// Calculate range: fetch last 5 messages (or all if less than 5)
	startSeq := uint32(1)
	if selectData.NumMessages > 5 {
		startSeq = selectData.NumMessages - 4 // Last 5 messages
	}
	seqSet.AddRange(startSeq, selectData.NumMessages)

	fetchCmd := c.Fetch(seqSet, &imap.FetchOptions{
		UID:      true,
		Envelope: true,
	})

	var openaiUIDs []imap.UID
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		var msgUID imap.UID
		var envelope *imap.Envelope

		for {
			item := msg.Next()
			if item == nil {
				break
			}

			switch data := item.(type) {
			case *imapclient.FetchItemDataUID:
				msgUID = data.UID
			case imapclient.FetchItemDataUID:
				msgUID = data.UID
			case *imapclient.FetchItemDataEnvelope:
				envelope = data.Envelope
			case imapclient.FetchItemDataEnvelope:
				envelope = data.Envelope
			}
		}

		// Filter for OpenAI emails
		if msgUID > 0 && envelope != nil && len(envelope.From) > 0 {
			fromAddr := strings.ToLower(envelope.From[0].Addr())
			if strings.Contains(fromAddr, "openai.com") {
				openaiUIDs = append(openaiUIDs, msgUID)
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return nil, err
	}

	return openaiUIDs, nil
}

func (o *OutlookProvider) fetchEnvelope(c *imapclient.Client, uid imap.UID) (*imap.Envelope, error) {
	fetchCmd := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		Envelope: true,
	})

	var envelope *imap.Envelope
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		for {
			item := msg.Next()
			if item == nil {
				break
			}

			// Try both pointer and value types
			switch data := item.(type) {
			case *imapclient.FetchItemDataEnvelope:
				envelope = data.Envelope
			case imapclient.FetchItemDataEnvelope:
				envelope = data.Envelope
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return nil, err
	}

	return envelope, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (o *OutlookProvider) getOpenAIMailIDs(c *imapclient.Client) ([]imap.UID, error) {
	return o.getKnownMailIDs(c)
}

func (o *OutlookProvider) extractOTPFromMail(c *imapclient.Client, uid imap.UID) (string, error) {
	fetchCmd := c.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{
			{Specifier: imap.PartSpecifierHeader},
			{Specifier: imap.PartSpecifierText},
		},
	})

	var subject, body string
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		for {
			item := msg.Next()
			if item == nil {
				break
			}

			// Try both pointer and value types
			switch data := item.(type) {
			case *imapclient.FetchItemDataBodySection:
				if data.Literal != nil {
					content, _ := io.ReadAll(data.Literal)
					contentStr := string(content)

					if data.Section.Specifier == imap.PartSpecifierHeader {
						lines := strings.Split(contentStr, "\n")
						for _, line := range lines {
							if strings.HasPrefix(strings.ToLower(line), "subject:") {
								subject = strings.TrimSpace(strings.TrimPrefix(line, "Subject:"))
								subject = strings.TrimSpace(strings.TrimPrefix(subject, "subject:"))
								break
							}
						}
					} else if data.Section.Specifier == imap.PartSpecifierText {
						body = contentStr
					}
				}
			case imapclient.FetchItemDataBodySection:
				if data.Literal != nil {
					content, _ := io.ReadAll(data.Literal)
					contentStr := string(content)

					if data.Section.Specifier == imap.PartSpecifierHeader {
						lines := strings.Split(contentStr, "\n")
						for _, line := range lines {
							if strings.HasPrefix(strings.ToLower(line), "subject:") {
								subject = strings.TrimSpace(strings.TrimPrefix(line, "Subject:"))
								subject = strings.TrimSpace(strings.TrimPrefix(subject, "subject:"))
								break
							}
						}
					} else if data.Section.Specifier == imap.PartSpecifierText {
						body = contentStr
					}
				}
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return "", err
	}

	if code := extractVerificationCode(subject); code != "" {
		return code, nil
	}
	return extractVerificationCode(body), nil
}

type graphMessage struct {
	ID               string    `json:"id"`
	Subject          string    `json:"subject"`
	ReceivedDateTime time.Time `json:"receivedDateTime"`
	Body             struct {
		Content string `json:"content"`
	} `json:"body"`
}

type graphResponse struct {
	Value []graphMessage `json:"value"`
}

func (o *OutlookProvider) fetchViaGraph(ctx context.Context, timeoutSec int) (string, error) {
	accessToken, _, err := o.getAccessToken("https://graph.microsoft.com/.default")
	if err != nil {
		return "", fmt.Errorf("get Graph token: %w", err)
	}

	knownIDs, err := o.getKnownGraphMessages(accessToken)
	if err != nil {
		return "", fmt.Errorf("get known messages: %w", err)
	}

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	fallbackTried := false
	start := time.Now()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled")
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout")
			}

			messages, err := o.queryGraphMessages(accessToken)
			if err != nil {
				continue
			}

			allIDs := extractGraphIDs(messages)
			newIDs := difference(allIDs, knownIDs)

			if len(newIDs) > 0 {
				for _, msg := range messages {
					if contains(newIDs, msg.ID) {
						if otp := extractOTPFromGraphMessage(msg); otp != "" {
							return otp, nil
						}
					}
				}
			}

			if !fallbackTried && time.Since(start) > 15*time.Second {
				fallbackTried = true
				knownMessages := filterKnownMessages(messages, knownIDs)
				for _, msg := range lastNMessages(knownMessages, 3) {
					if otp := extractOTPFromGraphMessage(msg); otp != "" {
						return otp, nil
					}
				}
			}
		}
	}
}

func (o *OutlookProvider) getKnownGraphMessages(token string) ([]string, error) {
	messages, err := o.queryGraphMessages(token)
	if err != nil {
		return nil, err
	}
	return extractGraphIDs(messages), nil
}

func (o *OutlookProvider) queryGraphMessages(token string) ([]graphMessage, error) {
	baseURL := "https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages"
	params := url.Values{
		"$filter":  {"contains(from/emailAddress/address, 'openai.com')"},
		"$select":  {"id,subject,body,from,receivedDateTime"},
		"$orderby": {"receivedDateTime desc"},
		"$top":     {"10"},
	}

	req, err := http.NewRequest("GET", baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Graph API error: %d: %s", resp.StatusCode, string(body))
	}

	var result graphResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Value, nil
}

func extractOTPFromGraphMessage(msg graphMessage) string {
	if code := extractVerificationCode(msg.Subject); code != "" {
		return code
	}
	return extractVerificationCode(msg.Body.Content)
}

func extractGraphIDs(messages []graphMessage) []string {
	ids := make([]string, len(messages))
	for i, msg := range messages {
		ids[i] = msg.ID
	}
	return ids
}

func filterKnownMessages(messages []graphMessage, knownIDs []string) []graphMessage {
	var result []graphMessage
	for _, msg := range messages {
		if contains(knownIDs, msg.ID) {
			result = append(result, msg)
		}
	}
	return result
}

func lastNMessages(messages []graphMessage, n int) []graphMessage {
	if len(messages) <= n {
		return messages
	}
	return messages[len(messages)-n:]
}

func difference[T comparable](a, b []T) []T {
	set := make(map[T]bool)
	for _, v := range b {
		set[v] = true
	}
	var result []T
	for _, v := range a {
		if !set[v] {
			result = append(result, v)
		}
	}
	return result
}

func lastN[T any](slice []T, n int) []T {
	if len(slice) <= n {
		return slice
	}
	return slice[len(slice)-n:]
}

func contains[T comparable](slice []T, item T) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}
