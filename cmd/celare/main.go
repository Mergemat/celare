// Command celare is an agent-friendly VPN client for macOS.
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/Mergemat/celare/internal/api"
	"github.com/Mergemat/celare/internal/cli"
)

func main() {
	configDir := os.Getenv("CELARE_CONFIG_DIR")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config", "celare")
	}
	socket := os.Getenv("CELARE_SOCKET")
	if socket == "" {
		socket = api.DefaultSocket
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Env{
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		ConfigDir: configDir,
		Socket:    socket,
		JSON:      os.Getenv("CELARE_OUTPUT") == "json",
	}))
}
