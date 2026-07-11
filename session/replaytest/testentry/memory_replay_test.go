package replaytest

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/compare"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/memoryharness"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
)

// TestMemoryReplay 在 inmemory 与 sqlite 之间回放记忆 case。
func TestMemoryReplay(t *testing.T) {
	ctx := context.Background()
	baseline := memoryinmemory.NewMemoryService()
	t.Cleanup(func() {
		_ = baseline.Close()
	})
	candidate := newSQLiteMemoryService(t)

	// 分别在两个后端执行同一组写入、读取和搜索。
	resultA, err := memoryharness.Run(
		ctx,
		baseline,
		scenario.Case05_Memory,
	)
	if err != nil {
		t.Fatalf("run memory baseline: %v", err)
	}
	resultB, err := memoryharness.Run(
		ctx,
		candidate,
		scenario.Case05_Memory,
	)
	if err != nil {
		t.Fatalf("run memory candidate: %v", err)
	}

	snapshotA := normalize.FromMemoryEntries(resultA.Read, resultA.Search)
	snapshotB := normalize.FromMemoryEntries(resultB.Read, resultB.Search)
	// 生成结构化差异报告，失败时输出 JSON 方便定位。
	report := compare.CompareMemory(compare.Context{
		Case:             scenario.Case05_Memory.Name,
		BaselineBackend:  "memory/inmemory",
		CandidateBackend: "memory/sqlite",
		Scope:            compare.ScopeMemory,
	}, snapshotA, snapshotB, compare.DefaultAllowedRules())
	if !report.Passed {
		data, _ := compare.MarshalReportSet([]compare.Report{report})
		t.Fatalf("memory snapshot diff:\n%s", data)
	}
}

func newSQLiteMemoryService(t *testing.T) memory.Service {
	t.Helper()

	f, err := os.CreateTemp("", "trpc-agent-go-memory-replaytest-*.db")
	if err != nil {
		t.Fatalf("create temp memory sqlite db: %v", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		t.Fatalf("close temp memory sqlite db: %v", err)
	}

	db, err := sql.Open("sqlite3", f.Name())
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatalf("open memory sqlite db: %v", err)
	}
	svc, err := memorysqlite.NewService(db)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatalf("new memory sqlite service: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Close()
		_ = os.Remove(f.Name())
	})
	return svc
}
