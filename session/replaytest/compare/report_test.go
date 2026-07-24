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
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
)

// TestCompareSessionDetectsResponseLessStateDeltaDiff 验证：没有 Response 的事件
// （如 OpAppendStateDelta 创建的纯状态变化事件）在两个后端存在字段差异时必须被检出。
// 归一化必须先填充与 Response 无关的公共字段（id/author/branch/tag/state_delta/...），
// 否则候选后端丢失这些字段也仍会判定一致。
func TestCompareSessionDetectsResponseLessStateDeltaDiff(t *testing.T) {
	base := &normalize.SnapShot{
		SessionId: "session-1",
		Events: []normalize.Event{{
			Index: 0, ID: "delta-1", Author: "system",
			Branch: "main", Tag: "state",
			StateDelta: map[string]string{"k": "v"},
			Extensions: map[string]string{"x": `{"v":1}`},
		}},
	}
	candidate := cloneSessionSnapshot(t, base)
	// 候选丢失 author、篡改 state_delta 与 branch，且没有 Response 字段可依赖。
	candidate.Events[0].Author = ""
	candidate.Events[0].Branch = "other"
	candidate.Events[0].StateDelta = map[string]string{"k": "dirty"}

	report := CompareSession(testContext(ScopeSession), base, candidate, nil)
	if report.Passed {
		t.Fatalf("无 Response 的 state-delta 事件差异必须被检出: %+v", report)
	}
	for _, path := range []string{
		"events[0].author",
		"events[0].branch",
		"events[0].state_delta.k",
	} {
		if !containsPath(report.Diffs, path) {
			t.Fatalf("未检测到无 Response 事件字段 %q: %+v", path, report.Diffs)
		}
	}
}

// TestCompareSessionToolArgsCanonicalized 验证：两个后端保存了语义相同、仅
// JSON 字段顺序和空白不同的工具调用参数时，归一化后比较应判定一致。
// 这模拟真实流程：后端存储原始 JSON，NormalizeEvent/NormalizeToolCalls 把它
// 规范化成统一字符串，比较器再按规范化值比较。
func TestCompareSessionToolArgsCanonicalized(t *testing.T) {
	// 两个后端各自存储的“原始”事件，参数字段顺序与空白不同但语义一致。
	backendA := normalize.NormalizeEvent(0, buildToolCallEventRaw(`{"city":"北京","weather":"晴"}`))
	backendB := normalize.NormalizeEvent(0, buildToolCallEventRaw(`{ "weather" : "晴" , "city" : "北京" }`))
	if backendA.ToolCalls[0].Args != backendB.ToolCalls[0].Args {
		t.Fatalf("语义相同的工具参数归一化后应一致: A=%q B=%q",
			backendA.ToolCalls[0].Args, backendB.ToolCalls[0].Args)
	}

	base := &normalize.SnapShot{
		SessionId: "session-1",
		Events:    []normalize.Event{backendA},
	}
	candidate := &normalize.SnapShot{
		SessionId: "session-1",
		Events:    []normalize.Event{backendB},
	}
	report := CompareSession(testContext(ScopeSession), base, candidate, nil)
	if !report.Passed {
		t.Fatalf("仅字段顺序/空白不同的工具参数应判定一致: %+v", report.Diffs)
	}
}

// buildToolCallEventRaw 构造一个工具调用事件，参数使用原始 JSON 字符串。
func buildToolCallEventRaw(args string) event.Event {
	return event.Event{
		ID:     "event-1",
		Author: "assistant",
		Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID: "call-1", Type: "function",
				Function: model.FunctionDefinitionParam{Name: "weather", Arguments: []byte(args)},
			}},
		}}}},
	}
}

