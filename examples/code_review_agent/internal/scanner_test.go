//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"strings"
	"testing"
)

// scannerTestFile 构造一个单 hunk 的测试 DiffFile。
func scannerTestFile(lines ...Line) DiffFile {
	return DiffFile{
		OldPath: "a.go",
		NewPath: "a.go",
		Hunks: []Hunk{{
			OldStart: 1, OldCount: len(lines), NewStart: 1, NewCount: len(lines),
			Lines: lines,
		}},
	}
}

// addLine 构造一个新增行。
func addLine(no int, content string) Line {
	return Line{Type: LineAdd, Content: content, NewNo: no}
}

// scanIDs 返回 ScanFile 产出的非重复 finding 的 ruleID 集合。
func scanIDs(t *testing.T, df DiffFile) []string {
	t.Helper()
	sc := NewRuleScanner()
	fs := sc.ScanFile(df)
	var ids []string
	for _, f := range fs {
		if !f.IsDuplicate {
			ids = append(ids, f.RuleID)
		}
	}
	return ids
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func join(s []string) string { return strings.Join(s, ",") }

// TestGoroutineLeak_NamedGoNotFlagged 验证命名 goroutine（go worker()）不再误报
// goroutine 泄漏：它有独立的 worker 函数，可能自带 channel/context 关闭机制。
func TestGoroutineLeak_NamedGoNotFlagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "go worker(shutdownCh)"),
	)
	ids := scanIDs(t, df)
	if contains(ids, "concur_goroutine_leak_001") {
		t.Errorf("命名 goroutine 不应报泄漏, got %v", ids)
	}
}

// TestGoroutineLeak_AnonymousFlagged 验证匿名 goroutine 命中泄漏规则。
func TestGoroutineLeak_AnonymousFlagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "go func() {"),
		addLine(2, "\tdone <- true"),
		addLine(3, "}()"),
	)
	ids := scanIDs(t, df)
	if !contains(ids, "concur_goroutine_leak_001") {
		t.Errorf("匿名 goroutine 应报泄漏, got %v", ids)
	}
}

// TestConcurContext_FiredOnGoroutineBlockLoop 验证文件级组合信号：
// 匿名 goroutine + 裸 for 阻塞循环 + 无 ctx.Done() -> 报无退出机制。
func TestConcurContext_FiredOnGoroutineBlockLoop(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "go func() {"),
		addLine(2, "\tfor {"),
		addLine(3, "\t\tprocess()"),
		addLine(4, "\t}"),
		addLine(5, "}()"),
	)
	ids := scanIDs(t, df)
	if !contains(ids, "concur_context_not_checked_001") {
		t.Errorf("goroutine 阻塞循环无 ctx 应报, got %v", ids)
	}
}

// TestConcurContext_NotFiredWithoutGoroutine 验证：只有 for/select 而无
// goroutine 时不报（此前行级 `for\s+\{` 会误报一切循环）。
func TestConcurContext_NotFiredWithoutGoroutine(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "for {"),
		addLine(2, "\titems = append(items, next())"),
		addLine(3, "}"),
	)
	ids := scanIDs(t, df)
	if contains(ids, "concur_context_not_checked_001") {
		t.Errorf("无 goroutine 的循环不应报, got %v", ids)
	}
}

// TestConcurContext_NotFiredWhenCtxDone 验证：存在 ctx.Done() 时不报，
// 说明组合信号正确识别了有取消机制的循环。
func TestConcurContext_NotFiredWhenCtxDone(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "go func() {"),
		addLine(2, "\tfor {"),
		addLine(3, "\t\tselect {"),
		addLine(4, "\t\tcase <-ctx.Done():"),
		addLine(5, "\t\t\treturn"),
		addLine(6, "\t\tdefault:"),
		addLine(7, "\t\t\tprocess()"),
		addLine(8, "\t\t}"),
		addLine(9, "\t}"),
		addLine(10, "}()"),
	)
	ids := scanIDs(t, df)
	if contains(ids, "concur_context_not_checked_001") {
		t.Errorf("已有 ctx.Done() 的循环不应报 context 未检查, got %v", ids)
	}
}

// TestErrUnchecked_FmtSprintfNotFlagged 验证白名单化后 fmt.Sprintf 不误报。
func TestErrUnchecked_FmtSprintfNotFlagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, `_ = fmt.Sprintf("%s:%d", host, port)`),
	)
	ids := scanIDs(t, df)
	if contains(ids, "err_unchecked_001") {
		t.Errorf("fmt.Sprintf 不返回 error, 不应报 err_unchecked, got %v", ids)
	}
}

