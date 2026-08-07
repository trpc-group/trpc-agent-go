//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrate_AddsNewColumns 验证旧 schema 建出的库能被 migrate() 补齐新列
// （验收标准 3/8：兼容已存在的 review.db，幂等 ALTER）。
func TestMigrate_AddsNewColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "old.db")

	// 手工用旧 schema 建库。
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开旧库失败: %v", err)
	}
	oldDDL := `
CREATE TABLE review_tasks (
	id TEXT PRIMARY KEY,
	input_type TEXT NOT NULL DEFAULT '',
	input_hash TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	created_at INTEGER NOT NULL,
	completed_at INTEGER DEFAULT 0,
	total_files INTEGER DEFAULT 0,
	total_findings INTEGER DEFAULT 0,
	critical_count INTEGER DEFAULT 0,
	high_count INTEGER DEFAULT 0,
	medium_count INTEGER DEFAULT 0,
	low_count INTEGER DEFAULT 0,
	warning_count INTEGER DEFAULT 0,
	duration_ms INTEGER DEFAULT 0,
	error_message TEXT DEFAULT ''
);
CREATE TABLE monitoring_summary (
	task_id TEXT PRIMARY KEY,
	total_duration_ms INTEGER DEFAULT 0,
	sandbox_duration_ms INTEGER DEFAULT 0,
	tool_calls_count INTEGER DEFAULT 0,
	permission_intercepts INTEGER DEFAULT 0,
	finding_count INTEGER DEFAULT 0
);
`
	if _, err := db.Exec(oldDDL); err != nil {
		db.Close()
		t.Fatalf("创建旧 schema 失败: %v", err)
	}
	db.Close()

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore 迁移失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	columns := [][2]string{
		{"review_tasks", "report_json_path"},
		{"review_tasks", "report_md_path"},
		{"review_tasks", "report_generated_at"},
		{"monitoring_summary", "timeout_count"},
		{"monitoring_summary", "sandbox_failure_count"},
	}
	for _, c := range columns {
		ok, err := store.columnExists(ctx, c[0], c[1])
		if err != nil {
			t.Fatalf("columnExists(%s.%s) 失败: %v", c[0], c[1], err)
		}
		if !ok {
			t.Errorf("迁移后缺少列 %s.%s", c[0], c[1])
		}
	}

	// 迁移是幂等的：再次打开不报错。
	store.Close()
	store, err = NewStore(dbPath)
	if err != nil {
		t.Fatalf("二次打开迁移失败: %v", err)
	}
	store.Close()
}

// TestSaveReportMeta_UpdatesTask 验证报告路径落库并可经 GetTask 查询
// （验收标准 3：报告可经 DB 按 task 定位）。
func TestSaveReportMeta_UpdatesTask(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "db.db"))
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	task := NewReviewTask("cr-meta-test", "diff_file", "hash")
	if err := store.SaveTask(ctx, task); err != nil {
		t.Fatalf("SaveTask 失败: %v", err)
	}

	jsonPath := "/tmp/cr/review_report.json"
	mdPath := "/tmp/cr/review_report.md"
	if err := store.SaveReportMeta(ctx, task.ID, jsonPath, mdPath, 12345); err != nil {
		t.Fatalf("SaveReportMeta 失败: %v", err)
	}

	got, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got.ReportJSONPath != jsonPath || got.ReportMDPath != mdPath {
		t.Errorf("report 路径不匹配: json=%q md=%q", got.ReportJSONPath, got.ReportMDPath)
	}
	if got.ReportGeneratedAt != 12345 {
		t.Errorf("report 生成时间不匹配: %d", got.ReportGeneratedAt)
	}
}

// TestSaveMonitoringSummary_AnomalyCounts 验证监控异常分布字段可写可读
// （验收标准 8：记录沙箱超时/失败计数）。
func TestSaveMonitoringSummary_AnomalyCounts(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "db.db"))
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	want := MonitoringSummary{
		TaskID:               "cr-mon-test",
		TotalDurationMs:      100,
		SandboxDurationMs:    30,
		ToolCallsCount:       2,
		PermissionIntercepts: 1,
		FindingCount:         5,
		TimeoutCount:         1,
		SandboxFailureCount:  2,
	}
	if err := store.SaveMonitoringSummary(ctx, want); err != nil {
		t.Fatalf("SaveMonitoringSummary 失败: %v", err)
	}

	got, err := store.GetMonitoringSummary(ctx, want.TaskID)
	if err != nil {
		t.Fatalf("GetMonitoringSummary 失败: %v", err)
	}
	if got.TimeoutCount != 1 || got.SandboxFailureCount != 2 {
		t.Errorf("异常计数不匹配: timeout=%d failure=%d", got.TimeoutCount, got.SandboxFailureCount)
	}
	if got.FindingCount != 5 || got.PermissionIntercepts != 1 {
		t.Errorf("常规字段回读异常: %+v", got)
	}
}
