package daemon

import (
	"encoding/json"
	"slices"

	"github.com/Mergemat/celare/internal/config"
)

// Diff is what a change does to the running tunnel. Only the parts that
// differ are set.
type Diff struct {
	Node    *StringDiff `json:"node,omitempty"`
	Default *StringDiff `json:"default,omitempty"`
	DNS     *ListDiff   `json:"dns,omitempty"`
	Rules   *RulesDiff  `json:"rules,omitempty"`
}

// StringDiff: "" means none (for Node: the tunnel is down).
type StringDiff struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ListDiff struct {
	From []string `json:"from"`
	To   []string `json:"to"`
}

type RulesDiff struct {
	Added   []config.Rule `json:"added,omitempty"`
	Removed []config.Rule `json:"removed,omitempty"`
	// Reordered is set when the same rules now match in another order,
	// which changes which one wins.
	Reordered bool `json:"reordered,omitempty"`
}

func diffSpecs(from, to *Spec) Diff {
	var d Diff
	if describe(from) != describe(to) {
		d.Node = &StringDiff{From: nodeName(from), To: nodeName(to)}
	}
	if from == nil || to == nil {
		return d
	}
	if from.Default != to.Default {
		d.Default = &StringDiff{From: string(from.Default), To: string(to.Default)}
	}
	if !slices.Equal(from.DNS, to.DNS) {
		d.DNS = &ListDiff{From: from.DNS, To: to.DNS}
	}
	d.Rules = diffRules(from.Rules, to.Rules)
	return d
}

func nodeName(s *Spec) string {
	if s == nil {
		return ""
	}
	return s.NodeName
}

func diffRules(from, to []config.Rule) *RulesDiff {
	key := func(r config.Rule) string { b, _ := json.Marshal(r); return string(b) }
	count := func(rs []config.Rule) map[string]int {
		m := map[string]int{}
		for _, r := range rs {
			m[key(r)]++
		}
		return m
	}
	fromCount, toCount := count(from), count(to)
	var rd RulesDiff
	for _, r := range to {
		if k := key(r); fromCount[k] > 0 {
			fromCount[k]--
		} else {
			rd.Added = append(rd.Added, r)
		}
	}
	for _, r := range from {
		if k := key(r); toCount[k] > 0 {
			toCount[k]--
		} else {
			rd.Removed = append(rd.Removed, r)
		}
	}
	if len(rd.Added) == 0 && len(rd.Removed) == 0 {
		if !slices.EqualFunc(from, to, func(a, b config.Rule) bool { return key(a) == key(b) }) {
			rd.Reordered = true
			return &rd
		}
		return nil
	}
	return &rd
}
