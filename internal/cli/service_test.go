package cli

import (
	"os"
	"testing"
)

// /dev/null is a character device but not a terminal; taking it for one
// sends escalation to sudo, which then fails for want of a terminal.
func TestDevNullIsNotATerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stdin := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = stdin }()
	if stdinIsTerminal() {
		t.Error("stdinIsTerminal() = true for /dev/null")
	}
}
