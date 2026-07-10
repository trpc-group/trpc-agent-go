package replaytest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/compare"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeSummarizer struct{}

func (fakeSummarizer) ShouldSummarize(sess *session.Session) bool {
	return true
}

func (fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "summary:%s:", sess.ID)
	for _, evt := range sess.Events {
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		msg := evt.Response.Choices[0].Message
		fmt.Fprintf(&b, "[%s]%s;", msg.Role, msg.Content)
	}

	return b.String(), nil
}

func (fakeSummarizer) SetPrompt(prompt string) {}

func (fakeSummarizer) SetModel(m model.Model) {}

func (fakeSummarizer) Metadata() map[string]any {
	return nil
}
func TestReplay(t *testing.T) {
	// 待测所有case
	cases := []*scenario.Case{
		scenario.Case01_SingleTurn,
		scenario.Case02_MultiTurn,
		scenario.Case03_UpdateState,
		scenario.Case04_ToolCall,
		scenario.Case06_Summary,
		scenario.Case06_SummaryFilterKey,
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

	baseline := inmemory.NewSessionService(inmemory.WithSummarizer(fakeSummarizer{}))
	candidate := newSQLiteService(t, sqlite.WithSummarizer(fakeSummarizer{}))
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

	diff := compare.MakeDiff(snapA, snapB)
	if len(diff) > 0 {
		t.Fatalf("snapshot diff: %+v", diff)
	}
}

func newSQLiteService(t *testing.T, opts ...sqlite.ServiceOpt) session.Service {
	t.Helper()

	f, err := os.CreateTemp("", "trpc-agent-go-replaytest-*.db")
	if err != nil {
		t.Fatalf("create temp sqlite db: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp sqlite db: %v", err)
	}

	db, err := sql.Open("sqlite3", f.Name())
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatalf("open sqlite db: %v", err)
	}

	svc, err := sqlite.NewService(db, opts...)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatalf("new sqlite service: %v", err)
	}

	t.Cleanup(func() {
		_ = svc.Close()
		_ = os.Remove(f.Name())
	})

	return svc
}
