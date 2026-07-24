//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// CompareSnapshots returns field-level differences between two normalized
// snapshots.
func CompareSnapshots(baseline Snapshot, candidate Snapshot) []Diff {
	cmp := snapshotComparator{
		baseline:  baseline,
		candidate: candidate,
	}
	cmp.compare("/events", baseline.Events, candidate.Events)
	cmp.compare("/state", baseline.State, candidate.State)
	cmp.compare("/memories", baseline.Memories, candidate.Memories)
	cmp.compare("/summaries", baseline.Summaries, candidate.Summaries)
	cmp.compare("/tracks", baseline.Tracks, candidate.Tracks)
	cmp.compareUnsupported()
	return cmp.diffs
}

type snapshotComparator struct {
	baseline  Snapshot
	candidate Snapshot
	diffs     []Diff
}

func (c *snapshotComparator) compare(path string, a any, b any) {
	if reflect.DeepEqual(a, b) {
		return
	}
	av := reflect.ValueOf(a)
	bv := reflect.ValueOf(b)
	if !av.IsValid() || !bv.IsValid() || av.Type() != bv.Type() {
		c.addDiff(path, a, b, false, "")
		return
	}
	switch av.Kind() {
	case reflect.Map:
		c.compareMap(path, av, bv)
	case reflect.Slice, reflect.Array:
		c.compareSlice(path, av, bv)
	case reflect.Struct:
		c.compareStruct(path, av, bv)
	case reflect.Pointer:
		if av.IsNil() || bv.IsNil() {
			c.addDiff(path, a, b, false, "")
			return
		}
		c.compare(path, av.Elem().Interface(), bv.Elem().Interface())
	default:
		allowed, explanation := allowedScalarDiff(path, a, b)
		c.addDiff(path, a, b, allowed, explanation)
	}
}

func (c *snapshotComparator) compareMap(path string, av reflect.Value, bv reflect.Value) {
	seen := map[string]struct{}{}
	keys := make([]string, 0, av.Len()+bv.Len())
	for _, key := range av.MapKeys() {
		k := fmt.Sprint(key.Interface())
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for _, key := range bv.MapKeys() {
		k := fmt.Sprint(key.Interface())
		if _, ok := seen[k]; ok {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ak := reflect.ValueOf(key)
		if av.Type().Key().Kind() != reflect.String {
			ak = reflect.ValueOf(key).Convert(av.Type().Key())
		}
		aVal := av.MapIndex(ak)
		bVal := bv.MapIndex(ak)
		child := path + "/" + escapePath(key)
		if !aVal.IsValid() || !bVal.IsValid() {
			var aAny any
			var bAny any
			if aVal.IsValid() {
				aAny = aVal.Interface()
			}
			if bVal.IsValid() {
				bAny = bVal.Interface()
			}
			c.addDiff(child, aAny, bAny, false, "")
			continue
		}
		c.compare(child, aVal.Interface(), bVal.Interface())
	}
}

func (c *snapshotComparator) compareSlice(path string, av reflect.Value, bv reflect.Value) {
	maxLen := av.Len()
	if bv.Len() > maxLen {
		maxLen = bv.Len()
	}
	for i := 0; i < maxLen; i++ {
		child := fmt.Sprintf("%s/%d", path, i)
		if i >= av.Len() || i >= bv.Len() {
			var aAny any
			var bAny any
			if i < av.Len() {
				aAny = av.Index(i).Interface()
			}
			if i < bv.Len() {
				bAny = bv.Index(i).Interface()
			}
			c.addDiff(child, aAny, bAny, false, "")
			continue
		}
		c.compare(child, av.Index(i).Interface(), bv.Index(i).Interface())
	}
}

func (c *snapshotComparator) compareStruct(path string, av reflect.Value, bv reflect.Value) {
	t := av.Type()
	for i := 0; i < av.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := jsonFieldName(field.Name, field.Tag.Get("json"))
		if name == "-" {
			continue
		}
		c.compare(path+"/"+name, av.Field(i).Interface(), bv.Field(i).Interface())
	}
}

func (c *snapshotComparator) compareUnsupported() {
	for _, item := range c.baseline.Unsupported {
		c.addUnsupported(item, c.baseline.Backend)
	}
	for _, item := range c.candidate.Unsupported {
		c.addUnsupported(item, c.candidate.Backend)
	}
}

func (c *snapshotComparator) addUnsupported(item UnsupportedCapability, backendName string) {
	c.diffs = append(c.diffs, Diff{
		CaseName:         c.baseline.CaseName,
		BaselineBackend:  c.baseline.Backend,
		CandidateBackend: c.candidate.Backend,
		SessionID:        c.baseline.SessionID,
		Path:             "/capabilities/" + item.Capability,
		Candidate:        backendName,
		AllowedDiff:      item.AllowedDiff,
		Explanation:      item.Explanation,
		Unsupported:      true,
	})
}

func (c *snapshotComparator) addDiff(path string, a any, b any, allowed bool, explanation string) {
	diff := Diff{
		CaseName:         c.baseline.CaseName,
		BaselineBackend:  c.baseline.Backend,
		CandidateBackend: c.candidate.Backend,
		SessionID:        c.baseline.SessionID,
		Path:             path,
		Baseline:         a,
		Candidate:        b,
		AllowedDiff:      allowed,
		Explanation:      explanation,
	}
	c.attachLocator(&diff)
	c.diffs = append(c.diffs, diff)
}

func (c *snapshotComparator) attachLocator(diff *Diff) {
	parts := strings.Split(strings.Trim(diff.Path, "/"), "/")
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case "events":
		if idx, err := strconv.Atoi(parts[1]); err == nil {
			diff.EventIndex = &idx
		}
	case "summaries":
		diff.SummaryFilterKey = unescapePath(parts[1])
	case "memories":
		if m, ok := diff.Baseline.(NormalizedMemory); ok {
			diff.MemoryID = m.ID
		} else if m, ok := diff.Candidate.(NormalizedMemory); ok {
			diff.MemoryID = m.ID
		} else if idx, err := strconv.Atoi(parts[1]); err == nil {
			switch {
			case idx < len(c.baseline.Memories):
				diff.MemoryID = c.baseline.Memories[idx].ID
			case idx < len(c.candidate.Memories):
				diff.MemoryID = c.candidate.Memories[idx].ID
			}
		}
	case "tracks":
		diff.TrackName = unescapePath(parts[1])
	}
}

func allowedScalarDiff(path string, a any, b any) (bool, string) {
	if strings.Contains(path, "/score") || strings.Contains(path, "/duration_ms") ||
		strings.Contains(path, "/durationMs") || strings.Contains(path, "/elapsed_ms") {
		af, aok := asFloat(a)
		bf, bok := asFloat(b)
		if aok && bok && abs(af-bf) <= 0.001 {
			return true, "floating score/duration differs within replay tolerance"
		}
	}
	return false, ""
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case *float64:
		if x == nil {
			return 0, false
		}
		return *x, true
	default:
		return 0, false
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func escapePath(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func unescapePath(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

func jsonFieldName(fallback string, tag string) string {
	if tag == "" {
		return fallback
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return fallback
	}
	return name
}
