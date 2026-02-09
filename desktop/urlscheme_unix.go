// +build !windows

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
