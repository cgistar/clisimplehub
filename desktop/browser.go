package main

import (
	"fmt"
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
		// Try Chrome first, then Edge
		cmd = exec.Command("cmd", "/c", "start", "chrome", "--incognito", url)
		if err := cmd.Run(); err != nil {
			// Fallback to Edge InPrivate mode
			cmd = exec.Command("cmd", "/c", "start", "msedge", "--inprivate", url)
			if err := cmd.Run(); err != nil {
				// Final fallback to default browser
				cmd = exec.Command("cmd", "/c", "start", url)
				return cmd.Run()
			}
		}
		return nil

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
