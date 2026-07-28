//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Normalization rules.
//
// Every value the comparator sees is either compared verbatim or passed
// through exactly one of these rules. Naming them is not decoration: a rule
// name is recorded next to each normalized field so a reader can tell the
// difference between "these backends agree" and "the harness was told not to
// look".
const (
	// RuleTimestampOffset renders an absolute instant as its offset from the
	// run's base instant, truncated to milliseconds. Backends persist time at different
	// precisions, from SQLite nanoseconds to millisecond-granular drivers, so
	// raw instants never compare equal even when the data is identical.
	RuleTimestampOffset = "timestamp-offset-ms"

	// RuleCanonicalJSON re-encodes a JSON value with sorted object keys.
	// Backends round-trip payloads through different encoders, so member order
	// carries no meaning and comparing raw bytes would report noise.
	RuleCanonicalJSON = "canonical-json"

	// RuleSortedMap projects a Go map as a slice ordered by key. Map iteration
	// order is deliberately randomized by the runtime, so an unordered
	// projection would produce a different answer on every run.
	RuleSortedMap = "sorted-map"

	// RuleSortedList orders a set-like list whose element order carries no
	// meaning, such as memory topics.
	RuleSortedList = "sorted-list"
)

// timestampPrecision is the granularity every persisted instant is truncated
// to before comparison.
const timestampPrecision = time.Millisecond

// offsetFrom renders an instant as its offset from the run's base instant.
//
// A zero instant maps to the empty string so that "no timestamp" stays
// distinguishable from "the base instant"; conflating them would hide a
// backend that drops a timestamp entirely.
func offsetFrom(ts, base time.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := ts.Sub(base).Truncate(timestampPrecision)
	return d.String()
}

// canonicalJSON re-encodes raw JSON with object keys sorted at every level.
//
// Invalid JSON is returned verbatim rather than rejected: a backend that
// corrupts a payload must surface as a difference in the payload, not as a
// harness error that aborts the run.
func canonicalJSON(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sortJSON(v)); err != nil {
		return string(raw)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

// sortJSON rebuilds decoded JSON so that encoding it yields sorted keys.
// encoding/json already emits map keys in sorted order, so this only needs to
// recurse and hand back plain maps and slices.
func sortJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = sortJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sortJSON(val)
		}
		return out
	default:
		return v
	}
}

// StateEntry is one state key projected for comparison.
//
// Nil is tracked separately from an empty value because the two mean different
// things: a nil state delta stores a nil value under the key while leaving the
// key present, whereas a delete removes it. A projection that rendered both as
// "" would silently accept a backend that turns one into the other.
type StateEntry struct {
	Key   string `json:"key" diffkey:"true"`
	Value string `json:"value"`
	Nil   bool   `json:"nil,omitempty"`
}

// stateEntries projects a state map as a key-ordered slice.
func stateEntries(state map[string][]byte) []StateEntry {
	if len(state) == 0 {
		return nil
	}
	out := make([]StateEntry, 0, len(state))
	for k, v := range state {
		entry := StateEntry{Key: k}
		if v == nil {
			entry.Nil = true
		} else {
			entry.Value = string(v)
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ExtensionEntry is one event extension projected for comparison.
type ExtensionEntry struct {
	Key   string `json:"key" diffkey:"true"`
	Value string `json:"value"`
}

// extensionEntries projects event extensions as a key-ordered slice with each
// value canonicalized.
func extensionEntries(ext map[string]json.RawMessage) []ExtensionEntry {
	if len(ext) == 0 {
		return nil
	}
	out := make([]ExtensionEntry, 0, len(ext))
	for k, v := range ext {
		out = append(out, ExtensionEntry{Key: k, Value: canonicalJSON(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// sortedCopy returns a sorted copy of a list whose order carries no meaning.
func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// renderValue formats a projected value for a divergence record.
func renderValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "<absent>"
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