func TestCompareSessionLocatesInjectedDifferences(t *testing.T) {
	base := sampleSessionSnapshot()
	// 逐个注入差异，验证 field_path 和 allowed_diff 是否符合预期。
	tests := []struct {
		name      string
		mutate    func(*normalize.SnapShot)
		wantPath  string
		wantAllow bool
	}{
		{"session_id", func(s *normalize.SnapShot) { s.SessionId = "wrong" }, "snapshot.session_id", false},
		{"event_content", func(s *normalize.SnapShot) { s.Events[0].Content = "wrong" }, "events[0].content", false},
		{"tool_args", func(s *normalize.SnapShot) { s.Events[0].ToolCalls[0].Args = `{"city":"上海"}` }, "events[0].tool_calls[0].args", false},
		{"state", func(s *normalize.SnapShot) { s.State["weather"] = "rain" }, "state.weather", false},
		{"summary_text", func(s *normalize.SnapShot) {
			summary := s.Summaries["weather"]
			summary.Text = "wrong"
			s.Summaries["weather"] = summary
		}, `summaries["weather"].text`, false},
		{"summary_missing", func(s *normalize.SnapShot) { delete(s.Summaries, "weather") }, `summaries["weather"]`, false},
		{"summary_boundary", func(s *normalize.SnapShot) {
			summary := s.Summaries["weather"]
			summary.LastEventID = "wrong"
			s.Summaries["weather"] = summary
		}, `summaries["weather"].last_event_id`, false},
		// summary 覆盖错误：摘要被陈旧版本覆盖，version 不匹配。
		{"summary_version", func(s *normalize.SnapShot) {
			summary := s.Summaries["weather"]
			summary.Version = 999
			s.Summaries["weather"] = summary
		}, `summaries["weather"].version`, false},
		// Go 必需的 filter-key 错误：摘要归属的 filter-key 字段值被改写。
		{"summary_filter_key", func(s *normalize.SnapShot) {
			summary := s.Summaries["weather"]
			summary.FilterKey = "wrong"
			s.Summaries["weather"] = summary
		}, `summaries["weather"].filter_key`, false},
		// summary 归属 session 错误：候选多出一条本属别的 session 的摘要。
		{"summary_ownership", func(s *normalize.SnapShot) {
			s.Summaries["profile"] = normalize.Summary{
				FilterKey: "profile", Text: "跨 session 污染", Version: 1,
			}
		}, `summaries["profile"]`, false},
		{"track_status", func(s *normalize.SnapShot) { s.Tracks["tool"][0].Payload = `{"status":"error","duration_ms":25}` }, `tracks["tool"][0].payload.status`, false},
		{"track_duration", func(s *normalize.SnapShot) { s.Tracks["tool"][0].Payload = `{"status":"ok","duration_ms":26}` }, `tracks["tool"][0].payload.duration_ms`, true},
		{"extension", func(s *normalize.SnapShot) { s.Events[0].Extensions["x"] = `{"v":2}` }, "events[0].extensions.x", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := cloneSessionSnapshot(t, base)
			tc.mutate(candidate)
			report := CompareSession(testContext(ScopeSession), base, candidate, DefaultAllowedRules())
			entries := report.Diffs
			if tc.wantAllow {
				entries = report.AllowedDiffs
			}
			if !containsPath(entries, tc.wantPath) {
				t.Fatalf("未检测到字段 %q，报告: %+v", tc.wantPath, report)
			}
			if tc.wantAllow && !report.Passed {
				t.Fatalf("允许差异不应导致失败: %+v", report)
			}
			if !tc.wantAllow && report.Passed {
				t.Fatalf("阻断差异应导致失败: %+v", report)
			}
		})
	}
}

func TestCompareMemoryLocatesMemoryID(t *testing.T) {
	base := &normalize.MemorySnapshot{
		Read: []normalize.MemoryEntry{{
			ID: "memory-1", AppName: "app", UserID: "user",
			Content: "用户偏好中文", Kind: "fact",
		}},
		Search: []normalize.MemoryEntry{{
			ID: "memory-1", AppName: "app", UserID: "user",
			Content: "用户偏好中文", Kind: "fact",
		}},
	}
	candidate := *base
	candidate.Read = append([]normalize.MemoryEntry(nil), base.Read...)
	candidate.Read[0].Content = "用户偏好英文"

	report := CompareMemory(
		testContext(ScopeMemory),
		base,
		&candidate,
		nil,
	)
	if report.Passed || len(report.Diffs) != 1 {
		t.Fatalf("memory 差异未被准确检测: %+v", report)
	}
	if report.Diffs[0].Locator.MemoryID != "memory-1" {
		t.Fatalf("memory id 定位错误: %+v", report.Diffs[0])
	}
}

