package clashplugin

import (
	"os"
	"strings"

	coreplugin "clisimplehub/internal/plugin"
)

func GetConfiguredClashPath() string {
	return strings.TrimSpace(coreplugin.GetAppClashPath())
}

func HasUsableExternalBinary() bool {
	_, ok := usableExternalBinaryPath()
	return ok
}

func HasEmbeddedRuntime() bool {
	return hasEmbeddedRuntime()
}

func ShouldShowClashUI() bool {
	return HasEmbeddedRuntime() || HasUsableExternalBinary()
}

func CanStartManagedRuntime() bool {
	return HasUsableExternalBinary() || HasEmbeddedRuntime()
}

func usableExternalBinaryPath() (string, bool) {
	path := GetConfiguredClashPath()
	if path == "" {
		return "", false
	}

	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}

	return path, true
}
