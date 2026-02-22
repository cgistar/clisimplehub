package auth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestSentinelPoW(t *testing.T) {
	gen := NewSentinelTokenGenerator("test-device-id", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	reqToken := gen.GenerateRequirementsToken()
	if !strings.HasPrefix(reqToken, "gAAAAAC") {
		t.Errorf("requirements token should start with gAAAAAC, got: %s", reqToken[:20])
	}

	powToken := gen.GeneratePoWToken("test-seed", "f")
	if !strings.HasPrefix(powToken, "gAAAAAB") {
		t.Errorf("PoW token should start with gAAAAAB, got: %s", powToken[:20])
	}
}

func TestFNV1a32(t *testing.T) {
	// Verify deterministic output
	h1 := fnv1a32("hello")
	h2 := fnv1a32("hello")
	if h1 != h2 {
		t.Errorf("fnv1a32 should be deterministic: %s != %s", h1, h2)
	}
	if len(h1) != 8 {
		t.Errorf("fnv1a32 should return 8-char hex: %s", h1)
	}

	// Different inputs should produce different outputs
	h3 := fnv1a32("world")
	if h1 == h3 {
		t.Errorf("different inputs should produce different hashes")
	}
}

func TestRandomChromeVersion(t *testing.T) {
	// Test that randomChromeVersion returns valid data
	profile, fullVer, ua := randomChromeVersion()

	if profile.Major == 0 {
		t.Error("profile.Major should not be 0")
	}
	if profile.Build == 0 {
		t.Error("profile.Build should not be 0")
	}
	if profile.SecChUA == "" {
		t.Error("profile.SecChUA should not be empty")
	}
	if fullVer == "" {
		t.Error("fullVer should not be empty")
	}
	if ua == "" {
		t.Error("userAgent should not be empty")
	}

	// Verify version format (e.g., "146.0.7540.87")
	parts := strings.Split(fullVer, ".")
	if len(parts) != 4 {
		t.Errorf("fullVer should have 4 parts, got: %s", fullVer)
	}

	// Verify UA contains the version
	if !strings.Contains(ua, fullVer) {
		t.Errorf("UA should contain version %s, got: %s", fullVer, ua)
	}

	// Test randomness: call multiple times and verify we get different patch numbers
	versions := make(map[string]bool)
	for i := 0; i < 20; i++ {
		_, ver, _ := randomChromeVersion()
		versions[ver] = true
	}
	if len(versions) < 2 {
		t.Error("randomChromeVersion should produce different versions across multiple calls")
	}

	t.Logf("Sample output: profile=Chrome_%d, version=%s, ua=%s", profile.Major, fullVer, ua[:80])
}

// TestHeadlessLogin is a manual integration test that requires real credentials.
// Run: TEST_EMAIL=xxx TEST_PASSWORD=xxx go test ./internal/codex/auth/ -run TestHeadlessLogin -v -count=1
func TestHeadlessLogin(t *testing.T) {
	email := os.Getenv("TEST_EMAIL")
	password := os.Getenv("TEST_PASSWORD")
	if email == "" || password == "" {
		t.Skip("TEST_EMAIL and TEST_PASSWORD not set")
	}

	proxyURL := os.Getenv("TEST_PROXY")
	clientID := os.Getenv("TEST_CLIENT_ID")

	session, err := StartHeadlessLogin(context.Background(), &HeadlessLoginRequest{
		Email:    email,
		Password: password,
		ClientID: clientID,
		ProxyURL: proxyURL,
	})
	if err != nil {
		t.Fatalf("StartHeadlessLogin failed: %v", err)
	}

	if session.State() == StateError {
		t.Fatalf("Login error: %v", session.Error())
	}

	if session.State() == StateNeedOTP {
		const maxOTPAttempts = 3
		for attempt := 1; attempt <= maxOTPAttempts && session.State() == StateNeedOTP; attempt++ {
			code, err := readOTPCode(t, attempt)
			if err != nil {
				t.Fatalf("read OTP failed: %v", err)
			}
			if err := session.SubmitOTP(context.Background(), code); err != nil {
				if session.State() == StateNeedOTP && attempt < maxOTPAttempts {
					t.Logf("SubmitOTP attempt %d/%d failed: %v", attempt, maxOTPAttempts, err)
					continue
				}
				t.Fatalf("SubmitOTP failed: %v", err)
			}
		}
		if session.State() == StateNeedOTP {
			t.Fatalf("OTP is still required after %d attempts", maxOTPAttempts)
		}
	}

	if session.State() != StateSuccess {
		t.Fatalf("unexpected state: %d, error: %v", session.State(), session.Error())
	}

	result := session.Result()
	t.Logf("AccessToken: %s...", result.AccessToken[:40])
	t.Logf("IDToken: %s...", result.IDToken[:40])
	t.Logf("AccountID: %s", result.AccountID)
	t.Logf("RefreshToken: %s...", result.RefreshToken[:40])
	t.Logf("Email: %s", result.Email)
	t.Logf("PlanType: %s", result.PlanType)
	t.Logf("ExpiresAt: %s", result.ExpiresAt)
}

func readOTPCode(t *testing.T, attempt int) (string, error) {
	t.Helper()

	if code := strings.TrimSpace(os.Getenv("TEST_OTP")); code != "" {
		return code, nil
	}

	otpAttemptEnv := fmt.Sprintf("TEST_OTP_%d", attempt)
	if code := strings.TrimSpace(os.Getenv(otpAttemptEnv)); code != "" {
		return code, nil
	}

	input := io.Reader(os.Stdin)
	output := io.Writer(os.Stdout)
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		defer tty.Close()
		input = tty
		output = tty
	} else {
		t.Logf("open /dev/tty failed (%v), fallback to stdin", err)
	}

	fmt.Fprintf(output, "Enter OTP code (attempt %d): ", attempt)
	line, readErr := bufio.NewReader(input).ReadString('\n')
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", fmt.Errorf("read OTP input: %w", readErr)
	}

	code := strings.TrimSpace(line)
	if code == "" {
		return "", fmt.Errorf("empty OTP code (set TEST_OTP or type code in terminal)")
	}
	return code, nil
}
