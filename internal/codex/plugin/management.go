package codexplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	codex "clisimplehub/internal/codex"
	codexAuth "clisimplehub/internal/codex/auth"
	codexShared "clisimplehub/internal/codex/shared"
	"clisimplehub/internal/plugin"
)

type oauthSession struct {
	PKCE      *codexAuth.PKCECodes
	ProxyURL  string
	ResultCh  chan *codexAuth.OAuthResult
	CreatedAt time.Time
	state     string
	done      chan struct{}
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	login     *codexAuth.CodexLoginResult
	once      sync.Once
	outcome   oauthOutcome
}

type oauthOutcome struct {
	err       error
	message   string
	email     string
	accountID string
	planType  string
}

type oauthLoopback struct {
	server   *http.Server
	listener net.Listener
	state    string
	host     string
	port     int
}

var (
	oauthSessions     = make(map[string]*oauthSession)
	oauthSessionsMu   sync.Mutex
	oauthURLAttemptMu sync.Mutex
	sessionTTL        = codexAuth.OAuthLoginTimeout

	webUILoginMu      sync.Mutex
	webUILoginCancel  context.CancelFunc
	webUILoginCleanup func()
	webUILoginGen     uint64
	saveAccountMu     sync.Mutex

	oauthLoopbackMu sync.Mutex
	oauthLoopbackLn *oauthLoopback
	oauthOutcomes   sync.Map
)

var errOAuthLoginCancelled = errors.New("登录已取消")

func storeSession(state string, s *oauthSession) {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	now := time.Now()
	for k, v := range oauthSessions {
		if now.Sub(v.CreatedAt) > sessionTTL {
			delete(oauthSessions, k)
		}
	}
	oauthSessions[state] = s
}

func popSession(state string) *oauthSession {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	s, ok := oauthSessions[state]
	if !ok {
		return nil
	}
	delete(oauthSessions, state)
	if time.Since(s.CreatedAt) > sessionTTL {
		return nil
	}
	return s
}

func getSession(state string) *oauthSession {
	oauthSessionsMu.Lock()
	defer oauthSessionsMu.Unlock()
	s, ok := oauthSessions[state]
	if !ok {
		return nil
	}
	if time.Since(s.CreatedAt) > sessionTTL {
		delete(oauthSessions, state)
		return nil
	}
	return s
}

func (p *CodexPlugin) handleAuthURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	authURL, state, resultCh, err := beginOAuthURLSession()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	go p.waitForOAuthCallback(state, resultCh)

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"url":    authURL,
		"state":  state,
	})
}

func beginOAuthURLSession() (string, string, chan *codexAuth.OAuthResult, error) {
	pkce, err := codexAuth.GeneratePKCECodes()
	if err != nil {
		return "", "", nil, fmt.Errorf("PKCE generation failed")
	}

	state, err := codexAuth.GenerateState()
	if err != nil {
		return "", "", nil, fmt.Errorf("state generation failed")
	}

	resultCh := make(chan *codexAuth.OAuthResult, 1)
	storeSession(state, &oauthSession{
		PKCE:      pkce,
		ProxyURL:  resolveCodexOAuthProxyURL(),
		ResultCh:  resultCh,
		CreatedAt: time.Now(),
	})
	return codexAuth.BuildAuthURL(state, pkce), state, resultCh, nil
}

func (d *desktopFacade) StartOAuthURLLogin(ctx context.Context) (json.RawMessage, error) {
	return d.startOAuthURLLogin(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(codexAuth.OAuthPort)))
}

func (d *desktopFacade) startOAuthURLLogin(ctx context.Context, listenAddr string) (json.RawMessage, error) {
	oauthURLAttemptMu.Lock()
	defer oauthURLAttemptMu.Unlock()
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	// 同一进程只能听一个 1455。新的登录会先关掉上一次的端口。
	stopOAuthLoopback("")

	authURL, state, _, err := beginOAuthURLSession()
	if err != nil {
		return nil, fmt.Errorf("生成登录链接失败：%w", err)
	}
	sess := getSession(state)
	if sess == nil {
		return nil, fmt.Errorf("生成登录链接失败")
	}
	sess.state = state
	sess.done = make(chan struct{})

	host, port, err := listenOAuthLoopback(state, listenAddr)
	if err != nil {
		close(sess.done)
		_ = popSession(state)
		return nil, fmt.Errorf("启动 %d 回调端口失败：%w", codexAuth.OAuthPort, err)
	}
	sess.ctx, sess.cancel = context.WithDeadline(context.Background(), sess.CreatedAt.Add(sessionTTL))
	go watchOAuthURLLogin(sess.ctx, state, sess)

	return json.Marshal(map[string]any{
		"url":          authURL,
		"state":        state,
		"callbackHost": host,
		"callbackPort": port,
		"listening":    true,
	})
}

