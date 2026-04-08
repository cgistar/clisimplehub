package entclawplugin

import "clisimplehub/internal/plugin"

func init() {
	plugin.Register(&EntclawPlugin{})
}

type EntclawPlugin struct{}

func (p *EntclawPlugin) Name() string { return "entclaw" }

func (p *EntclawPlugin) Init(cfg plugin.InitConfig) error { return nil }

func (p *EntclawPlugin) Reload() error { return nil }
