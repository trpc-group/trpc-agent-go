//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package compare

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
)

// Scope 表示回放比较范围。
type Scope string

const (
	// ScopeSession compares session replay snapshots.
	ScopeSession Scope = "session"
	// ScopeMemory compares memory replay snapshots.
	ScopeMemory Scope = "memory"
)

// Domain 表示差异所属的数据域。
type Domain string

const (
	// DomainSnapshot compares the full normalized snapshot.
	DomainSnapshot Domain = "snapshot"
	// DomainEvent compares normalized session events.
	DomainEvent Domain = "event"
	// DomainState compares session state maps.
	DomainState Domain = "state"
	// DomainSummary compares session summaries.
	DomainSummary Domain = "summary"
	// DomainTrack compares track event payloads.
	DomainTrack Domain = "track"
	// DomainMemory compares memory entries.
	DomainMemory Domain = "memory"
)

// Context 保存一次比较的元信息。
type Context struct {
	Case             string // case 名称
	BaselineBackend  string // 基准后端
	CandidateBackend string // 候选后端
	Scope            Scope  // 比较范围
}

// Locator 用于把差异定位到具体 session/event/summary/track/memory。
type Locator struct {
	SessionID        string `json:"session_id,omitempty"`
	EventIndex       *int   `json:"event_index,omitempty"`
	SummaryFilterKey string `json:"summary_filter_key,omitempty"`
	TrackName        string `json:"track_name,omitempty"`
	TrackEventIndex  *int   `json:"track_event_index,omitempty"`
	MemoryID         string `json:"memory_id,omitempty"`
	MemoryList       string `json:"memory_list,omitempty"` // read / search
	StateKey         string `json:"state_key,omitempty"`
}

// DiffEntry 描述一条字段级差异。
type DiffEntry struct {
	Case             string  `json:"case"`
	Scope            Scope   `json:"scope"`
	BaselineBackend  string  `json:"baseline_backend"`
	CandidateBackend string  `json:"candidate_backend"`
	Locator          Locator `json:"locator"`
	Domain           Domain  `json:"domain"`
	FieldPath        string  `json:"field_path"`
	BaselineValue    any     `json:"baseline_value"`
	CandidateValue   any     `json:"candidate_value"`
	AllowedDiff      bool    `json:"allowed_diff"` // 是否属于允许差异
	Reason           string  `json:"reason"`
}

// UnsupportedCapability 描述候选后端不支持的能力。
type UnsupportedCapability struct {
	Backend     string `json:"backend"`
	Feature     string `json:"feature"`
	AllowedDiff bool   `json:"allowed_diff"`
	Reason      string `json:"reason"`
}

// Report 汇总一次回放比较结果。
type Report struct {
	Case             string                  `json:"case"`
	BaselineBackend  string                  `json:"baseline_backend"`
	CandidateBackend string                  `json:"candidate_backend"`
	Scope            Scope                   `json:"scope"`
	Passed           bool                    `json:"passed"`
	Diffs            []DiffEntry             `json:"diffs"`
	AllowedDiffs     []DiffEntry             `json:"allowed_diffs,omitempty"`
	Unsupported      []UnsupportedCapability `json:"unsupported,omitempty"`
}

// ReportSet 用于序列化多条报告。
type ReportSet struct {
	Reports []Report `json:"reports"`
}

// AllowedRule 定义允许存在的差异规则。
type AllowedRule struct {
	Domain     Domain // 数据域
	PathSuffix string // 字段路径后缀
	Reason     string // 允许原因
}

// DefaultAllowedRules 返回默认允许差异，例如 Track 耗时指标。
func DefaultAllowedRules() []AllowedRule {
	return []AllowedRule{{
		Domain:     DomainTrack,
		PathSuffix: ".payload.duration_ms",
		Reason:     "耗时指标由后端运行环境产生，允许存在微小差异",
	}}
}

// CompareSession 逐字段比较两个 session 快照，并生成结构化报告。
func CompareSession(
	ctx Context,
	a *normalize.SnapShot,
	b *normalize.SnapShot,
	rules []AllowedRule,
) Report {
	report := newReport(ctx)
	if a == nil || b == nil {
		report.add(DomainSnapshot, "snapshot", Locator{}, a, b, rules)
		return report
	}
	locator := Locator{SessionID: a.SessionId}
	if a.SessionId != b.SessionId {
		report.add(
			DomainSnapshot,
			"snapshot.session_id",
			locator,
			a.SessionId,
			b.SessionId,
			rules,
		)
	}
	compareEvents(&report, locator, a.Events, b.Events, rules)
	compareStringMap(
		&report,
		DomainState,
		"state",
		locator,
		a.State,
		b.State,
		rules,
	)
	compareSummaries(&report, locator, a.Summaries, b.Summaries, rules)
	compareTracks(&report, locator, a.Tracks, b.Tracks, rules)
	return report
}