func (d *desktopFacade) OAuthURLLoginStatus(state string) (json.RawMessage, error) {
	state = strings.TrimSpace(state)
	host, port, listening := oauthLoopbackInfo(state)
	if sess := getSession(state); sess != nil && sess.done != nil {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		select {
		case <-sess.done:
			return marshalOAuthOutcome(sess.outcome)
		default:
			return json.Marshal(map[string]any{
				"status":       "pending",
				"error":        oauthErrorMessage(sess.outcome.err),
				"listening":    listening,
				"callbackHost": host,
				"callbackPort": port,
			})
		}
	}
	if outcome, ok := loadOAuthOutcome(state); ok {
		return marshalOAuthOutcome(outcome)
	}
	return json.Marshal(map[string]any{
		"status":    "closed",
		"listening": false,
	})
}

func (d *desktopFacade) CompleteOAuthURLLogin(ctx context.Context, callbackURL string) (json.RawMessage, error) {
	code, state, errorMessage, err := parseOAuthCallbackURL(callbackURL)
	if err != nil {
		return nil, err
	}
	if state == "" {
		return nil, fmt.Errorf("缺少 state，请粘贴完整的回调 URL")
	}

	sess := getSession(state)
	if sess == nil || sess.done == nil {
		if outcome, ok := loadOAuthOutcome(state); ok && outcome.err == nil && outcome.message != "" {
			return marshalOAuthOutcome(outcome)
		}
		return nil, fmt.Errorf("登录会话不存在或已过期，请重新生成登录链接")
	}
	if err := sess.finish(sess.ctx, &codexAuth.OAuthResult{Code: code, State: state, Error: errorMessage}); err != nil {
		return nil, err
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	return marshalOAuthOutcome(sess.outcome)
}

func (d *desktopFacade) CancelOAuthURLLogin(state string) error {
	state = strings.TrimSpace(state)
	if state == "" {
		stopOAuthLoopback("")
		return nil
	}
	// 关闭窗口仅停止监听；旧链接对应的 PKCE 保留至超时或登录成功。
	stopOAuthLoopback(state)
	return nil
}

func watchOAuthURLLogin(ctx context.Context, state string, sess *oauthSession) {
	defer stopOAuthLoopback(state)
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case result := <-sess.ResultCh:
			_ = sess.finish(ctx, result)
		case <-sess.done:
			return
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				sess.fail(fmt.Errorf("登录超时，%d 端口已关闭", codexAuth.OAuthPort))
			} else {
				sess.fail(errOAuthLoginCancelled)
			}
			return
		}
	}
}

