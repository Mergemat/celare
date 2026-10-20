// Package engine runs Xray-core in-process.
package engine

import (
	"bytes"
	"fmt"

	"github.com/xtls/xray-core/core"
	// Registers every Xray protocol, transport and config loader.
	_ "github.com/xtls/xray-core/main/distro/all"
)

// Engine is one running Xray instance.
type Engine struct {
	instance *core.Instance
}

// Start loads an Xray JSON config and starts serving it.
func Start(configJSON []byte) (*Engine, error) {
	cfg, err := core.LoadConfig("json", bytes.NewReader(configJSON))
	if err != nil {
		return nil, fmt.Errorf("xray rejected the config: %w", err)
	}
	instance, err := core.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("xray could not build the config: %w", err)
	}
	if err := instance.Start(); err != nil {
		return nil, fmt.Errorf("xray failed to start: %w", err)
	}
	return &Engine{instance: instance}, nil
}

// Close stops the instance and releases its listeners and interfaces.
func (e *Engine) Close() error {
	return e.instance.Close()
}
