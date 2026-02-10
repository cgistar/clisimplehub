// +build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// RegisterURLScheme registers the kiro:// URL scheme in Windows registry
// This allows the application to be launched when kiro:// URLs are opened in browsers
func RegisterURLScheme() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks to get the actual executable path
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	scheme := "kiro"
	description := "URL:Kiro Protocol"

	// Check if already registered with correct path
	if isCorrectlyRegistered(scheme, exePath) {
		log.Printf("URL scheme '%s://' is already correctly registered", scheme)
		return nil
	}

	log.Printf("Registering URL scheme '%s://' for executable: %s", scheme, exePath)

	// Create/Update registry keys under HKEY_CURRENT_USER\Software\Classes
	// This doesn't require admin privileges
	baseKey := fmt.Sprintf(`Software\Classes\%s`, scheme)

	// Create base key with description
	key, _, err := registry.CreateKey(registry.CURRENT_USER, baseKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to create registry key %s: %w", baseKey, err)
	}
	defer key.Close()

	if err := key.SetStringValue("", description); err != nil {
		return fmt.Errorf("failed to set description: %w", err)
	}

	if err := key.SetStringValue("URL Protocol", ""); err != nil {
		return fmt.Errorf("failed to set URL Protocol: %w", err)
	}

	// Create DefaultIcon key
	iconKey, _, err := registry.CreateKey(registry.CURRENT_USER, baseKey+`\DefaultIcon`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to create DefaultIcon key: %w", err)
	}
	defer iconKey.Close()

	iconPath := fmt.Sprintf(`"%s",0`, exePath)
	if err := iconKey.SetStringValue("", iconPath); err != nil {
		return fmt.Errorf("failed to set icon path: %w", err)
	}

	// Create shell\open\command key
	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, baseKey+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to create command key: %w", err)
	}
	defer cmdKey.Close()

	commandLine := fmt.Sprintf(`"%s" "%%1"`, exePath)
	if err := cmdKey.SetStringValue("", commandLine); err != nil {
		return fmt.Errorf("failed to set command: %w", err)
	}

	log.Printf("Successfully registered URL scheme '%s://'", scheme)
	return nil
}

// isCorrectlyRegistered checks if the URL scheme is already registered with the correct executable path
func isCorrectlyRegistered(scheme, exePath string) bool {
	baseKey := fmt.Sprintf(`Software\Classes\%s`, scheme)
	cmdKeyPath := baseKey + `\shell\open\command`

	key, err := registry.OpenKey(registry.CURRENT_USER, cmdKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()

	registeredCmd, _, err := key.GetStringValue("")
	if err != nil {
		return false
	}

	expectedCmd := fmt.Sprintf(`"%s" "%%1"`, exePath)
	return registeredCmd == expectedCmd
}

// GetRegisteredExecutablePath returns the executable path currently registered for kiro:// scheme
// Returns empty string if not registered or if registered by current application (based on executable name)
func GetRegisteredExecutablePath() (string, error) {
	scheme := "kiro"
	baseKey := fmt.Sprintf(`Software\Classes\%s`, scheme)
	cmdKeyPath := baseKey + `\shell\open\command`

	key, err := registry.OpenKey(registry.CURRENT_USER, cmdKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return "", nil // Not registered
		}
		return "", fmt.Errorf("failed to open registry key: %w", err)
	}
	defer key.Close()

	registeredCmd, _, err := key.GetStringValue("")
	if err != nil {
		return "", fmt.Errorf("failed to read command value: %w", err)
	}

	// Extract executable path from command line (format: "path\to\exe.exe" "%1")
	registeredPath := extractPathFromCommand(registeredCmd)

	// If registered by current application (based on name), return empty (it's a leftover)
	if isCurrentApplication(registeredPath) {
		return "", nil
	}

	return registeredPath, nil
}

// isCurrentApplication checks if the given path belongs to the current application
// Comparison is based on executable name (without path and extension) to handle:
// - Path changes (app moved or renamed directory)
// - Leftover registrations from previous sessions
func isCurrentApplication(registeredPath string) bool {
	if registeredPath == "" {
		return false
	}

	currentExePath, err := os.Executable()
	if err != nil {
		return false
	}
	currentExePath, _ = filepath.EvalSymlinks(currentExePath)

	currentName := getExecutableName(currentExePath)
	registeredName := getExecutableName(registeredPath)

	return strings.EqualFold(currentName, registeredName)
}

// getExecutableName extracts the executable name without path and extension
// Example: "C:\path\to\app.exe" -> "app"
func getExecutableName(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// extractPathFromCommand extracts the executable path from a registry command value
// Handles formats like: "C:\path\to\app.exe" "%1"
func extractPathFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if len(cmd) == 0 {
		return ""
	}

	// If starts with quote, find the closing quote
	if cmd[0] == '"' {
		endQuote := strings.Index(cmd[1:], `"`)
		if endQuote >= 0 {
			return cmd[1 : endQuote+1]
		}
	}

	// Otherwise, take everything before the first space
	spaceIdx := strings.Index(cmd, " ")
	if spaceIdx >= 0 {
		return cmd[:spaceIdx]
	}

	return cmd
}

