//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backend"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/compare"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/summary"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// TestReplay 在 inmemory 与 sqlite 之间回放全部 session case。
func TestReplay(t *testing.T) {
	// 待测所有case
	cases := []*scenario.Case{
		scenario.Case01_SingleTurn,
		scenario.Case02_MultiTurn,
		scenario.Case03_UpdateState,
		scenario.Case04_ToolCall,
		scenario.Case06_Summary,
		scenario.Case06_SummaryFilterKey,
		scenario.Case07_SummaryWithTruncation,
		scenario.Case08_Track,
		scenario.Case09_ConcurrentAppend,
		scenario.Case10_Recovery,
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			RunCase(t, tc)
		})
	}
}

// case执行器, 执行单个case
func RunCase(t *testing.T, c *scenario.Case) {
	t.Helper()

	ctx := context.Background()

	var baseline session.Service = inmemory.NewSessionService(inmemory.WithSummarizer(summary.FakeSummarizer{}))
	var candidate session.Service = backend.NewSQLiteService(t, sqlite.WithSummarizer(summary.FakeSummarizer{}))
	// Case10 需要模拟首次追加失败。
	if c.Name == scenario.Case10_Recovery.Name {
		baseline = &failFirstAppendService{Service: baseline}
		candidate = &failFirstAppendService{Service: candidate}
	}
	// sessA: inmemory 返回的 session，sessB: sqlite 返回的 session
	sessA, err := harness.Run(ctx, baseline, c)
	if err != nil {
		t.Fatalf("基准后端回放失败: %v", err)
	}

	sessB, err := harness.Run(ctx, candidate, c)
	if err != nil {
		t.Fatalf("候选后端回放失败: %v", err)
	}
	// 归一化
	snapA := normalize.FromSession(sessA)
	snapB := normalize.FromSession(sessB)
	assertCaseExpectations(t, c.Name, snapA)
	assertCaseExpectations(t, c.Name, snapB)

	report := compare.CompareSession(compare.Context{
		Case:             c.Name,
		BaselineBackend:  "session/inmemory",
		CandidateBackend: "session/sqlite",
		Scope:            compare.ScopeSession,
	}, snapA, snapB, compare.DefaultAllowedRules())
	// 失败时输出结构化 JSON，便于定位到 event/summary/track 字段。
	if !report.Passed {
		data, _ := compare.MarshalReportSet([]compare.Report{report})
		t.Fatalf("快照不一致:\n%s", data)
	}
	// 注入差异，确认比较器能检出不同。
	assertInjectedMismatchDetected(t, c.Name, snapA, snapB)
}

// 向候选快照注入一条"属该 case 领域"的差异，验证比较器能检出并落到正确字段。
// 每个 case 注入与其语义相关的字段，而不是一律改 event content，从而证明对应领域的比较器确实在跑。
func assertInjectedMismatchDetected(t *testing.T, caseName string, baseline, candidate *normalize.SnapShot) {
	t.Helper()

	injector, wantDomain, wantPrefix, ok := injectorForCase(caseName, candidate)
	if !ok {
		// 该 case 没有可注入的目标字段，回退到事件内容，保证仍能检出。
		if len(candidate.Events) == 0 {
			t.Fatalf("%s 没有可注入差异的事件", caseName)
		}
		candidate.Events[0].Content += " [注入差异]"
		wantDomain, wantPrefix = compare.DomainEvent, "events[0].content"
	} else {
		injector(candidate)
	}

	report := compare.CompareSession(compare.Context{
		Case: caseName, BaselineBackend: "session/inmemory", CandidateBackend: "session/sqlite", Scope: compare.ScopeSession,
	}, baseline, candidate, compare.DefaultAllowedRules())
	if report.Passed {
		t.Fatalf("%s: 注入的领域差异未被检出", caseName)
	}
	// 差异必须落在期望的字段路径前缀上，证明比较器定位到了正确的 domain。
	for _, diff := range report.Diffs {
		if diff.Domain == wantDomain && strings.HasPrefix(diff.FieldPath, wantPrefix) {
			return
		}
	}
	t.Fatalf("%s: 注入差异未被定位到 %s/%s，报告: %+v", caseName, wantDomain, wantPrefix, report.Diffs)
}

