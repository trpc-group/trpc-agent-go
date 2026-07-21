package agent

import (
	"path/filepath"
	"testing"
	"time"
)

func TestJSONStoreRoundTrip(t *testing.T) {
	store := &JSONStore{Path: filepath.Join(t.TempDir(), "store.json")}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	task := ReviewTask{ID: "task_1", Status: TaskStatusRunning, InputKind: "diff", StartedAt: time.Now()}
	if err := store.SaveTask(task); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePermissionDecision(PermissionDecision{ID: "perm_1", TaskID: task.ID, Decision: DecisionAllow}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSandboxRun(SandboxRun{ID: "run_1", TaskID: task.ID, Status: "failed", ErrorType: "forced_failure"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFinding(task.ID, Finding{Severity: SeverityHigh, Category: "security", File: "a.go", Line: 1, RuleID: "R"}, "finding"); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetTaskRecord(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Task.ID != task.ID || len(record.PermissionDecisions) != 1 || len(record.SandboxRuns) != 1 || len(record.Findings) != 1 {
		t.Fatalf("bad record: %+v", record)
	}
}