// RestoreRegistration restores the previous kiro:// URL scheme registration
// If previousPath is empty, unregisters the scheme
func RestoreRegistration(previousPath string) error {
	previousPath = strings.TrimSpace(previousPath)

	if previousPath == "" {
		// No previous registration, just unregister
		return UnregisterURLScheme()
	}

	// Restore previous registration
	scheme := "kiro"
	description := "URL:Kiro Protocol"
	baseKey := fmt.Sprintf(`Software\Classes\%s`, scheme)

	log.Printf("Restoring URL scheme '%s://' to previous executable: %s", scheme, previousPath)

	// Create base key with description
	key, _, err := registry.CreateKey(registry.CURRENT_USER, baseKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to create registry key %s: %w", baseKey, err)
	}
	defer key.Close()

	if err := key.SetStringValue("", description); err != nil {
		return fmt.Errorf("failed to set description: %w", err)
	}

	if err := key.SetStringValue("URL Protocol", ""); err != nil {
		return fmt.Errorf("failed to set URL Protocol: %w", err)
	}

	// Create DefaultIcon key
	iconKey, _, err := registry.CreateKey(registry.CURRENT_USER, baseKey+`\DefaultIcon`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to create DefaultIcon key: %w", err)
	}
	defer iconKey.Close()

	iconPath := fmt.Sprintf(`"%s",0`, previousPath)
	if err := iconKey.SetStringValue("", iconPath); err != nil {
		return fmt.Errorf("failed to set icon path: %w", err)
	}

	// Create shell\open\command key
	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, baseKey+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to create command key: %w", err)
	}
	defer cmdKey.Close()

	commandLine := fmt.Sprintf(`"%s" "%%1"`, previousPath)
	if err := cmdKey.SetStringValue("", commandLine); err != nil {
		return fmt.Errorf("failed to set command: %w", err)
	}

	log.Printf("Successfully restored URL scheme '%s://' to previous executable", scheme)
	return nil
}

// UnregisterURLScheme removes the kiro:// URL scheme registration
// This is useful for cleanup during uninstallation
func UnregisterURLScheme() error {
	scheme := "kiro"
	baseKey := fmt.Sprintf(`Software\Classes\%s`, scheme)

	err := registry.DeleteKey(registry.CURRENT_USER, baseKey+`\shell\open\command`)
	if err != nil && err != registry.ErrNotExist {
		log.Printf("Warning: failed to delete command key: %v", err)
	}

	err = registry.DeleteKey(registry.CURRENT_USER, baseKey+`\shell\open`)
	if err != nil && err != registry.ErrNotExist {
		log.Printf("Warning: failed to delete open key: %v", err)
	}

	err = registry.DeleteKey(registry.CURRENT_USER, baseKey+`\shell`)
	if err != nil && err != registry.ErrNotExist {
		log.Printf("Warning: failed to delete shell key: %v", err)
	}

	err = registry.DeleteKey(registry.CURRENT_USER, baseKey+`\DefaultIcon`)
	if err != nil && err != registry.ErrNotExist {
		log.Printf("Warning: failed to delete DefaultIcon key: %v", err)
	}

	err = registry.DeleteKey(registry.CURRENT_USER, baseKey)
	if err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("failed to delete base key: %w", err)
	}

	log.Printf("Successfully unregistered URL scheme '%s://'", scheme)
	return nil
}

// CleanupKiroProtocolIfNeeded checks for leftover kiro:// protocol registration and cleans it up
// This is called at application startup to handle:
// - Cancelled login sessions (protocol not restored)
// - Application path changes (moved or renamed)
func CleanupKiroProtocolIfNeeded() error {
	registeredPath, err := GetRegisteredExecutablePath()
	if err != nil {
		return err
	}

	// If no registration or not registered by current app, nothing to clean up
	if registeredPath == "" {
		// Empty means either not registered or registered by current app (leftover)
		// Check if actually registered
		scheme := "kiro"
		baseKey := fmt.Sprintf(`Software\Classes\%s`, scheme)
		key, err := registry.OpenKey(registry.CURRENT_USER, baseKey, registry.QUERY_VALUE)
		if err != nil {
			if err == registry.ErrNotExist {
				return nil // Not registered, nothing to clean
			}
			return nil // Can't check, skip cleanup
		}
		key.Close()

		// Registered but GetRegisteredExecutablePath returned empty -> it's a leftover
		log.Printf("Detected leftover kiro:// protocol registration, cleaning up...")
		return UnregisterURLScheme()
	}

	// registeredPath is not empty -> registered by another app, don't clean up
	return nil
}

// Stub functions for compatibility with app.go (not used on Windows)
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
	status := &KiroProtocolHandlerStatus{
		Supported:        true,
		Scheme:           "kiro",
		IsDefaultHandler: false,
	}

	exePath, err := os.Executable()
	if err != nil {
		status.Note = fmt.Sprintf("failed to get executable path: %v", err)
		return status, nil
	}

	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		status.Note = fmt.Sprintf("failed to resolve executable path: %v", err)
		return status, nil
	}

	status.IsDefaultHandler = isCorrectlyRegistered("kiro", exePath)

	registeredPath, err := GetRegisteredExecutablePath()
	if err != nil {
		status.Note = err.Error()
		return status, nil
	}

	status.DefaultHandlerBundleID = registeredPath
	return status, nil
}