func oauthErrorMessage(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func (s *oauthSession) finish(ctx context.Context, result *codexAuth.OAuthResult) error {
	// 多个缓存 state 共用一个登录结果，避免并发回调重复保存或相互清理。
	oauthURLAttemptMu.Lock()
	defer oauthURLAttemptMu.Unlock()
	select {
	case <-s.done:
		return s.outcome.err
	default:
	}
	if err := s.tryFinish(ctx, result); err != nil {
		return err
	}
	completeOAuthURLSessions(s.outcome)
	return nil
}

func completeOAuthURLSessions(outcome oauthOutcome) {
	oauthSessionsMu.Lock()
	var sessions []*oauthSession
	for _, sess := range oauthSessions {
		if sess.done != nil {
			sessions = append(sessions, sess)
		}
	}
	oauthSessionsMu.Unlock()
	for _, sess := range sessions {
		sess.mu.Lock()
		sess.once.Do(func() {
			sess.outcome = outcome
			sess.rememberOutcome()
			_ = popSession(sess.state)
			close(sess.done)
		})
		if sess.cancel != nil {
			sess.cancel()
		}
		sess.mu.Unlock()
	}
}

func (s *oauthSession) tryFinish(ctx context.Context, result *codexAuth.OAuthResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return s.outcome.err
	default:
	}
	if ctx == nil {
		ctx = context.Background()
	}
	attempt := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if result == nil || result.State != s.state {
			return fmt.Errorf("state mismatch")
		}
		if strings.TrimSpace(result.Error) != "" {
			return fmt.Errorf("OAuth 登录失败：%s", strings.TrimSpace(result.Error))
		}
		if strings.TrimSpace(result.Code) == "" {
			return fmt.Errorf("回调 URL 中没有 authorization code")
		}
		// Token 已换取成功时，保存失败后的重试不能再次消费授权码。
		if s.login == nil {
			login, err := codexAuth.ExchangeCodeForTokens(ctx, strings.TrimSpace(result.Code), s.PKCE, s.ProxyURL)
			if err != nil {
				return fmt.Errorf("换取 Token 失败：%w", err)
			}
			s.login = login
		}
		cp := currentCodexPlugin()
		if cp == nil {
			return fmt.Errorf("codex-accounts 插件不可用")
		}
		if err := cp.saveOAuthAccount(s.login); err != nil {
			return fmt.Errorf("保存账号失败：%w", err)
		}
		return nil
	}
	if err := attempt(); err != nil {
		s.outcome.err = err
		return err
	}
	s.outcome = oauthOutcome{message: "OpenAI 登录成功", email: s.login.Email, accountID: s.login.AccountID, planType: s.login.PlanType}
	s.once.Do(func() {
		s.rememberOutcome()
		_ = popSession(s.state)
		close(s.done)
		if s.cancel != nil {
			s.cancel()
		}
	})
	return nil
}

func (s *oauthSession) fail(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = errOAuthLoginCancelled
	}
	s.once.Do(func() {
		defer close(s.done)
		defer func() { _ = popSession(s.state) }()
		s.outcome.err = err
		if !errors.Is(err, errOAuthLoginCancelled) {
			s.rememberOutcome()
		}
	})
}

func (s *oauthSession) rememberOutcome() {
	if s == nil || s.state == "" {
		return
	}
	oauthOutcomes.Store(s.state, s.outcome)
}

func loadOAuthOutcome(state string) (oauthOutcome, bool) {
	value, ok := oauthOutcomes.Load(state)
	if !ok {
		return oauthOutcome{}, false
	}
	outcome, ok := value.(oauthOutcome)
	return outcome, ok
}

func deliverOAuthResult(sess *oauthSession, result *codexAuth.OAuthResult) {
	if sess == nil || result == nil || sess.ResultCh == nil {
		return
	}
	select {
	case sess.ResultCh <- result:
	default:
	}
}

func marshalOAuthOutcome(outcome oauthOutcome) (json.RawMessage, error) {
	if outcome.err != nil {
		return json.Marshal(map[string]any{
			"status":    "error",
			"listening": false,
			"error":     outcome.err.Error(),
		})
	}
	return json.Marshal(map[string]any{
		"status":    "success",
		"listening": false,
		"message":   outcome.message,
		"email":     outcome.email,
		"accountId": outcome.accountID,
		"planType":  outcome.planType,
	})
}

func listenOAuthLoopback(state, listenAddr string) (string, int, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return "", 0, err
	}
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		_ = ln.Close()
		return "", 0, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", handleOAuthLoopbackCallback)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      40 * time.Second,
	}

	oauthLoopbackMu.Lock()
	oauthLoopbackLn = &oauthLoopback{
		server:   server,
		listener: ln,
		state:    state,
		host:     host,
		port:     port,
	}
	oauthLoopbackMu.Unlock()

	go func() {
		_ = server.Serve(ln)
	}()
	return host, port, nil
}

func handleOAuthLoopbackCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	code := strings.TrimSpace(query.Get("code"))
	state := strings.TrimSpace(query.Get("state"))
	errorMessage := strings.TrimSpace(query.Get("error"))
	if errorMessage == "" {
		errorMessage = strings.TrimSpace(query.Get("error_description"))
	}
	if code == "" && errorMessage == "" {
		http.Error(w, "No authorization code received", http.StatusBadRequest)
		return
	}
	sess := getSession(state)
	if sess == nil {
		http.Error(w, "login session not found or expired", http.StatusNotFound)
		return
	}
	result := &codexAuth.OAuthResult{Code: code, State: state, Error: errorMessage}
	if sess.done != nil {
		if err := sess.finish(sess.ctx, result); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		deliverOAuthResult(sess, result)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<h1>Callback received</h1><p>Check cliSimpleHub for the login result.</p>"))
}

