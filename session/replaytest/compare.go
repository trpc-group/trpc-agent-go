//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

type differenceLocator struct {
	eventIndex       *int
	summaryID        string
	summaryFilterKey string
	trackName        string
	memoryID         string
}

type snapshotComparer struct {
	caseName string
	baseline *Snapshot
	compared *Snapshot
	rules    []AllowedDiffRule
	diffs    []Difference
}

// CompareSnapshots returns every semantic mismatch between two normalized
// snapshots. Known capability gaps and configured rules remain visible with
// AllowedDiff set to true.
func CompareSnapshots(
	caseName string,
	baseline *Snapshot,
	compared *Snapshot,
	rules []AllowedDiffRule,
) []Difference {
	if baseline == nil || compared == nil {
		return []Difference{{
			Case:            caseName,
			Source:          DifferenceSourceBackend,
			BaselineBackend: snapshotBackendName(baseline),
			Backend:         snapshotBackendName(compared),
			SessionID:       snapshotSessionID(baseline, compared),
			FieldPath:       "snapshot",
			BaselineValue:   baseline,
			ComparedValue:   compared,
		}}
	}

	c := &snapshotComparer{
		caseName: caseName,
		baseline: baseline,
		compared: compared,
		rules:    rules,
	}
	c.compareIdentity()
	c.compareUnsupported()

	if supportsComparison(baseline, compared, FeatureEvents) {
		c.compareEventSnapshots()
		c.compareNode(
			"observed_event_order",
			baseline.ObservedEventOrder,
			compared.ObservedEventOrder,
			differenceLocator{},
		)
	}
	if supportsComparison(baseline, compared, FeatureState) {
		c.compareNode(
			"state",
			baseline.State,
			compared.State,
			differenceLocator{},
		)
		c.compareNode(
			"state_transitions",
			baseline.StateTransitions,
			compared.StateTransitions,
			differenceLocator{},
		)
	}
	if supportsComparison(baseline, compared, FeatureMemory) {
		c.compareMemories()
		c.compareMemorySearches()
	}
	if supportsComparison(baseline, compared, FeatureSummary) {
		c.compareSummaries()
		c.compareNode(
			"summary_history",
			baseline.SummaryHistory,
			compared.SummaryHistory,
			differenceLocator{},
		)
		c.compareNode(
			"contexts",
			baseline.Contexts,
			compared.Contexts,
			differenceLocator{},
		)
	}
	if supportsComparison(baseline, compared, FeatureTrack) {
		c.compareTracks()
	}
	c.compareNode(
		"recoveries",
		baseline.Recoveries,
		compared.Recoveries,
		differenceLocator{},
	)
	return c.diffs
}

func snapshotBackendName(s *Snapshot) string {
	if s == nil {
		return ""
	}
	return s.Backend
}

func snapshotSessionID(a, b *Snapshot) string {
	if a != nil {
		return a.SessionID
	}
	if b != nil {
		return b.SessionID
	}
	return ""
}

func hasUnsupported(s *Snapshot, feature Feature) bool {
	for _, item := range s.Unsupported {
		if item.Feature == feature {
			return true
		}
	}
	return false
}

func supportsComparison(
	baseline *Snapshot,
	compared *Snapshot,
	feature Feature,
) bool {
	return !hasUnsupported(baseline, feature) &&
		!hasUnsupported(compared, feature)
}

func (c *snapshotComparer) compareIdentity() {
	c.compareNode(
		"app_name",
		c.baseline.AppName,
		c.compared.AppName,
		differenceLocator{},
	)
	c.compareNode(
		"user_id",
		c.baseline.UserID,
		c.compared.UserID,
		differenceLocator{},
	)
	c.compareNode(
		"session_id",
		c.baseline.SessionID,
		c.compared.SessionID,
		differenceLocator{},
	)
}

func (c *snapshotComparer) compareUnsupported() {
	seen := make(map[Feature]struct{}, len(c.compared.Unsupported))
	for _, unsupported := range c.compared.Unsupported {
		seen[unsupported.Feature] = struct{}{}
		c.addUnsupportedDifference(unsupported, false)
	}
	for _, unsupported := range c.baseline.Unsupported {
		if _, ok := seen[unsupported.Feature]; ok {
			continue
		}
		c.addUnsupportedDifference(unsupported, true)
	}
}

