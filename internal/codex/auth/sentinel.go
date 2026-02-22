package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	sentinelMaxAttempts = 500000
	sentinelErrorPrefix = "wQ8Lk5FbGpA2NcR9dShT6gYjU7VxZ4D"
	sentinelURL         = "https://sentinel.openai.com/backend-api/sentinel/req"
	sentinelSDKURL      = "https://sentinel.openai.com/sentinel/20260124ceb8/sdk.js"
	sentinelReferer     = "https://sentinel.openai.com/backend-api/sentinel/frame.html"
)

type SentinelTokenGenerator struct {
	DeviceID        string
	UserAgent       string
	requirementSeed string
	sid             string
}

type SentinelChallengeResp struct {
	Token       string                `json:"token"`
	ProofOfWork *SentinelPoWChallenge `json:"proofofwork"`
}

type SentinelPoWChallenge struct {
	Required   bool   `json:"required"`
	Seed       string `json:"seed"`
	Difficulty string `json:"difficulty"`
}

func NewSentinelTokenGenerator(deviceID, userAgent string) *SentinelTokenGenerator {
	return &SentinelTokenGenerator{
		DeviceID:        deviceID,
		UserAgent:       userAgent,
		requirementSeed: fmt.Sprintf("%f", rand.Float64()),
		sid:             uuid.New().String(),
	}
}

func fnv1a32(text string) string {
	h := uint32(2166136261)
	for _, ch := range text {
		h ^= uint32(ch)
		h = (h * 16777619) & 0xFFFFFFFF
	}
	h ^= (h >> 16)
	h = (h * 2246822507) & 0xFFFFFFFF
	h ^= (h >> 13)
	h = (h * 3266489909) & 0xFFFFFFFF
	h ^= (h >> 16)
	h &= 0xFFFFFFFF
	return fmt.Sprintf("%08x", h)
}

func (g *SentinelTokenGenerator) getConfig() []any {
	nowStr := time.Now().UTC().Format("Mon Jan 02 2006 15:04:05 GMT+0000 (Coordinated Universal Time)")
	perfNow := rand.Float64()*49000 + 1000
	timeOrigin := float64(time.Now().UnixMilli()) - perfNow

	navProps := []string{
		"vendorSub", "productSub", "vendor", "maxTouchPoints",
		"scheduling", "userActivation", "doNotTrack", "geolocation",
		"connection", "plugins", "mimeTypes", "pdfViewerEnabled",
		"webkitTemporaryStorage", "webkitPersistentStorage",
		"hardwareConcurrency", "cookieEnabled", "credentials",
		"mediaDevices", "permissions", "locks", "ink",
	}
	navProp := navProps[rand.Intn(len(navProps))]
	navVal := navProp + "-undefined"

	docProps := []string{"location", "implementation", "URL", "documentURI", "compatMode"}
	globalProps := []string{"Object", "Function", "Array", "Number", "parseFloat", "undefined"}
	webglValues := []int{4, 8, 12, 16}

	return []any{
		"1920x1080",
		nowStr,
		4294705152,
		rand.Float64(),
		g.UserAgent,
		sentinelSDKURL,
		nil,
		nil,
		"en-US",
		"en-US,en",
		rand.Float64(),
		navVal,
		docProps[rand.Intn(len(docProps))],
		globalProps[rand.Intn(len(globalProps))],
		perfNow,
		g.sid,
		"",
		webglValues[rand.Intn(len(webglValues))],
		timeOrigin,
	}
}

func sentinelBase64Encode(data []any) string {
	raw, _ := json.Marshal(data)
	return base64.StdEncoding.EncodeToString(raw)
}

func (g *SentinelTokenGenerator) runCheck(startTime time.Time, seed, difficulty string, config []any, nonce int) string {
	config[3] = nonce
	config[9] = int(time.Since(startTime).Milliseconds())
	data := sentinelBase64Encode(config)
	hashHex := fnv1a32(seed + data)
	diffLen := len(difficulty)
	if diffLen > 0 {
		// Guard against malformed difficulty that exceeds hash length
		if diffLen > len(hashHex) {
			diffLen = len(hashHex)
		}
		if hashHex[:diffLen] <= difficulty {
			return data + "~S"
		}
	}
	return ""
}

func (g *SentinelTokenGenerator) GeneratePoWToken(seed, difficulty string) string {
	if difficulty == "" {
		difficulty = "0"
	}
	startTime := time.Now()
	config := g.getConfig()

	for i := 0; i < sentinelMaxAttempts; i++ {
		result := g.runCheck(startTime, seed, difficulty, config, i)
		if result != "" {
			return "gAAAAAB" + result
		}
	}
	nilEncoded := sentinelBase64Encode([]any{nil})
	return "gAAAAAB" + sentinelErrorPrefix + nilEncoded
}

func (g *SentinelTokenGenerator) GenerateRequirementsToken() string {
	config := g.getConfig()
	config[3] = 1
	config[9] = int(rand.Float64()*45 + 5)
	data := sentinelBase64Encode(config)
	return "gAAAAAC" + data
}

func FetchSentinelChallenge(client *http.Client, deviceID, flow, userAgent, secChUA string) (*SentinelChallengeResp, error) {
	gen := NewSentinelTokenGenerator(deviceID, userAgent)
	reqBody := map[string]string{
		"p":    gen.GenerateRequirementsToken(),
		"id":   deviceID,
		"flow": flow,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequest("POST", sentinelURL, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("Referer", sentinelReferer)
	req.Header.Set("Origin", "https://sentinel.openai.com")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("sec-ch-ua", secChUA)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sentinel challenge failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var challenge SentinelChallengeResp
	if err := json.NewDecoder(resp.Body).Decode(&challenge); err != nil {
		return nil, fmt.Errorf("sentinel challenge parse: %w", err)
	}
	return &challenge, nil
}

func BuildSentinelToken(client *http.Client, deviceID, flow, userAgent, secChUA string) (string, error) {
	challenge, err := FetchSentinelChallenge(client, deviceID, flow, userAgent, secChUA)
	if err != nil {
		return "", err
	}

	cValue := challenge.Token
	if cValue == "" {
		return "", fmt.Errorf("sentinel challenge returned empty token")
	}

	gen := NewSentinelTokenGenerator(deviceID, userAgent)
	var pValue string
	if challenge.ProofOfWork != nil && challenge.ProofOfWork.Required && challenge.ProofOfWork.Seed != "" {
		pValue = gen.GeneratePoWToken(challenge.ProofOfWork.Seed, challenge.ProofOfWork.Difficulty)
	} else {
		pValue = gen.GenerateRequirementsToken()
	}

	result := map[string]string{
		"p":    pValue,
		"t":    "",
		"c":    cValue,
		"id":   deviceID,
		"flow": flow,
	}
	encoded, _ := json.Marshal(result)
	return string(encoded), nil
}
