//go:build !noclash

package main

import (
	_ "clisimplehub/internal/clash/plugin" // activate Clash plugin
	_ "clisimplehub/internal/codex/plugin" // activate Codex accounts plugin
	_ "clisimplehub/internal/kiro/plugin"  // activate Kiro plugin
)
