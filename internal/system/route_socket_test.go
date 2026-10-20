package system

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Exercise the kernel's routing socket parser on an unused documentation
// network, and compare to the independently implemented route utility.
func TestRouteSocketIntegration(t *testing.T) {
	if os.Getenv("CELARE_SYSTEM_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("requires CELARE_SYSTEM_TEST=1 and root")
	}
	for _, prefix := range []string{"198.51.100.0/24", "2001:db8:ce1a::/48"} {
		t.Run(prefix, func(t *testing.T) {
			spec := RouteSpec{Prefix: netip.MustParsePrefix(prefix), Interface: "lo0", Scoped: true}
			start := time.Now()
			if err := ChangeRoute(true, spec); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ChangeRoute(false, spec) })
			t.Logf("add: %s", time.Since(start))
			family := "-inet"
			if spec.Prefix.Addr().Is6() {
				family = "-inet6"
			}
			out, err := exec.Command("route", "-n", "get", family, "-ifscope", "lo0", spec.Prefix.Addr().Next().String()).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "interface: lo0") {
				t.Fatalf("kernel route: %s (%v)", out, err)
			}
			start = time.Now()
			if err := ChangeRoute(false, spec); err != nil {
				t.Fatal(err)
			}
			t.Logf("delete: %s", time.Since(start))
			// Deleting an already absent route is how crash recovery behaves.
			if err := ChangeRoute(false, spec); err != nil {
				t.Fatal(err)
			}
		})
	}
}