func (c *snapshotComparer) addUnsupportedDifference(
	unsupported UnsupportedFeature,
	baselineUnsupported bool,
) {
	baselineValue := capabilityStatus(c.baseline, unsupported.Feature)
	comparedValue := capabilityStatus(c.compared, unsupported.Feature)
	if baselineUnsupported {
		baselineValue = "unsupported"
		comparedValue = "supported"
	}
	c.diffs = append(c.diffs, Difference{
		Case:            c.caseName,
		Source:          DifferenceSourceBackend,
		BaselineBackend: c.baseline.Backend,
		Backend:         c.compared.Backend,
		SessionID:       c.baseline.SessionID,
		FieldPath: fmt.Sprintf(
			`capabilities["%s"]`,
			unsupported.Feature,
		),
		BaselineValue: baselineValue,
		ComparedValue: comparedValue,
		AllowedDiff:   unsupported.AllowedDiff,
		Explanation:   unsupported.Reason,
	})
}

func capabilityStatus(s *Snapshot, feature Feature) string {
	if hasUnsupported(s, feature) {
		return "unsupported"
	}
	return "supported"
}

func (c *snapshotComparer) compareEventSnapshots() {
	if len(c.baseline.Events) != len(c.compared.Events) {
		c.addDifference(
			"events.length",
			len(c.baseline.Events),
			len(c.compared.Events),
			differenceLocator{},
		)
	}
	count := minInt(len(c.baseline.Events), len(c.compared.Events))
	for i := 0; i < count; i++ {
		index := i
		c.compareNode(
			fmt.Sprintf("events[%d]", i),
			c.baseline.Events[i],
			c.compared.Events[i],
			differenceLocator{eventIndex: &index},
		)
	}
}

func (c *snapshotComparer) compareMemories() {
	if len(c.baseline.Memories) != len(c.compared.Memories) {
		c.addDifference(
			"memories.length",
			len(c.baseline.Memories),
			len(c.compared.Memories),
			differenceLocator{},
		)
	}
	baseByID := memoriesByID(c.baseline.Memories)
	comparedByID := memoriesByID(c.compared.Memories)
	for _, id := range unionKeys(baseByID, comparedByID) {
		c.compareNode(
			fmt.Sprintf(`memories[%q]`, id),
			baseByID[id],
			comparedByID[id],
			differenceLocator{memoryID: id},
		)
	}
}

func (c *snapshotComparer) compareMemorySearches() {
	if len(c.baseline.MemorySearches) !=
		len(c.compared.MemorySearches) {
		c.addDifference(
			"memory_searches.length",
			len(c.baseline.MemorySearches),
			len(c.compared.MemorySearches),
			differenceLocator{},
		)
	}
	searchCount := minInt(
		len(c.baseline.MemorySearches),
		len(c.compared.MemorySearches),
	)
	for i := 0; i < searchCount; i++ {
		baseline := c.baseline.MemorySearches[i]
		compared := c.compared.MemorySearches[i]
		prefix := fmt.Sprintf("memory_searches[%d]", i)
		c.compareNode(
			prefix+".query",
			baseline.Query,
			compared.Query,
			differenceLocator{},
		)
		if len(baseline.Results) != len(compared.Results) {
			c.addDifference(
				prefix+".results.length",
				len(baseline.Results),
				len(compared.Results),
				differenceLocator{},
			)
		}
		resultCount := minInt(
			len(baseline.Results),
			len(compared.Results),
		)
		for j := 0; j < resultCount; j++ {
			memoryID := baseline.Results[j].ID
			if memoryID == "" {
				memoryID = compared.Results[j].ID
			}
			c.compareNode(
				fmt.Sprintf("%s.results[%d]", prefix, j),
				baseline.Results[j],
				compared.Results[j],
				differenceLocator{memoryID: memoryID},
			)
		}
	}
}

func memoriesByID(memories []MemorySnapshot) map[string]any {
	out := make(map[string]any, len(memories))
	for _, item := range memories {
		out[item.ID] = item
	}
	return out
}

func (c *snapshotComparer) compareSummaries() {
	base := make(map[string]any, len(c.baseline.Summaries))
	for key, item := range c.baseline.Summaries {
		base[key] = item
	}
	target := make(map[string]any, len(c.compared.Summaries))
	for key, item := range c.compared.Summaries {
		target[key] = item
	}
	for _, key := range unionKeys(base, target) {
		baseSummary, baseOK := c.baseline.Summaries[key]
		targetSummary, targetOK := c.compared.Summaries[key]
		summaryID := baseSummary.ID
		if !baseOK && targetOK {
			summaryID = targetSummary.ID
		}
		c.compareNode(
			fmt.Sprintf(`summaries[%q]`, key),
			base[key],
			target[key],
			differenceLocator{
				summaryID:        summaryID,
				summaryFilterKey: key,
			},
		)
	}
}

