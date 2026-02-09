// +build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

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
