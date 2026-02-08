package shared

var defaultFilterTags = []string{"xaiartifact", "xai:tool_usage_card", "grok:render"}

const defaultUserAgent = `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36`

func boolPtr(b bool) *bool { return &b }

func DefaultSettings() GrokSettings {
	return GrokSettings{
		Chat: ChatSettings{
			Temporary:      boolPtr(true),
			DisableMemory:  boolPtr(true),
			FilterTags:     defaultFilterTags,
			Thinking:       boolPtr(false),
			DynamicStatsig: boolPtr(true),
		},
		Network: NetworkSettings{
			Timeout: 120,
		},
		Image: ImageSettings{
			ImageWs:               boolPtr(true),
			ImageWsNsfw:           boolPtr(true),
			ImageWsBlockedSeconds: 15,
			ImageWsFinalMinBytes:  100000,
			ImageWsMediumMinBytes: 30000,
		},
	}
}

func derefBool(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

func (s *GrokSettings) GetTemporary() bool {
	if s == nil {
		return true
	}
	return derefBool(s.Chat.Temporary, true)
}

func (s *GrokSettings) GetDisableMemory() bool {
	if s == nil {
		return true
	}
	return derefBool(s.Chat.DisableMemory, true)
}

func (s *GrokSettings) GetFilterTags() []string {
	if s == nil || len(s.Chat.FilterTags) == 0 {
		return defaultFilterTags
	}
	return s.Chat.FilterTags
}

func (s *GrokSettings) GetThinking() bool {
	if s == nil {
		return true
	}
	return derefBool(s.Chat.Thinking, true)
}

func (s *GrokSettings) GetDynamicStatsig() bool {
	if s == nil {
		return true
	}
	return derefBool(s.Chat.DynamicStatsig, true)
}

func (s *GrokSettings) GetUserAgent() string {
	if s != nil && s.Security.UserAgent != "" {
		return s.Security.UserAgent
	}
	return defaultUserAgent
}

func (s *GrokSettings) GetCfClearance() string {
	if s == nil {
		return ""
	}
	return s.Security.CfClearance
}

func (s *GrokSettings) GetTimeout() int {
	if s == nil || s.Network.Timeout <= 0 {
		return 120
	}
	return s.Network.Timeout
}

func (s *GrokSettings) GetImageWs() bool {
	if s == nil {
		return true
	}
	return derefBool(s.Image.ImageWs, true)
}

func (s *GrokSettings) GetImageWsNsfw() bool {
	if s == nil {
		return true
	}
	return derefBool(s.Image.ImageWsNsfw, true)
}

func (s *GrokSettings) GetImageWsBlockedSec() int {
	if s == nil || s.Image.ImageWsBlockedSeconds <= 0 {
		return 15
	}
	return s.Image.ImageWsBlockedSeconds
}

func (s *GrokSettings) GetImageWsFinalMin() int {
	if s == nil || s.Image.ImageWsFinalMinBytes <= 0 {
		return 100000
	}
	return s.Image.ImageWsFinalMinBytes
}

func (s *GrokSettings) GetImageWsMediumMin() int {
	if s == nil || s.Image.ImageWsMediumMinBytes <= 0 {
		return 30000
	}
	return s.Image.ImageWsMediumMinBytes
}
