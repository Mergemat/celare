// Package library finds nodes: the ones in config.toml and the ones
// cached from subscriptions.
package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mergemat/celare/internal/config"
	"github.com/Mergemat/celare/internal/node"
)

// Library is rooted at the config directory (~/.config/celare).
type Library struct {
	Dir string
}

// Entry is one addressable node.
type Entry struct {
	Name   string    `json:"name"`
	Source string    `json:"source"` // "config" or "subscription:<name>"
	Node   node.Node `json:"-"`
}

// SubscriptionInfo is the metadata kept next to a subscription's links.
type SubscriptionInfo struct {
	Name      string    `json:"name"`
	Nodes     int       `json:"nodes"`
	UpdatedAt time.Time `json:"updatedAt"`
	Usage     *Usage    `json:"usage,omitempty"`
	// Problems are links in the subscription that could not be parsed.
	Problems []string `json:"problems,omitempty"`
}

// Usage is the panel's Subscription-Userinfo header. Expire is a Unix
// time, 0 when the subscription never expires.
type Usage struct {
	UploadBytes   int64 `json:"uploadBytes"`
	DownloadBytes int64 `json:"downloadBytes"`
	TotalBytes    int64 `json:"totalBytes"`
	Expire        int64 `json:"expire"`
}

func (l Library) subDir() string { return filepath.Join(l.Dir, "subscriptions") }

// UpdateSubscription downloads a subscription and replaces its cache. On
// any failure the previous cache is left untouched.
func (l Library) UpdateSubscription(ctx context.Context, sub config.Subscription) (SubscriptionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.URL, nil)
	if err != nil {
		return SubscriptionInfo{}, fmt.Errorf("subscription %q has an invalid url: %w", sub.Name, err)
	}
	req.Header.Set("User-Agent", "celare")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return SubscriptionInfo{}, fmt.Errorf("fetch subscription %q: %w", sub.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return SubscriptionInfo{}, fmt.Errorf("fetch subscription %q: panel answered %s", sub.Name, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return SubscriptionInfo{}, fmt.Errorf("fetch subscription %q: %w", sub.Name, err)
	}
	nodes, problems := node.ParseSubscription(body)
	if len(nodes) == 0 {
		return SubscriptionInfo{}, fmt.Errorf("subscription %q returned no usable links (%d unparseable lines); the cache was kept", sub.Name, len(problems))
	}
	info := SubscriptionInfo{
		Name:      sub.Name,
		Nodes:     len(nodes),
		UpdatedAt: time.Now().UTC(),
		Usage:     parseUsage(resp.Header.Get("Subscription-Userinfo")),
	}
	for _, p := range problems {
		info.Problems = append(info.Problems, fmt.Sprintf("line %d: %v", p.Line, p.Err))
	}
	links := linksOf(body)
	if err := os.MkdirAll(l.subDir(), 0o700); err != nil {
		return SubscriptionInfo{}, err
	}
	meta, _ := json.MarshalIndent(info, "", "  ")
	if err := writeAtomic(filepath.Join(l.subDir(), sub.Name+".txt"), []byte(links)); err != nil {
		return SubscriptionInfo{}, err
	}
	if err := writeAtomic(filepath.Join(l.subDir(), sub.Name+".json"), meta); err != nil {
		return SubscriptionInfo{}, err
	}
	return info, nil
}

// SubscriptionInfo returns the cached metadata, or false if the
// subscription was never fetched.
func (l Library) SubscriptionInfo(name string) (SubscriptionInfo, bool) {
	b, err := os.ReadFile(filepath.Join(l.subDir(), name+".json"))
	if err != nil {
		return SubscriptionInfo{}, false
	}
	var info SubscriptionInfo
	if json.Unmarshal(b, &info) != nil {
		return SubscriptionInfo{}, false
	}
	return info, true
}

// Nodes lists every addressable node: config.toml's first, then each
// subscription's in panel order.
func (l Library) Nodes(cfg config.Config) ([]Entry, error) {
	var out []Entry
	for _, n := range cfg.Nodes {
		parsed, err := node.ParseLink(n.Link)
		if err != nil {
			return nil, fmt.Errorf("node %q in config.toml: %w", n.Name, err)
		}
		out = append(out, Entry{Name: n.Name, Source: "config", Node: parsed})
	}
	for _, sub := range cfg.Subscriptions {
		b, err := os.ReadFile(filepath.Join(l.subDir(), sub.Name+".txt"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		nodes, _ := node.ParseSubscription(b)
		taken := map[string]int{}
		for _, n := range nodes {
			name := n.Name
			if name == "" {
				name = n.Protocol + "-" + n.Address
			}
			taken[name]++
			if c := taken[name]; c > 1 {
				name += " (" + strconv.Itoa(c) + ")"
			}
			out = append(out, Entry{Name: sub.Name + "/" + name, Source: "subscription:" + sub.Name, Node: n})
		}
	}
	return out, nil
}

// Resolve finds a node by its listed name.
func (l Library) Resolve(cfg config.Config, name string) (node.Node, error) {
	entries, err := l.Nodes(cfg)
	if err != nil {
		return node.Node{}, err
	}
	for _, e := range entries {
		if e.Name == name {
			return e.Node, nil
		}
	}
	return node.Node{}, fmt.Errorf("no node named %q; run `celare nodes list` for the names", name)
}

// linksOf returns the body as plain newline-separated links, decoding
// base64 bodies so the cache file stays readable.
func linksOf(body []byte) string {
	nodes, _ := node.ParseSubscription(body)
	text := strings.TrimSpace(string(body))
	if !strings.Contains(text, "://") && len(nodes) > 0 {
		if decoded, err := node.DecodeBase64(text); err == nil {
			text = strings.TrimSpace(string(decoded))
		}
	}
	return text + "\n"
}

func parseUsage(header string) *Usage {
	if header == "" {
		return nil
	}
	u := &Usage{}
	for _, part := range strings.Split(header, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		switch k {
		case "upload":
			u.UploadBytes = n
		case "download":
			u.DownloadBytes = n
		case "total":
			u.TotalBytes = n
		case "expire":
			u.Expire = n
		}
	}
	return u
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