func (c *snapshotComparer) compareTracks() {
	base := make(map[string]any, len(c.baseline.Tracks))
	for key, events := range c.baseline.Tracks {
		base[key] = events
	}
	target := make(map[string]any, len(c.compared.Tracks))
	for key, events := range c.compared.Tracks {
		target[key] = events
	}
	for _, key := range unionKeys(base, target) {
		c.compareNode(
			fmt.Sprintf(`tracks[%q]`, key),
			base[key],
			target[key],
			differenceLocator{trackName: key},
		)
	}
}

func (c *snapshotComparer) compareNode(
	path string,
	baseline any,
	compared any,
	locator differenceLocator,
) {
	baseNode := toJSONNode(baseline)
	comparedNode := toJSONNode(compared)
	c.compareJSONNode(path, baseNode, comparedNode, locator)
}

func (c *snapshotComparer) compareJSONNode(
	path string,
	baseline any,
	compared any,
	locator differenceLocator,
) {
	if baseline == nil || compared == nil {
		if baseline != nil || compared != nil {
			c.addDifference(path, baseline, compared, locator)
		}
		return
	}

	switch base := baseline.(type) {
	case map[string]any:
		target, ok := compared.(map[string]any)
		if !ok {
			c.addDifference(path, baseline, compared, locator)
			return
		}
		for _, key := range unionKeys(base, target) {
			childPath := path + "." + key
			c.compareJSONNode(
				childPath,
				base[key],
				target[key],
				locator,
			)
		}
	case []any:
		target, ok := compared.([]any)
		if !ok {
			c.addDifference(path, baseline, compared, locator)
			return
		}
		if len(base) != len(target) {
			c.addDifference(
				path+".length",
				len(base),
				len(target),
				locator,
			)
		}
		for i := 0; i < minInt(len(base), len(target)); i++ {
			c.compareJSONNode(
				fmt.Sprintf("%s[%d]", path, i),
				base[i],
				target[i],
				locator,
			)
		}
	default:
		if !scalarEqual(baseline, compared) {
			c.addDifference(path, baseline, compared, locator)
		}
	}
}

func (c *snapshotComparer) addDifference(
	path string,
	baseline any,
	compared any,
	locator differenceLocator,
) {
	allowed, explanation := c.matchRule(path, baseline, compared)
	c.diffs = append(c.diffs, Difference{
		Case:             c.caseName,
		Source:           DifferenceSourceBackend,
		BaselineBackend:  c.baseline.Backend,
		Backend:          c.compared.Backend,
		SessionID:        c.baseline.SessionID,
		EventIndex:       locator.eventIndex,
		SummaryID:        locator.summaryID,
		SummaryFilterKey: locator.summaryFilterKey,
		TrackName:        locator.trackName,
		MemoryID:         locator.memoryID,
		FieldPath:        path,
		BaselineValue:    baseline,
		ComparedValue:    compared,
		AllowedDiff:      allowed,
		Explanation:      explanation,
	})
}

func (c *snapshotComparer) matchRule(
	path string,
	baseline any,
	compared any,
) (bool, string) {
	for _, rule := range c.rules {
		if rule.Backend != "" && rule.Backend != c.compared.Backend {
			continue
		}
		if !pathHasPrefix(path, rule.PathPrefix) {
			continue
		}
		if rule.AbsoluteTolerance > 0 {
			baseNumber, baseOK := asFloat64(baseline)
			targetNumber, targetOK := asFloat64(compared)
			if !baseOK || !targetOK ||
				math.Abs(baseNumber-targetNumber) > rule.AbsoluteTolerance {
				continue
			}
		}
		return true, rule.Explanation
	}
	return false, ""
}

func pathHasPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix) || len(path) == len(prefix) {
		return false
	}
	next := path[len(prefix)]
	return next == '.' || next == '['
}

func toJSONNode(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var node any
	if err := decoder.Decode(&node); err != nil {
		return string(data)
	}
	return node
}

func scalarEqual(a, b any) bool {
	aNumber, aOK := asFloat64(a)
	bNumber, bOK := asFloat64(b)
	if aOK || bOK {
		return aOK && bOK && aNumber == bNumber
	}
	return reflect.DeepEqual(a, b)
}

func asFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		number, err := v.Float64()
		return number, err == nil
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	default:
		return 0, false
	}
}

func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for key := range a {
		seen[key] = struct{}{}
	}
	for key := range b {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
