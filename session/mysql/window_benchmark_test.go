//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storagemysql "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

var windowBenchBase = time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)

type windowBenchDataset struct {
	name                          string
	rows, contentBytes, roleEvery int
}

var windowBenchDatasets = []windowBenchDataset{
	{"small128", 128, 512, 1},
	{"small4096", 4096, 512, 1},
	{"large4096", 4096, 32768, 1},
	{"small32768", 32768, 512, 1},
	{"sparse8192", 8192, 512, 128},
}

// prepareWindowBenchmark creates isolated default-schema tables; no fixture or
// index is shared with application data. Fixture creation is outside timing.
func prepareWindowBenchmark(b *testing.B) (string, string) {
	b.Helper()
	dsn := os.Getenv("TRPC_AGENT_GO_MYSQL_TEST_DSN")
	if dsn == "" {
		b.Skip("set TRPC_AGENT_GO_MYSQL_TEST_DSN to run MySQL benchmarks")
	}
	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		b.Fatal(err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	dsn = cfg.FormatDSN()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	prefix := fmt.Sprintf("wb_%x_", time.Now().UnixNano())
	b.Cleanup(func() {
		for i := len(tableDefs) - 1; i >= 0; i-- {
			_, err := db.Exec("DROP TABLE IF EXISTS " + sqldb.BuildTableName(prefix, tableDefs[i].name))
			if err != nil {
				b.Errorf("clean benchmark table: %v", err)
			}
		}
	})
	svc, err := NewService(WithMySQLClientDSN(dsn), WithTablePrefix(prefix))
	if err != nil {
		b.Fatal(err)
	}
	defer svc.Close()
	for _, data := range windowBenchDatasets {
		key := session.Key{AppName: "perf-app", UserID: "perf-user", SessionID: data.name}
		if _, err := svc.CreateSession(context.Background(), key, nil); err != nil {
			b.Fatal(err)
		}
		if _, err := db.Exec("UPDATE "+svc.tableSessionStates+" SET created_at = ? WHERE session_id = ?", windowBenchBase, data.name); err != nil {
			b.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = tx.Rollback() })
		stmt, err := tx.Prepare("INSERT INTO " + svc.tableSessionEvents + "(app_name,user_id,session_id,event,created_at) VALUES(?,?,?,?,?)")
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < data.rows; i++ {
			role := model.RoleUser
			if i%data.roleEvery == 0 {
				role = model.RoleAssistant
			}
			evt := event.Event{
				ID:        fmt.Sprintf("e-%06d", i),
				Timestamp: windowBenchBase.Add(time.Duration(i) * time.Microsecond),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Role: role, Content: strings.Repeat("x", data.contentBytes)},
				}}},
			}
			payload, err := json.Marshal(evt)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := stmt.Exec(key.AppName, key.UserID, key.SessionID, payload, evt.Timestamp); err != nil {
				b.Fatal(err)
			}
		}
		if err := stmt.Close(); err != nil {
			b.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
		b.Logf("prepared %s: %d rows, content %d bytes", data.name, data.rows, data.contentBytes)
	}
	return dsn, prefix
}

type windowBenchClient struct {
	storagemysql.Client
	queries int
}

func (c *windowBenchClient) Query(ctx context.Context, next storagemysql.NextFunc, query string, args ...any) error {
	c.queries++
	return c.Client.Query(ctx, next, query, args...)
}

// BenchmarkService_GetEventWindowMySQL checks exact windows on the default
// schema, including a sparse-role scan. Compare timings against the base branch;
// queries/op counts client calls, not network round trips.
func BenchmarkService_GetEventWindowMySQL(b *testing.B) {
	dsn, prefix := prepareWindowBenchmark(b)
	cases := []struct {
		name, dataset         string
		anchor, before, after int
		roles                 []model.Role
	}{
		{"short", "small128", 64, 2, 2, nil},
		{"4k-small", "small4096", 2048, 5, 5, nil},
		{"4k-large", "large4096", 2048, 5, 5, nil},
		{"32k-small", "small32768", 16384, 5, 5, nil},
		{"4k-anchor-start", "small4096", 0, 0, 0, nil},
		{"4k-anchor-middle", "small4096", 2048, 0, 0, nil},
		{"8k-sparse-role", "sparse8192", 4096, 2, 2, []model.Role{model.RoleAssistant}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			db, err := sql.Open("mysql", dsn)
			if err != nil {
				b.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			b.Cleanup(func() { _ = db.Close() })
			client := &windowBenchClient{Client: storagemysql.WrapSQLDB(db)}
			svc := &Service{
				mysqlClient:        client,
				tableSessionStates: sqldb.BuildTableName(prefix, sqldb.TableNameSessionStates),
				tableSessionEvents: sqldb.BuildTableName(prefix, sqldb.TableNameSessionEvents),
			}
			req := session.EventWindowRequest{
				Key:           session.Key{AppName: "perf-app", UserID: "perf-user", SessionID: tc.dataset},
				AnchorEventID: fmt.Sprintf("e-%06d", tc.anchor),
				Before:        tc.before, After: tc.after, Roles: tc.roles,
			}
			check := func() {
				got, err := svc.GetEventWindow(context.Background(), req)
				if err != nil {
					b.Fatal(err)
				}
				if len(got.Entries) != tc.before+tc.after+1 {
					b.Fatalf("unexpected size %d", len(got.Entries))
				}
				step := 1
				if len(tc.roles) != 0 {
					step = 128
				}
				for i, entry := range got.Entries {
					want := fmt.Sprintf("e-%06d", tc.anchor+(i-tc.before)*step)
					if entry.Event.ID != want {
						b.Fatalf("event %d: got %s, want %s", i, entry.Event.ID, want)
					}
				}
			}
			for i := 0; i < 5; i++ {
				check()
			}
			client.queries = 0
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				check()
			}
			b.StopTimer()
			b.ReportMetric(float64(client.queries)/float64(b.N), "queries/op")
		})
	}
}
