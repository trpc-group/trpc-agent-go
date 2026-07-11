package backend

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// new 一个 temp sqlite db service
func NewSQLiteService(t *testing.T, opts ...sqlite.ServiceOpt) session.Service {
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
