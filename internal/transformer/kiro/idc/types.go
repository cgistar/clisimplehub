package idc

// RegisterRequest 注册 OIDC 客户端请求
type RegisterRequest struct {
	Region string
}

// RegisterResponse 注册 OIDC 客户端响应
type RegisterResponse struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// DeviceAuthRequest 设备授权请求
type DeviceAuthRequest struct {
	ClientId     string
	ClientSecret string
	Region       string
	StartUrl     string // 可选，为空则使用默认 Builder ID URL
}

// DeviceAuthResponse 设备授权响应
type DeviceAuthResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationUri         string `json:"verificationUri"`
	VerificationUriComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// PollTokenRequest 轮询 token 请求
type PollTokenRequest struct {
	ClientId     string
	ClientSecret string
	DeviceCode   string
	Region       string
}

// PollTokenResponse 轮询 token 响应
type PollTokenResponse struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	Error        string `json:"error,omitempty"`
}

// AuthCodeRegisterRequest Authorization Code Flow 客户端注册请求
type AuthCodeRegisterRequest struct {
	Region   string
	IssuerUrl string
}

// AuthCodeRegisterResponse Authorization Code Flow 客户端注册响应
type AuthCodeRegisterResponse struct {
	ClientId     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// AuthCodeTokenRequest Authorization Code Flow token 交换请求
type AuthCodeTokenRequest struct {
	Region       string
	ClientId     string
	ClientSecret string
	Code         string
	RedirectUri  string
	CodeVerifier string
}

// AuthCodeTokenResponse Authorization Code Flow token 交换响应
type AuthCodeTokenResponse struct {
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int    `json:"expiresIn,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	Error        string `json:"error,omitempty"`
}
