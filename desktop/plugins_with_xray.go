//go:build !noxray

package main

import (
	_ "clisimplehub/internal/codex/plugin" // activate Codex accounts plugin
	_ "clisimplehub/internal/kiro/plugin"  // activate Kiro plugin
	_ "clisimplehub/internal/xray/plugin"  // activate XRay plugin
)
