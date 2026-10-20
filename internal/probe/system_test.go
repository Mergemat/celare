package probe_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mergemat/celare/internal/probe"
)

// The traffic check must say which stage failed: DNS, connecting, TLS,
// or waiting for a response.
func TestSystemNamesTheFailedStage(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer ok.Close()

	cases := []struct {
		url, want string
	}{
		{"http://celare-check.invalid/", "couldn't look up celare-check.invalid"},
		{"http://127.0.0.1:1/", "couldn't connect to 127.0.0.1:1"},
		{slow.URL, "connected, but no response"},
	}
	for _, tc := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		err := probe.System(ctx, tc.url)
		cancel()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("System(%s) = %v, want it to say %q", tc.url, err, tc.want)
		}
	}
	if err := probe.System(context.Background(), ok.URL); err != nil {
		t.Errorf("System(ok) = %v, want nil", err)
	}
}
