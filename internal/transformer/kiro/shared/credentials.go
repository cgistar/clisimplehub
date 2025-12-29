package shared

import "time"

// KiroCredentials represents credentials loaded from kiro-auth-token.json.
type KiroCredentials struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ProfileArn   string    `json:"profileArn"`
	ExpiresAt    time.Time `json:"expiresAt"`
	Region       string    `json:"region,omitempty"`
	AuthMethod   string    `json:"authMethod,omitempty"`
	Provider     string    `json:"provider,omitempty"`
}

// credentialsJSON is used for JSON unmarshaling with string expiresAt.
type credentialsJSON struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresAt    string `json:"expiresAt"`
	Region       string `json:"region,omitempty"`
	AuthMethod   string `json:"authMethod,omitempty"`
	Provider     string `json:"provider,omitempty"`
}
