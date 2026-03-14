package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// PickClashExecutable opens a native file dialog and returns the selected clash executable path.
func (a *App) PickClashExecutable() (string, error) {
	if a == nil || a.ctx == nil {
		return "", fmt.Errorf("app context not initialized")
	}

	currentPath := ""
	if settings, err := a.GetSettings(); err == nil && settings != nil {
		currentPath = strings.TrimSpace(settings.ClashPath)
	}

	options := wailsRuntime.OpenDialogOptions{
		Title: "Select Clash Executable",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "Executable Files", Pattern: "*.exe;*.bin;*.run;*.AppImage"},
			{DisplayName: "All Files", Pattern: "*"},
		},
	}

	if currentPath != "" {
		defaultDir := filepath.Dir(currentPath)
		if info, err := os.Stat(defaultDir); err == nil && info.IsDir() {
			options.DefaultDirectory = defaultDir
		}
		options.DefaultFilename = filepath.Base(currentPath)
	}

	selected, err := wailsRuntime.OpenFileDialog(a.ctx, options)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(selected), nil
}
