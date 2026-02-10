//go:build !windows && !darwin

package main

// RegisterURLScheme is a no-op on non-Windows platforms
// URL scheme registration is handled by the OS on macOS/Linux
func RegisterURLScheme() error {
	// On macOS, URL schemes are registered via Info.plist (handled by Wails)
	// On Linux, URL schemes are registered via .desktop files
	return nil
}

// UnregisterURLScheme is a no-op on non-Windows platforms
func UnregisterURLScheme() error {
	return nil
}

// CleanupKiroProtocolIfNeeded is a no-op on non-Windows platforms
func CleanupKiroProtocolIfNeeded() error {
	return nil
}

// GetRegisteredExecutablePath is not applicable on non-Windows platforms
func GetRegisteredExecutablePath() (string, error) {
	return "", errURLSchemeHandlerUnsupported
}

// RestoreRegistration is not applicable on non-Windows platforms
func RestoreRegistration(_ string) error {
	return errURLSchemeHandlerUnsupported
}

// Stub functions for compatibility with app.go
func getMainBundleIdentifier() (string, error) {
	return "", errURLSchemeHandlerUnsupported
}

func getDefaultHandlerBundleIDForURLScheme(_ string) (string, error) {
	return "", errURLSchemeHandlerUnsupported
}

func setDefaultHandlerBundleIDForURLScheme(_, _ string) error {
	return errURLSchemeHandlerUnsupported
}

func getKiroProtocolHandlerStatusPlatform() (*KiroProtocolHandlerStatus, error) {
	return &KiroProtocolHandlerStatus{
		Supported:        false,
		Scheme:           "kiro",
		IsDefaultHandler: false,
		Note:             errURLSchemeHandlerUnsupported.Error(),
	}, nil
}
