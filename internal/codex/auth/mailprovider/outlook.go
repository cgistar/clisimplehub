package mailprovider

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

type OutlookProvider struct {
	email             string
	mode              string
	clientID          string
	refreshToken      string
	graphDelete       bool
	graphDeleteStrict bool
}

func (o *OutlookProvider) Name() string { return "outlook" }

func (o *OutlookProvider) RestoreState(_ map[string]string) {}

func (o *OutlookProvider) CreateEmail(params map[string]string) (string, string, error) {
	o.email = params["outlook_email"]
	o.mode = params["outlook_mode"]
	o.clientID = params["outlook_client_id"]
	o.refreshToken = params["outlook_refresh_token"]
	o.graphDelete = parseBoolDefault(params["outlook_graph_delete"], true)
	o.graphDeleteStrict = parseBoolDefault(params["outlook_graph_delete_strict"], false)

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
		TLSConfig: &tls.Config{
			ServerName: imapServer,
			MinVersion: tls.VersionTLS12,
		},
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

	triedUIDs := make(map[imap.UID]struct{})

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
			if len(allIDs) == 0 {
				continue
			}

			newIDs := difference(allIDs, knownIDs)
			candidateID := allIDs[len(allIDs)-1]
			if len(newIDs) > 0 {
				candidateID = newIDs[len(newIDs)-1]
				knownIDs = dedupeAndSortUIDs(append(knownIDs, newIDs...))
			}

			if _, exists := triedUIDs[candidateID]; exists {
				continue
			}
			triedUIDs[candidateID] = struct{}{}

			otp, err := o.extractOTPFromMail(c, candidateID)
			if err == nil && otp != "" {
				if err := o.deleteMailByUID(c, candidateID); err != nil {
					return "", fmt.Errorf("delete OTP mail (uid=%d): %w", candidateID, err)
				}
				return otp, nil
			}
		}
	}
}

func (o *OutlookProvider) deleteMailByUID(c *imapclient.Client, uid imap.UID) error {
	storeCmd := c.Store(imap.UIDSetNum(uid), &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagDeleted},
	}, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("mark deleted: %w", err)
	}

	expungeCmd := c.UIDExpunge(imap.UIDSetNum(uid))
	if err := expungeCmd.Close(); err != nil {
		return fmt.Errorf("uid expunge: %w", err)
	}

	return nil
}

func (o *OutlookProvider) getKnownMailIDs(c *imapclient.Client) ([]imap.UID, error) {
	selectData, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		return nil, err
	}

	if selectData.NumMessages == 0 {
		return []imap.UID{}, nil
	}

	return o.searchOpenAIMailUIDs(c)
}

func (o *OutlookProvider) searchOpenAIMailUIDs(c *imapclient.Client) ([]imap.UID, error) {
	fromValues := []string{"noreply@tm.openai.com", "openai.com"}
	var errs []string
	success := false

	for _, fromValue := range fromValues {
		criteria := &imap.SearchCriteria{
			Header: []imap.SearchCriteriaHeaderField{
				{Key: "From", Value: fromValue},
			},
		}

		data, err := c.UIDSearch(criteria, nil).Wait()
		if err != nil {
			errs = append(errs, fmt.Sprintf("From=%q: %v", fromValue, err))
			continue
		}

		success = true
		if data != nil {
			uids := dedupeAndSortUIDs(data.AllUIDs())
			if len(uids) > 0 {
				// Prefer exact sender first; stop early when matched.
				return uids, nil
			}
		}
	}

	if !success {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return []imap.UID{}, nil
}

func dedupeAndSortUIDs(uids []imap.UID) []imap.UID {
	if len(uids) == 0 {
		return []imap.UID{}
	}

	uidSet := make(map[imap.UID]struct{}, len(uids))
	deduped := make([]imap.UID, 0, len(uids))
	for _, uid := range uids {
		if _, exists := uidSet[uid]; exists {
			continue
		}
		uidSet[uid] = struct{}{}
		deduped = append(deduped, uid)
	}
	sort.Slice(deduped, func(i, j int) bool { return deduped[i] < deduped[j] })
	return deduped
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

	if code := extractNumericVerificationCode(subject); code != "" {
		return code, nil
	}
	return extractNumericVerificationCode(body), nil
}

type graphMessage struct {
	ID               string          `json:"id"`
	Subject          string          `json:"subject"`
	ReceivedDateTime time.Time       `json:"receivedDateTime"`
	BodyPreview      string          `json:"bodyPreview"`
	From             *graphRecipient `json:"from"`
}

type graphRecipient struct {
	EmailAddress graphEmailAddress `json:"emailAddress"`
}

type graphEmailAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type graphResponse struct {
	Value []graphMessage `json:"value"`
}

func (o *OutlookProvider) fetchViaGraph(ctx context.Context, timeoutSec int) (string, error) {
	accessToken, _, err := o.getAccessToken("https://graph.microsoft.com/.default")
	if err != nil {
		return "", fmt.Errorf("get Graph token: %w", err)
	}

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	triedMessageIDs := make(map[string]struct{})
	maxAge := 10 * time.Minute

	tryOnce := func() (string, error) {
		messages, err := o.queryGraphMessages(accessToken)
		if err != nil {
			return "", nil
		}

		openAIMessages := filterGraphMessagesForOpenAI(messages)
		otp, messageID := findOTPFromGraphMessages(openAIMessages, time.Now(), maxAge, triedMessageIDs)
		if otp == "" {
			return "", nil
		}
		if err := o.deleteGraphMessageMaybe(accessToken, messageID); err != nil {
			return "", err
		}
		return otp, nil
	}

	if otp, err := tryOnce(); err != nil {
		return "", err
	} else if otp != "" {
		return otp, nil
	}

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled")
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("timeout")
			}

			otp, err := tryOnce()
			if err != nil {
				return "", err
			}
			if otp != "" {
				return otp, nil
			}
		}
	}
}

