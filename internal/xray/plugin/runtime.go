package xrayplugin

import (
	"bytes"
	"fmt"
	"io"

	xray "github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// xrayInstance wraps a xray.Instance to implement io.Closer.
type xrayInstance struct {
	inst *xray.Instance
}

func (v *xrayInstance) Close() error {
	if v.inst != nil {
		return v.inst.Close()
	}
	return nil
}

// startXRayInstance starts a xray-core instance from runtime JSON.
func startXRayInstance(runtimeJSON []byte) (io.Closer, error) {
	cfg, err := xray.LoadConfig("json", bytes.NewReader(runtimeJSON))
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	inst, err := xray.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}

	if err := inst.Start(); err != nil {
		inst.Close()
		return nil, fmt.Errorf("start instance: %w", err)
	}

	return &xrayInstance{inst: inst}, nil
}