// CompareMemory 逐字段比较两个 memory 快照，并生成结构化报告。
func CompareMemory(
	ctx Context,
	a *normalize.MemorySnapshot,
	b *normalize.MemorySnapshot,
	rules []AllowedRule,
) Report {
	report := newReport(ctx)
	if a == nil || b == nil {
		report.add(DomainSnapshot, "memory", Locator{}, a, b, rules)
		return report
	}
	compareMemoryList(&report, "read", a.Read, b.Read, rules)
	compareMemoryList(&report, "search", a.Search, b.Search, rules)
	return report
}

// MarshalReportSet 把多条报告序列化成 JSON。
func MarshalReportSet(reports []Report) ([]byte, error) {
	return json.MarshalIndent(ReportSet{Reports: reports}, "", "  ")
}

// WriteReport writes a deterministic JSON artifact consumable by CI systems.
func WriteReport(path string, reports []Report) error {
	data, err := MarshalReportSet(reports)
	if err != nil {
		return fmt.Errorf("marshal replay report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write replay report: %w", err)
	}
	return nil
}

func newReport(ctx Context) Report {
	return Report{
		Case:             ctx.Case,
		BaselineBackend:  ctx.BaselineBackend,
		CandidateBackend: ctx.CandidateBackend,
		Scope:            ctx.Scope,
		Passed:           true,
		Diffs:            make([]DiffEntry, 0),
	}
}

func (r *Report) add(
	domain Domain,
	fieldPath string,
	locator Locator,
	baseline any,
	candidate any,
	rules []AllowedRule,
) {
	allowed, reason := allowedByRule(domain, fieldPath, rules)
	entry := DiffEntry{
		Case:             r.Case,
		Scope:            r.Scope,
		BaselineBackend:  r.BaselineBackend,
		CandidateBackend: r.CandidateBackend,
		Locator:          locator,
		Domain:           domain,
		FieldPath:        fieldPath,
		BaselineValue:    baseline,
		CandidateValue:   candidate,
		AllowedDiff:      allowed,
		Reason:           reason,
	}
	if allowed {
		// 允许差异单独归档，不影响 Passed。
		r.AllowedDiffs = append(r.AllowedDiffs, entry)
		return
	}
	r.Passed = false
	r.Diffs = append(r.Diffs, entry)
}

func allowedByRule(
	domain Domain,
	fieldPath string,
	rules []AllowedRule,
) (bool, string) {
	for _, rule := range rules {
		if rule.Domain == domain && strings.HasSuffix(fieldPath, rule.PathSuffix) {
			return true, rule.Reason
		}
	}
	return false, "字段值不一致"
}

// 按事件下标逐字段比较，并带上 event_index 定位信息。
func compareEvents(
	report *Report,
	locator Locator,
	a []normalize.Event,
	b []normalize.Event,
	rules []AllowedRule,
) {
	if len(a) != len(b) {
		report.add(DomainSnapshot, "events.length", locator, len(a), len(b), rules)
	}
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		itemLocator := locator
		itemLocator.EventIndex = intPointer(i)
		prefix := fmt.Sprintf("events[%d]", i)
		compareValue(report, DomainEvent, prefix+".id", itemLocator, a[i].ID, b[i].ID, rules)
		compareValue(report, DomainEvent, prefix+".author", itemLocator, a[i].Author, b[i].Author, rules)
		compareValue(report, DomainEvent, prefix+".role", itemLocator, a[i].Role, b[i].Role, rules)
		compareValue(report, DomainEvent, prefix+".content", itemLocator, a[i].Content, b[i].Content, rules)
		compareValue(report, DomainEvent, prefix+".tool_id", itemLocator, a[i].ToolID, b[i].ToolID, rules)
		compareValue(report, DomainEvent, prefix+".tool_name", itemLocator, a[i].ToolName, b[i].ToolName, rules)
		compareValue(report, DomainEvent, prefix+".filter_key", itemLocator, a[i].FilterKey, b[i].FilterKey, rules)
		compareValue(report, DomainEvent, prefix+".branch", itemLocator, a[i].Branch, b[i].Branch, rules)
		compareValue(report, DomainEvent, prefix+".tag", itemLocator, a[i].Tag, b[i].Tag, rules)
		compareStringMap(report, DomainEvent, prefix+".state_delta", itemLocator, a[i].StateDelta, b[i].StateDelta, rules)
		compareStringMap(report, DomainEvent, prefix+".extensions", itemLocator, a[i].Extensions, b[i].Extensions, rules)
		compareToolCalls(report, itemLocator, prefix, a[i].ToolCalls, b[i].ToolCalls, rules)
	}
}