func oauthLoopbackInfo(state string) (string, int, bool) {
	oauthLoopbackMu.Lock()
	defer oauthLoopbackMu.Unlock()
	if oauthLoopbackLn == nil || oauthLoopbackLn.state != state {
		return "127.0.0.1", codexAuth.OAuthPort, false
	}
	return oauthLoopbackLn.host, oauthLoopbackLn.port, true
}

func stopOAuthLoopback(state string) {
	oauthLoopbackMu.Lock()
	current := oauthLoopbackLn
	if current == nil || (state != "" && current.state != state) {
		oauthLoopbackMu.Unlock()
		return
	}
	oauthLoopbackLn = nil
	listener := current.listener
	server := current.server
	oauthLoopbackMu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	if server != nil {
		ctx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(ctx)
	}
}

func parseOAuthCallbackURL(raw string) (code string, state string, errorMessage string, err error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)
	if raw == "" {
		return "", "", "", fmt.Errorf("请粘贴回调 URL")
	}
	if !strings.Contains(raw, "://") {
		query := strings.TrimPrefix(raw, "?")
		if strings.Contains(query, "code=") || strings.Contains(query, "state=") || strings.Contains(query, "error=") {
			raw = "http://localhost:1455/auth/callback?" + query
		}
	}

	u, parseErr := url.Parse(raw)
	if parseErr != nil || u == nil || u.Scheme == "" || u.Host == "" {
		return "", "", "", fmt.Errorf("callback URL 无效")
	}
	query := u.Query()
	code = strings.TrimSpace(query.Get("code"))
	state = strings.TrimSpace(query.Get("state"))
	errorMessage = strings.TrimSpace(query.Get("error"))
	if errorMessage == "" {
		errorMessage = strings.TrimSpace(query.Get("error_description"))
	}
	return code, state, errorMessage, nil
}

func resolveCodexOAuthProxyURL() string {
	cp := currentCodexPlugin()
	if cp == nil {
		return resolveProxyURL("", "")
	}
	cp.mu.RLock()
	codexJsonPath := cp.codexJsonPath
	cp.mu.RUnlock()
	return resolveProxyURL("", codexJsonPath)
}

func currentCodexPlugin() *CodexPlugin {
	p := plugin.ByName("codex-accounts")
	cp, _ := p.(*CodexPlugin)
	return cp
}