// TestErrUnchecked_OSRemoveFlagged 验证确知返回 error 的调用被检出。
func TestErrUnchecked_OSRemoveFlagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "_ = os.Remove(tmpFile)"),
	)
	ids := scanIDs(t, df)
	if !contains(ids, "err_unchecked_001") {
		t.Errorf("os.Remove 忽略错误应报, got %v", ids)
	}
}

// TestOpenWithDeferClose_NotFlagged 验证文件级二次验证：已有 defer f.Close()
// 时不报"打开未关闭"。
func TestOpenWithDeferClose_NotFlagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "f, err := os.Open(path)"),
		addLine(2, "if err != nil {"),
		addLine(3, "\treturn err"),
		addLine(4, "}"),
		addLine(5, "defer f.Close()"),
	)
	ids := scanIDs(t, df)
	if contains(ids, "res_open_without_close_001") {
		t.Errorf("已有 defer f.Close() 不应报未关闭, got %v", ids)
	}
}

// TestOpenWithoutDefer_Flagged 验证无 defer 时仍报。
func TestOpenWithoutDefer_Flagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "f, err := os.Open(path)"),
		addLine(2, "if err != nil {"),
		addLine(3, "\treturn err"),
		addLine(4, "}"),
	)
	ids := scanIDs(t, df)
	if !contains(ids, "res_open_without_close_001") {
		t.Errorf("无 defer Close 的 os.Open 应报, got %v", ids)
	}
}

// TestHTTPBody_DeferredNotFlagged 验证 defer resp.Body.Close() 时不再报
// HTTP body 未关闭（属性形式 Close 的二次验证）。
func TestHTTPBody_DeferredNotFlagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "resp, err := http.Get(url)"),
		addLine(2, "if err != nil {"),
		addLine(3, "\treturn err"),
		addLine(4, "}"),
		addLine(5, "defer resp.Body.Close()"),
	)
	ids := scanIDs(t, df)
	if contains(ids, "res_http_body_not_closed_001") {
		t.Errorf("已有 defer resp.Body.Close() 不应报, got %v", ids)
	}
}

// TestDBNoPing_WithPing_NotFlagged 验证 sql.Open + db.Ping 不报未 Ping，
// 且 defer db.Close() 时也不报未关闭。
func TestDBNoPing_WithPing_NotFlagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, `db, err := sql.Open("sqlite3", "x.db")`),
		addLine(2, "if err != nil {"),
		addLine(3, "\treturn err"),
		addLine(4, "}"),
		addLine(5, "defer db.Close()"),
		addLine(6, "if err := db.Ping(); err != nil {"),
		addLine(7, "\treturn err"),
		addLine(8, "}"),
	)
	ids := scanIDs(t, df)
	if contains(ids, "db_no_ping_001") {
		t.Errorf("有 db.Ping() 不应报未 Ping, got %v", ids)
	}
	if contains(ids, "db_no_close_001") {
		t.Errorf("已有 defer db.Close() 不应报未关闭, got %v", ids)
	}
}

// TestDBNoPing_NoPing_Flagged 验证 sql.Open 后无 Ping 时文件级规则报出。
func TestDBNoPing_NoPing_Flagged(t *testing.T) {
	df := scannerTestFile(
		addLine(1, `db, err := sql.Open("sqlite3", "x.db")`),
		addLine(2, "if err != nil {"),
		addLine(3, "\treturn err"),
		addLine(4, "}"),
		addLine(5, "return db, nil"),
	)
	ids := scanIDs(t, df)
	if !contains(ids, "db_no_ping_001") {
		t.Errorf("sql.Open 后无 Ping 应报, got %v", ids)
	}
}

// TestRuleConfidence 验证启发式规则携带了下调的置信度，
// 保证低置信度问题能落入 warnings / needs_human_review。
func TestRuleConfidence(t *testing.T) {
	df := scannerTestFile(
		addLine(1, "go func() {"),
		addLine(2, "}()"),
	)
	sc := NewRuleScanner()
	for _, f := range sc.ScanFile(df) {
		if f.RuleID == "concur_goroutine_leak_001" && f.Confidence != 0.8 {
			t.Errorf("goroutine 泄漏规则置信度应为 0.8, got %v", f.Confidence)
		}
	}
}