func compareToolCalls(
	report *Report,
	locator Locator,
	prefix string,
	a []normalize.ToolCall,
	b []normalize.ToolCall,
	rules []AllowedRule,
) {
	if len(a) != len(b) {
		report.add(DomainEvent, prefix+".tool_calls.length", locator, len(a), len(b), rules)
	}
	for i := 0; i < min(len(a), len(b)); i++ {
		path := fmt.Sprintf("%s.tool_calls[%d]", prefix, i)
		compareValue(report, DomainEvent, path+".id", locator, a[i].ID, b[i].ID, rules)
		compareValue(report, DomainEvent, path+".name", locator, a[i].Name, b[i].Name, rules)
		compareValue(report, DomainEvent, path+".args", locator, a[i].Args, b[i].Args, rules)
	}
}

func compareSummaries(
	report *Report,
	locator Locator,
	a map[string]normalize.Summary,
	b map[string]normalize.Summary,
	rules []AllowedRule,
) {
	for _, key := range unionKeys(a, b) {
		left, leftOK := a[key]
		right, rightOK := b[key]
		itemLocator := locator
		itemLocator.SummaryFilterKey = key
		prefix := fmt.Sprintf("summaries[%q]", key)
		if !leftOK || !rightOK {
			report.add(DomainSummary, prefix, itemLocator, valueOrNil(left, leftOK), valueOrNil(right, rightOK), rules)
			continue
		}
		compareValue(report, DomainSummary, prefix+".text", itemLocator, left.Text, right.Text, rules)
		compareValue(report, DomainSummary, prefix+".version", itemLocator, left.Version, right.Version, rules)
		compareValue(report, DomainSummary, prefix+".filter_key", itemLocator, left.FilterKey, right.FilterKey, rules)
		compareValue(report, DomainSummary, prefix+".updated_at_set", itemLocator, left.UpdatedAtSet, right.UpdatedAtSet, rules)
		compareValue(report, DomainSummary, prefix+".cutoff_at_set", itemLocator, left.CutoffAtSet, right.CutoffAtSet, rules)
		compareValue(report, DomainSummary, prefix+".last_event_id", itemLocator, left.LastEventID, right.LastEventID, rules)
	}
}

func compareTracks(
	report *Report,
	locator Locator,
	a map[string][]normalize.TrackEvent,
	b map[string][]normalize.TrackEvent,
	rules []AllowedRule,
) {
	for _, name := range unionKeys(a, b) {
		left, leftOK := a[name]
		right, rightOK := b[name]
		itemLocator := locator
		itemLocator.TrackName = name
		prefix := fmt.Sprintf("tracks[%q]", name)
		if !leftOK || !rightOK {
			report.add(DomainTrack, prefix, itemLocator, valueOrNil(left, leftOK), valueOrNil(right, rightOK), rules)
			continue
		}
		if len(left) != len(right) {
			report.add(DomainTrack, prefix+".length", itemLocator, len(left), len(right), rules)
		}
		for i := 0; i < min(len(left), len(right)); i++ {
			eventLocator := itemLocator
			eventLocator.TrackEventIndex = intPointer(i)
			path := fmt.Sprintf("%s[%d].payload", prefix, i)
			compareJSON(report, path, eventLocator, left[i].Payload, right[i].Payload, rules)
		}
	}
}

// 把 Track payload 当 JSON 树递归比较，便于定位到具体字段。
func compareJSON(
	report *Report,
	fieldPath string,
	locator Locator,
	a string,
	b string,
	rules []AllowedRule,
) {
	var left any
	var right any
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil {
		compareValue(report, DomainTrack, fieldPath, locator, a, b, rules)
		return
	}
	compareJSONValue(report, fieldPath, locator, left, right, rules)
}

func compareJSONValue(
	report *Report,
	fieldPath string,
	locator Locator,
	a any,
	b any,
	rules []AllowedRule,
) {
	leftMap, leftMapOK := a.(map[string]any)
	rightMap, rightMapOK := b.(map[string]any)
	if leftMapOK && rightMapOK {
		for _, key := range unionKeys(leftMap, rightMap) {
			left, leftOK := leftMap[key]
			right, rightOK := rightMap[key]
			path := fieldPath + "." + key
			if !leftOK || !rightOK {
				report.add(DomainTrack, path, locator, valueOrNil(left, leftOK), valueOrNil(right, rightOK), rules)
				continue
			}
			compareJSONValue(report, path, locator, left, right, rules)
		}
		return
	}
	compareValue(report, DomainTrack, fieldPath, locator, a, b, rules)
}