func (p *CodexPlugin) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var body struct {
		Provider    string `json:"provider"`
		RedirectURL string `json:"redirect_url"`
		State       string `json:"state"`
		Code        string `json:"code"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	code := body.Code
	state := body.State
	errorMessage := body.Error

	if body.RedirectURL != "" {
		if u, err := url.Parse(body.RedirectURL); err == nil {
			if c := u.Query().Get("code"); c != "" {
				code = c
			}
			if s := u.Query().Get("state"); s != "" {
				state = s
			}
			if e := u.Query().Get("error"); e != "" {
				errorMessage = e
			} else if e := u.Query().Get("error_description"); e != "" {
				errorMessage = e
			}
		}
	}

	if provider := strings.TrimSpace(body.Provider); provider != "" && !strings.EqualFold(provider, "codex") && !strings.EqualFold(provider, "openai") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported provider"})
		return
	}
	if strings.TrimSpace(state) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing state"})
		return
	}
	if strings.TrimSpace(code) == "" && strings.TrimSpace(errorMessage) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing authorization code"})
		return
	}

	sess := getSession(state)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found or expired"})
		return
	}

	result := &codexAuth.OAuthResult{
		Code:  strings.TrimSpace(code),
		State: strings.TrimSpace(state),
		Error: strings.TrimSpace(errorMessage),
	}
	select {
	case sess.ResultCh <- result:
	default:
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (p *CodexPlugin) waitForOAuthCallback(state string, resultCh <-chan *codexAuth.OAuthResult) {
	var oauthResult *codexAuth.OAuthResult
	select {
	case oauthResult = <-resultCh:
	case <-time.After(5 * time.Minute):
		_ = popSession(state)
		log.Printf("[codex-auth] OAuth callback timeout for state %s", state)
		return
	}

	if oauthResult.Error != "" {
		_ = popSession(state)
		log.Printf("[codex-auth] OAuth callback error: %s", oauthResult.Error)
		return
	}

	if oauthResult.State != state {
		_ = popSession(state)
		log.Printf("[codex-auth] OAuth state mismatch")
		return
	}

	sess := popSession(state)
	if sess == nil {
		log.Printf("[codex-auth] OAuth session expired before token exchange")
		return
	}

	result, err := codexAuth.ExchangeCodeForTokens(context.Background(), oauthResult.Code, sess.PKCE, sess.ProxyURL)
	if err != nil {
		log.Printf("[codex-auth] token exchange failed: %v", err)
		return
	}
	if err := p.saveOAuthAccount(result); err != nil {
		log.Printf("[codex-auth] save account failed: %v", err)
	}
}

func (p *CodexPlugin) saveOAuthAccount(result *codexAuth.CodexLoginResult) error {
	saveAccountMu.Lock()
	defer saveAccountMu.Unlock()

	if strings.TrimSpace(result.AccountID) == "" {
		return fmt.Errorf("accountId is required from OAuth login")
	}
	if strings.TrimSpace(result.Email) == "" {
		return fmt.Errorf("email is required from OAuth login")
	}
	localID := codexShared.GenerateCodexLocalID(result.AccountID, result.Email)
	if localID == "" {
		return fmt.Errorf("account id is required from OAuth login")
	}

	store := p.GetAccountStore()
	if store == nil {
		return fmt.Errorf("account store not initialized")
	}

	p.mu.RLock()
	codexJsonPath := p.codexJsonPath
	p.mu.RUnlock()

	ctx := context.Background()
	existing, _ := store.GetByID(ctx, localID)

	var expiresAt time.Time
	if result.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, result.ExpiresAt); err == nil {
			expiresAt = t
		}
	}

	if existing != nil {
		existing.RefreshToken = result.RefreshToken
		existing.AccessToken = result.AccessToken
		existing.IDToken = result.IDToken
		existing.Email = result.Email
		existing.PlanType = result.PlanType
		existing.Status = codexShared.CodexStatusValid
		existing.ExpiresAt = expiresAt
		existing.CooldownUntil = time.Time{}
		existing.CooldownReason = ""
		if err := store.Update(ctx, existing); err != nil {
			return fmt.Errorf("update account: %w", err)
		}
	} else {
		now := time.Now()
		account := &codexShared.CodexAccount{
			ID:           localID,
			RefreshToken: result.RefreshToken,
			AccessToken:  result.AccessToken,
			IDToken:      result.IDToken,
			AccountID:    result.AccountID,
			Email:        result.Email,
			PlanType:     result.PlanType,
			Enabled:      true,
			Websockets:   true,
			Status:       codexShared.CodexStatusValid,
			ExpiresAt:    expiresAt,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := store.Insert(ctx, account); err != nil {
			return fmt.Errorf("insert account: %w", err)
		}
	}

	// Update active in codex.json if needed
	mc, _ := codexShared.LoadCodexMultiConfig(codexJsonPath)
	if mc == nil {
		mc = &codexShared.CodexMultiConfig{}
	}
	if mc.ActiveAccountID == "" {
		mc.ActiveAccountID = localID
		_ = codexShared.SaveCodexMultiConfig(codexJsonPath, mc)
	}

	if svc := p.GetService(); svc != nil {
		svc.RemoveAuthManager(localID)
		svc.ensureCodexEndpoint()
	}

	if pool := codex.GetPool(); pool != nil {
		pool.Reload()
	}

	return nil
}

func resolveProxyURL(requested, codexJsonPath string) string {
	if u := strings.TrimSpace(requested); u != "" {
		return u
	}
	if u := plugin.GetAppProxyURL(); u != "" {
		return u
	}
	if codexJsonPath != "" {
		if mc, err := codexShared.LoadCodexMultiConfig(codexJsonPath); err == nil && mc != nil {
			if u := strings.TrimSpace(mc.ProxyUrl); u != "" {
				return u
			}
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