func TestCompareMemoryDetectsDuplicateCountMismatch(t *testing.T) {
	entry := normalize.MemoryEntry{
		ID: "memory-1", AppName: "app", UserID: "user",
		Content: "用户偏好中文", Kind: "fact",
	}
	base := &normalize.MemorySnapshot{
		Read: []normalize.MemoryEntry{entry, entry},
	}
	candidate := &normalize.MemorySnapshot{
		Read: []normalize.MemoryEntry{entry},
	}

	report := CompareMemory(testContext(ScopeMemory), base, candidate, nil)
	if report.Passed {
		t.Fatalf("重复 semantic id 数量不一致应失败: %+v", report)
	}
	if !containsPath(report.Diffs, `memory.read[id="memory-1"].count`) {
		t.Fatalf("应报告 count 差异: %+v", report.Diffs)
	}
}

func TestMarshalReportSet(t *testing.T) {
	// 验证报告 JSON 中包含定位信息和 allowed_diff 字段。
	candidate := cloneSessionSnapshot(t, sampleSessionSnapshot())
	candidate.Events[0].Content = "wrong"
	report := CompareSession(
		testContext(ScopeSession),
		sampleSessionSnapshot(),
		candidate,
		nil,
	)
	data, err := MarshalReportSet([]Report{report})
	if err != nil {
		t.Fatalf("序列化报告失败: %v", err)
	}
	text := string(data)
	for _, expected := range []string{
		`"case": "injection_case"`,
		`"event_index": 0`,
		`"field_path": "events[0].content"`,
		`"allowed_diff": false`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("报告缺少 %s:\n%s", expected, text)
		}
	}
}

func TestWriteReport(t *testing.T) {
	path := t.TempDir() + "/replay-report.json"
	if err := WriteReport(path, []Report{newReport(testContext(ScopeSession))}); err != nil {
		t.Fatalf("写入报告失败: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取报告失败: %v", err)
	}
	var artifact ReportSet
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("报告不是合法 JSON: %v", err)
	}
	if len(artifact.Reports) != 1 || !artifact.Reports[0].Passed {
		t.Fatalf("报告内容异常: %+v", artifact)
	}
}

// 构造一份完整的 session 快照样本，供差异注入测试复用。
func sampleSessionSnapshot() *normalize.SnapShot {
	return &normalize.SnapShot{
		SessionId: "session-1",
		Events: []normalize.Event{{
			Index: 0, ID: "event-1", Role: "assistant", Content: "北京晴",
			ToolCalls: []normalize.ToolCall{{
				ID: "call-1", Name: "weather", Args: `{"city":"北京"}`,
			}},
			Extensions: map[string]string{"x": `{"v":1}`},
		}},
		State: map[string]string{"weather": "sunny"},
		Summaries: map[string]normalize.Summary{
			"weather": {
				FilterKey: "weather", Text: "天气摘要", Version: 1,
				UpdatedAtSet: true, CutoffAtSet: true, LastEventID: "event-1",
			},
		},
		Tracks: map[string][]normalize.TrackEvent{
			"tool": {{
				Index: 0, TrackName: "tool",
				Payload: `{"duration_ms":25,"status":"ok"}`,
			}},
		},
	}
}

// 通过 JSON 深拷贝快照，避免测试间互相污染。
func cloneSessionSnapshot(
	t *testing.T,
	value *normalize.SnapShot,
) *normalize.SnapShot {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("序列化快照失败: %v", err)
	}
	var cloned normalize.SnapShot
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatalf("反序列化快照失败: %v", err)
	}
	return &cloned
}

// 构造测试用比较上下文。
func testContext(scope Scope) Context {
	return Context{
		Case:             "injection_case",
		BaselineBackend:  "inmemory",
		CandidateBackend: "sqlite",
		Scope:            scope,
	}
}

// 判断差异列表里是否包含目标字段路径。
func containsPath(entries []DiffEntry, path string) bool {
	for _, entry := range entries {
		if entry.FieldPath == path {
			return true
		}
	}
	return false
}