func (o *OutlookProvider) queryGraphMessages(token string) ([]graphMessage, error) {
	baseURL := "https://graph.microsoft.com/v1.0/me/mailFolders/inbox/messages"
	params := url.Values{
		"$select":  {"id,subject,bodyPreview,from,receivedDateTime"},
		"$orderby": {"receivedDateTime desc"},
		"$top":     {"25"},
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

func (o *OutlookProvider) deleteGraphMessageMaybe(token, messageID string) error {
	if !o.graphDelete || messageID == "" {
		return nil
	}
	if err := o.deleteGraphMessage(token, messageID); err != nil {
		if o.graphDeleteStrict {
			return fmt.Errorf("delete OTP message (id=%s): %w", messageID, err)
		}
		return nil
	}
	return nil
}

func (o *OutlookProvider) deleteGraphMessage(token, messageID string) error {
	escapedID := url.PathEscape(messageID)
	endpoint := "https://graph.microsoft.com/v1.0/me/messages/" + escapedID

	req, err := http.NewRequest("DELETE", endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("Graph API error: %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func extractOTPFromGraphMessage(msg graphMessage) string {
	if code := extractNumericVerificationCode(msg.Subject); code != "" {
		return code
	}
	return extractNumericVerificationCode(msg.BodyPreview)
}

func findOTPFromGraphMessages(messages []graphMessage, now time.Time, maxAge time.Duration, triedMessageIDs map[string]struct{}) (otp string, messageID string) {
	cutoff := now.Add(-maxAge)
	for _, msg := range messages {
		if msg.ID == "" {
			continue
		}
		if msg.ReceivedDateTime.IsZero() || msg.ReceivedDateTime.Before(cutoff) {
			continue
		}
		if _, ok := triedMessageIDs[msg.ID]; ok {
			continue
		}
		triedMessageIDs[msg.ID] = struct{}{}

		if code := extractOTPFromGraphMessage(msg); code != "" {
			return code, msg.ID
		}
	}
	return "", ""
}

var numericCodePattern = regexp.MustCompile(`\b(\d{6})\b`)

func extractNumericVerificationCode(text string) string {
	if text == "" {
		return ""
	}
	if m := numericCodePattern.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	return ""
}

func filterGraphMessagesForOpenAI(messages []graphMessage) []graphMessage {
	result := make([]graphMessage, 0, len(messages))
	for _, msg := range messages {
		if isOpenAIGraphMessage(msg) {
			result = append(result, msg)
		}
	}
	return result
}

func isOpenAIGraphMessage(msg graphMessage) bool {
	if msg.From != nil {
		address := strings.ToLower(strings.TrimSpace(msg.From.EmailAddress.Address))
		if strings.Contains(address, "openai.com") {
			return true
		}
	}
	subject := strings.ToLower(msg.Subject)
	return strings.Contains(subject, "openai")
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

func parseBoolDefault(v string, defaultValue bool) bool {
	if v == "" {
		return defaultValue
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return defaultValue
	}
}