// injectorForCase 返回与 case 语义相关的注入函数、期望 domain 与字段路径前缀。
// 第二个返回值 ok 为 false 表示该 case 没有可注入的目标字段。
func injectorForCase(
	caseName string,
	candidate *normalize.SnapShot,
) (func(*normalize.SnapShot), compare.Domain, string, bool) {
	switch caseName {
	case scenario.Case03_UpdateState.Name:
		// state 覆盖语义错误。
		return func(s *normalize.SnapShot) {
			s.State["final"] = "dirty"
		}, compare.DomainState, "state.final", true

	case scenario.Case04_ToolCall.Name:
		// 工具调用参数被篡改。
		return func(s *normalize.SnapShot) {
			for i := range s.Events {
				if len(s.Events[i].ToolCalls) > 0 {
					s.Events[i].ToolCalls[0].Args = `{"city":"注入"}`
					return
				}
			}
		}, compare.DomainEvent, "events[", true

	case scenario.Case06_Summary.Name:
		// summary 正文被覆盖。
		return func(s *normalize.SnapShot) {
			if summary, ok := s.Summaries[""]; ok {
				summary.Text = "wrong summary"
				s.Summaries[""] = summary
			}
		}, compare.DomainSummary, `summaries[""]`, true

	case scenario.Case06_SummaryFilterKey.Name:
		// Go 必需的 summary filter-key 错误。
		return func(s *normalize.SnapShot) {
			if summary, ok := s.Summaries["weather"]; ok {
				summary.FilterKey = "wrong"
				s.Summaries["weather"] = summary
			}
		}, compare.DomainSummary, `summaries["weather"].filter_key`, true

	case scenario.Case07_SummaryWithTruncation.Name:
		// 摘要截断边界错误。
		return func(s *normalize.SnapShot) {
			if summary, ok := s.Summaries[""]; ok {
				summary.LastEventID = "wrong"
				s.Summaries[""] = summary
			}
		}, compare.DomainSummary, `summaries[""].last_event_id`, true

	case scenario.Case08_Track.Name:
		// track 状态字段错误（duration_ms 是 allowed_diff，不能用来证明检出）。
		return func(s *normalize.SnapShot) {
			for name, events := range s.Tracks {
				for i := range events {
					if mutated, ok := mutateTrackStatus(events[i].Payload); ok {
						s.Tracks[name][i].Payload = mutated
						return
					}
				}
			}
		}, compare.DomainTrack, `tracks[`, true
	}
	return nil, "", "", false
}

// mutateTrackStatus 把 track payload 中的非耗时字段 status 改成注入值，
// 用于证明 track 比较器能检出非 allowed_diff 的差异。
func mutateTrackStatus(payload string) (string, bool) {
	var value map[string]any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return "", false
	}
	if _, ok := value["status"]; !ok {
		return "", false
	}
	value["status"] = "injected"
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// 模拟首次 AppendEvent 传输失败，用于 Case10 恢复测试。
type failFirstAppendService struct {
	session.Service
	failed bool
}

func (s *failFirstAppendService) AppendEvent(ctx context.Context, sess *session.Session, evt *event.Event, opts ...session.Option) error {
	if !s.failed {
		s.failed = true
		return errors.New("注入的追加失败")
	}
	return s.Service.AppendEvent(ctx, sess, evt, opts...)
}

// 校验单个 case 的业务期望，不依赖后端实现细节。
func assertCaseExpectations(
	t *testing.T,
	caseName string,
	snapshot *normalize.SnapShot,
) {
	t.Helper()
	switch caseName {
	case scenario.Case03_UpdateState.Name:
		if len(snapshot.State) != 1 || snapshot.State["final"] != "clean" {
			t.Fatalf("状态覆盖/删除/清空语义错误: %+v", snapshot.State)
		}
	case scenario.Case07_SummaryWithTruncation.Name:
		summary, ok := snapshot.Summaries[""]
		if !ok || summary.LastEventID != "case07-a2" {
			t.Fatalf("summary boundary 错误: %+v", snapshot.Summaries)
		}
		if len(snapshot.Events) != 2 ||
			snapshot.Events[0].ID != "case07-u3" ||
			snapshot.Events[1].ID != "case07-a3" {
			t.Fatalf("截断后事件错误: %+v", snapshot.Events)
		}
	case scenario.Case09_ConcurrentAppend.Name:
		want := []string{"case09-u1", "case09-a1", "case09-u2"}
		if len(snapshot.Events) != len(want) {
			t.Fatalf("并发事件数量错误: %+v", snapshot.Events)
		}
		for i, id := range want {
			if snapshot.Events[i].ID != id {
				t.Fatalf("并发事件顺序错误: %+v", snapshot.Events)
			}
		}
	case scenario.Case10_Recovery.Name:
		if snapshot.State["recovered"] != "ok" {
			t.Fatalf("恢复状态丢失: %+v", snapshot.State)
		}
		if len(snapshot.Events) != 1 ||
			snapshot.Events[0].ID != "case10-u1" {
			t.Fatalf("恢复后的事件错误: %+v", snapshot.Events)
		}
	}
}
