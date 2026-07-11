package replaytest

import (
	"context"
	"testing"

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

	baseline := inmemory.NewSessionService(inmemory.WithSummarizer(summary.FakeSummarizer{}))
	candidate := backend.NewSQLiteService(t, sqlite.WithSummarizer(summary.FakeSummarizer{}))
	// sessA : inmemroy 返回session  sessB : sqlite 返回session
	sessA, err := harness.Run(ctx, baseline, c)
	if err != nil {
		t.Fatalf("run svcA: %v", err)
	}

	sessB, err := harness.Run(ctx, candidate, c)
	if err != nil {
		t.Fatalf("run svcB: %v", err)
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
		t.Fatalf("snapshot diff:\n%s", data)
	}
}

// 校验单个 case 的业务期望，不依赖后端实现细节。
func assertCaseExpectations(
	t *testing.T,
	caseName string,
	snapshot *normalize.SnapShot,
) {
	t.Helper()
	switch caseName {
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

