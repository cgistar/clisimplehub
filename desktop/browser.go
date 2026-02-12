package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// OpenURLInIncognito opens a URL in the default browser's incognito/private mode
func (a *App) OpenURLInIncognito(url string) error {
	if url == "" {
		return fmt.Errorf("URL is required")
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin": // macOS
		// Try Chrome first, then Safari
		cmd = exec.Command("open", "-na", "Google Chrome", "--args", "--incognito", url)
		if err := cmd.Run(); err != nil {
			// Fallback to Safari private mode
			cmd = exec.Command("open", "-a", "Safari", url)
			return cmd.Run()
		}
		return nil

	case "windows":
		// Try common Chrome installation paths
		chromePaths := []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LOCALAPPDATA") + `\Google\Chrome\Application\chrome.exe`,
		}

		for _, chromePath := range chromePaths {
			if _, err := os.Stat(chromePath); err == nil {
				// Chrome found, launch it directly to avoid cmd.exe parsing issues
				cmd = exec.Command(chromePath, "--incognito", url)
				return cmd.Run()
			}
		}

		// Chrome not found, return error
		return fmt.Errorf("Chrome not found in common installation paths")

	case "linux":
		// Try Chrome/Chromium first, then Firefox
		cmd = exec.Command("google-chrome", "--incognito", url)
		if err := cmd.Run(); err != nil {
			cmd = exec.Command("chromium-browser", "--incognito", url)
			if err := cmd.Run(); err != nil {
				// Fallback to Firefox private mode
				cmd = exec.Command("firefox", "--private-window", url)
				if err := cmd.Run(); err != nil {
					// Final fallback to xdg-open
					cmd = exec.Command("xdg-open", url)
					return cmd.Run()
				}
			}
		}
		return nil

	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}
