package smoke

import (
	"bytes"
	"testing"

	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
)

func TestXrayLoadsJSON(t *testing.T) {
	cfg := `{"inbounds":[{"listen":"127.0.0.1","port":10999,"protocol":"socks"}],"outbounds":[{"protocol":"freedom"}]}`
	c, err := core.LoadConfig("json", bytes.NewBufferString(cfg))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := core.New(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	srv.Close()
}
