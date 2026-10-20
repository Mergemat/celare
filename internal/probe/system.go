package probe

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"time"
)

// System fetches target the way any app on the Mac would, through the
// system resolver and routes, and on failure says which stage broke:
// DNS, connecting, TLS, or the response. Each call uses a fresh
// connection, so an earlier session's pooled connection can't answer.
func System(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	var resolved, connected bool
	var dnsErr, connErr, tlsErr error
	var addr string
	trace := &httptrace.ClientTrace{
		DNSDone: func(i httptrace.DNSDoneInfo) {
			mu.Lock()
			defer mu.Unlock()
			dnsErr = i.Err
			resolved = i.Err == nil
		},
		ConnectDone: func(_, a string, err error) {
			mu.Lock()
			defer mu.Unlock()
			addr = a
			if err != nil {
				connErr = err
			} else {
				connected = true
			}
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			mu.Lock()
			defer mu.Unlock()
			tlsErr = err
		},
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		return nil
	}
	elapsed := time.Since(start).Round(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	host := u.Hostname()
	switch {
	case net.ParseIP(host) == nil && !resolved:
		cause := dnsErr
		if cause == nil {
			cause = fmt.Errorf("no answer after %s", elapsed)
		}
		return fmt.Errorf("couldn't look up %s: %v", host, cause)
	case !connected:
		cause := connErr
		if cause == nil {
			cause = fmt.Errorf("no answer after %s", elapsed)
		}
		if addr == "" {
			addr = net.JoinHostPort(host, u.Port())
		}
		return fmt.Errorf("couldn't connect to %s: %v", addr, cause)
	case tlsErr != nil:
		return fmt.Errorf("connected to %s, but the TLS handshake failed: %v", addr, tlsErr)
	default:
		return fmt.Errorf("connected, but no response from %s after %s", host, elapsed)
	}
}
