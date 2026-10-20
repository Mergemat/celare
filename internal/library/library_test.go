package library_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/library"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var ignoreTime = cmpopts.IgnoreFields(library.SubscriptionInfo{}, "UpdatedAt")

// base64 of three links: "Tokyo", "Tokyo" again, and an unnamed one.
// Built with: printf '...' | base64
const subBody = "dHJvamFuOi8vcHdAMjAzLjAuMTEzLjE6NDQzP3NlY3VyaXR5PXRscyNUb2t5bwp0cm9qYW46Ly9wd0AyMDMuMC4xMTMuMjo0NDM/c2VjdXJpdHk9dGxzI1Rva3lvCmh5MjovL2F1dGhAMjAzLjAuMTEzLjM6NDQzP3NlY3VyaXR5PXRscwo="

func TestUpdateSubscriptionThenListAndResolve(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Subscription-Userinfo", "upload=100; download=900; total=10000; expire=1798761600")
		w.Write([]byte(subBody))
	}))
	defer panel.Close()

	lib := library.Library{Dir: t.TempDir()}
	cfg := config.Config{
		Nodes:         []config.NodeEntry{{Name: "home", Link: "vless://22222222-3333-4444-9555-666666666666@198.51.100.7:443?security=none#Home"}},
		Subscriptions: []config.Subscription{{Name: "main", URL: panel.URL}},
	}
	res, err := lib.UpdateSubscription(context.Background(), cfg.Subscriptions[0])
	if err != nil {
		t.Fatal(err)
	}
	want := library.SubscriptionInfo{
		Name: "main", Nodes: 3,
		Usage: &library.Usage{UploadBytes: 100, DownloadBytes: 900, TotalBytes: 10000, Expire: 1798761600},
	}
	if diff := cmp.Diff(want, res, ignoreTime); diff != "" {
		t.Errorf("UpdateSubscription mismatch (-want +got):\n%s", diff)
	}

	entries, err := lib.Nodes(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	// Duplicate names get a numeric suffix; unnamed nodes are named after
	// protocol and address so every node stays addressable.
	wantNames := []string{"home", "main/Tokyo", "main/Tokyo (2)", "main/hysteria2-203.0.113.3"}
	if diff := cmp.Diff(wantNames, names); diff != "" {
		t.Errorf("node names mismatch (-want +got):\n%s", diff)
	}

	n, err := lib.Resolve(cfg, "main/Tokyo (2)")
	if err != nil {
		t.Fatal(err)
	}
	if n.Address != "203.0.113.2" {
		t.Errorf("resolved %q to address %s, want 203.0.113.2", "main/Tokyo (2)", n.Address)
	}
}

func TestResolveUnknownNodeSuggestsListing(t *testing.T) {
	lib := library.Library{Dir: t.TempDir()}
	_, err := lib.Resolve(config.Config{}, "main/Nowhere")
	if err == nil || !strings.Contains(err.Error(), "celare nodes list") {
		t.Fatalf("err = %v, want a hint to run celare nodes list", err)
	}
}

// A panel answering with an error page must not wipe the cached nodes.
func TestFailedUpdateKeepsTheCache(t *testing.T) {
	ok := true
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !ok {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		w.Write([]byte(subBody))
	}))
	defer panel.Close()
	lib := library.Library{Dir: t.TempDir()}
	sub := config.Subscription{Name: "main", URL: panel.URL}
	if _, err := lib.UpdateSubscription(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	ok = false
	if _, err := lib.UpdateSubscription(context.Background(), sub); err == nil {
		t.Fatal("update against a 502 succeeded, want an error")
	}
	entries, err := lib.Nodes(config.Config{Subscriptions: []config.Subscription{sub}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("after a failed update %d nodes remain, want the 3 cached ones", len(entries))
	}
}
