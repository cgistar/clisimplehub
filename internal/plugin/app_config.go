package plugin

import (
	"strings"
	"sync"
)

var (
	appConfigGetterMu sync.RWMutex
	appConfigGetter   func(key string) (string, error)
)

func SetConfigGetter(getter func(key string) (string, error)) {
	appConfigGetterMu.Lock()
	defer appConfigGetterMu.Unlock()
	appConfigGetter = getter
}

func GetConfigValue(key string) (string, error) {
	appConfigGetterMu.RLock()
	getter := appConfigGetter
	appConfigGetterMu.RUnlock()
	if getter == nil {
		return "", nil
	}
	return getter(key)
}

func GetAppProxyURL() string {
	value, err := GetConfigValue("proxyUrl")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func GetAppClashPath() string {
	value, err := GetConfigValue("clashPath")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
