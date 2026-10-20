package cli

import (
	_ "embed"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

//go:embed SKILL.md
var skillMarkdown string

// Version is set at build time with -ldflags "-X .../cli.Version=...".
var Version = "dev"

func (a *app) skillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skill",
		Short: "Print SKILL.md, which teaches an agent to drive celare",
		Long: `Print SKILL.md. Install it for Claude Code with:
  mkdir -p ~/.claude/skills/celare && celare skill > ~/.claude/skills/celare/SKILL.md`,
		Args: exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.ok(map[string]string{"skill": skillMarkdown}, func(w io.Writer) { fmt.Fprint(w, skillMarkdown) })
		},
	}
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print celare's version",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.ok(map[string]string{"version": Version, "xray": "26.3.27"}, func(w io.Writer) {
				fmt.Fprintf(w, "celare %s (xray-core 26.3.27)\n", Version)
			})
		},
	}
}
