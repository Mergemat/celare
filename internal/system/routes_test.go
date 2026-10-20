package system_test

import (
	"os/exec"
	"testing"

	"github.com/Mergemat/celare/internal/netconf"
	"github.com/Mergemat/celare/internal/system"
	"github.com/google/go-cmp/cmp"
)

// The kernel read must see exactly what netstat shows on this machine.
func TestDefaultRoutesMatchNetstat(t *testing.T) {
	out, err := exec.Command("netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		t.Skip("no netstat:", err)
	}
	got, err := system.DefaultRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(netconf.ParseDefaultRoutes(string(out)), got); diff != "" {
		t.Errorf("DefaultRoutes differs from netstat (-netstat +kernel):\n%s", diff)
	}
}