// 按 memory ID 比较 read/search 列表：先比较同 ID 条目数量，再逐条比较字段。
func compareMemoryList(
	report *Report,
	listName string,
	a []normalize.MemoryEntry,
	b []normalize.MemoryEntry,
	rules []AllowedRule,
) {
	leftCounts := memoryCountByID(a)
	rightCounts := memoryCountByID(b)
	for _, id := range unionKeys(leftCounts, rightCounts) {
		lc, leftOK := leftCounts[id]
		rc, rightOK := rightCounts[id]
		locator := Locator{MemoryID: id, MemoryList: listName}
		prefix := fmt.Sprintf("memory.%s[id=%q]", listName, id)
		if !leftOK || !rightOK {
			report.add(
				DomainMemory,
				prefix+".count",
				locator,
				valueOrNil(lc, leftOK),
				valueOrNil(rc, rightOK),
				rules,
			)
			continue
		}
		if lc != rc {
			report.add(DomainMemory, prefix+".count", locator, lc, rc, rules)
			continue
		}
		leftGroup := memoryEntriesByID(a, id)
		rightGroup := memoryEntriesByID(b, id)
		for i := 0; i < len(leftGroup); i++ {
			itemPrefix := prefix
			if len(leftGroup) > 1 {
				itemPrefix = fmt.Sprintf("%s[%d]", prefix, i)
			}
			compareMemoryEntryFields(report, itemPrefix, locator, leftGroup[i], rightGroup[i], rules)
		}
	}
}

func compareMemoryEntryFields(
	report *Report,
	prefix string,
	locator Locator,
	l normalize.MemoryEntry,
	r normalize.MemoryEntry,
	rules []AllowedRule,
) {
	compareValue(report, DomainMemory, prefix+".content", locator, l.Content, r.Content, rules)
	compareValue(report, DomainMemory, prefix+".app_name", locator, l.AppName, r.AppName, rules)
	compareValue(report, DomainMemory, prefix+".user_id", locator, l.UserID, r.UserID, rules)
	compareValue(report, DomainMemory, prefix+".topics", locator, l.Topics, r.Topics, rules)
	compareValue(report, DomainMemory, prefix+".kind", locator, l.Kind, r.Kind, rules)
	compareValue(report, DomainMemory, prefix+".event_time", locator, l.EventTime, r.EventTime, rules)
	compareValue(report, DomainMemory, prefix+".participants", locator, l.Participants, r.Participants, rules)
	compareValue(report, DomainMemory, prefix+".location", locator, l.Location, r.Location, rules)
}

func memoryCountByID(entries []normalize.MemoryEntry) map[string]int {
	out := make(map[string]int, len(entries))
	for _, entry := range entries {
		out[entry.ID]++
	}
	return out
}

func memoryEntriesByID(entries []normalize.MemoryEntry, id string) []normalize.MemoryEntry {
	out := make([]normalize.MemoryEntry, 0)
	for _, entry := range entries {
		if entry.ID == id {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return memoryEntrySortKey(out[i]) < memoryEntrySortKey(out[j])
	})
	return out
}

func memoryEntrySortKey(entry normalize.MemoryEntry) string {
	return strings.Join([]string{
		entry.Content,
		entry.AppName,
		entry.UserID,
		entry.Kind,
		entry.EventTime,
		entry.Location,
		strings.Join(entry.Topics, "\x1e"),
		strings.Join(entry.Participants, "\x1e"),
	}, "\x1f")
}

func compareStringMap(
	report *Report,
	domain Domain,
	prefix string,
	locator Locator,
	a map[string]string,
	b map[string]string,
	rules []AllowedRule,
) {
	for _, key := range unionKeys(a, b) {
		left, leftOK := a[key]
		right, rightOK := b[key]
		itemLocator := locator
		if domain == DomainState {
			itemLocator.StateKey = key
		}
		path := prefix + "." + key
		if !leftOK || !rightOK {
			report.add(domain, path, itemLocator, valueOrNil(left, leftOK), valueOrNil(right, rightOK), rules)
			continue
		}
		compareValue(report, domain, path, itemLocator, left, right, rules)
	}
}

func compareValue(
	report *Report,
	domain Domain,
	fieldPath string,
	locator Locator,
	a any,
	b any,
	rules []AllowedRule,
) {
	if reflect.DeepEqual(a, b) {
		return
	}
	report.add(domain, fieldPath, locator, a, b, rules)
}

// 合并两个 map 的 key 并排序，保证比较顺序稳定。
func unionKeys[V any](a map[string]V, b map[string]V) []string {
	keys := make(map[string]struct{}, len(a)+len(b))
	for key := range a {
		keys[key] = struct{}{}
	}
	for key := range b {
		keys[key] = struct{}{}
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func valueOrNil[V any](value V, ok bool) any {
	if !ok {
		return nil
	}
	return value
}

func intPointer(value int) *int {
	return &value
}
