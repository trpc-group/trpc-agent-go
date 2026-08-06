// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package rules

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// checker 是测试用的规则接口。
type checker interface {
	Check(diff.FileDiff) ([]findings.Finding, error)
}

func checkTokenRule(t *testing.T, diffContent string, rule checker) []findings.Finding {
	t.Helper()
	files, err := diff.Parse(strings.NewReader(diffContent))
	if err != nil {
		t.Fatalf("解析 diff 失败: %v", err)
	}
	results, err := rule.Check(files[0])
	if err != nil {
		t.Fatalf("规则执行失败: %v", err)
	}
	return results
}

func mustParseMultiFile(t *testing.T, diffContent string) []diff.FileDiff {
	t.Helper()
	files, err := diff.Parse(strings.NewReader(diffContent))
	if err != nil {
		t.Fatalf("解析 diff 失败: %v", err)
	}
	return files
}

// ========== SEC-AST-001: 硬编码密钥 ==========

func TestTokenSecretRule_HardcodedPassword(t *testing.T) {
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	password := "secret123456"
 }
`
	rule := NewTokenSecretRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("应检测到硬编码密码")
	}
	if results[0].Confidence < 0.85 {
		t.Errorf("置信度 = %.2f, 期望 >= 0.85", results[0].Confidence)
	}
}

func TestTokenSecretRule_EnvVarNotReported(t *testing.T) {
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	password := os.Getenv("PASSWORD")
 }
`
	rule := NewTokenSecretRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("环境变量不应报告，发现 %d 个", len(results))
	}
}

func TestTokenSecretRule_CommentNotReported(t *testing.T) {
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	// password is not hardcoded here
 }
`
	rule := NewTokenSecretRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("注释不应报告，发现 %d 个", len(results))
	}
}

func TestTokenSecretRule_Placeholder(t *testing.T) {
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	password := "your-password-here"
 }
`
	rule := NewTokenSecretRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("占位符不应报告，发现 %d 个", len(results))
	}
}

func TestTokenSecretRule_ApiKey(t *testing.T) {
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	apiKey := "sk_prod_AbcDefGhiJklMnoPqrStu12"
 }
`
	rule := NewTokenSecretRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("应检测到硬编码 API Key")
	}
}

func TestTokenSecretRule_NormalCode(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main

+import "fmt"
`
	rule := NewTokenSecretRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("正常代码不应报告，发现 %d 个", len(results))
	}
}

// ========== SEC-AST-002: 敏感信息泄漏 ==========

func TestTokenLeakRule_AWSKey(t *testing.T) {
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	key := "AKIAIOSFODNN7EXAMPLE"
 }
`
	rule := NewTokenLeakRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("应检测到 AWS Key")
	}
}

func TestTokenLeakRule_GitHubToken(t *testing.T) {
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	tok := "ghp_FAKEtestTokenForDetection000000000000"
 }
`
	rule := NewTokenLeakRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("应检测到 GitHub Token")
	}
}

func TestTokenLeakRule_PrivateKey(t *testing.T) {
	input := `--- a/cert.go
+++ b/cert.go
@@ -5,2 +5,3 @@
 func loadKey() {
+	pem := "-----BEGIN RSA PRIVATE KEY-----"
 }
`
	rule := NewTokenLeakRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("应检测到私钥")
	}
}

func TestTokenLeakRule_DBConnectionString(t *testing.T) {
	input := `--- a/db.go
+++ b/db.go
@@ -5,2 +5,3 @@
 func connect() {
+	dsn := "postgres://admin:secret@localhost:5432/mydb"
 }
`
	rule := NewTokenLeakRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("应检测到数据库连接串")
	}
}

func TestTokenLeakRule_JWTToken(t *testing.T) {
	input := `--- a/auth.go
+++ b/auth.go
@@ -5,2 +5,3 @@
 func getToken() {
+	tok := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456"
 }
`
	rule := NewTokenLeakRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("应检测到 JWT Token")
	}
}

func TestTokenLeakRule_NormalString(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -5,2 +5,3 @@
 func main() {
+	msg := "hello world"
 }
`
	rule := NewTokenLeakRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("正常字符串不应报告，发现 %d 个", len(results))
	}
}

// ========== GOR-AST-001: goroutine 泄漏 ==========

func TestTokenGoroutineRule_LeakyGoroutine(t *testing.T) {
	input := `--- a/service.go
+++ b/service.go
@@ -10,3 +10,8 @@
 func (s *Service) Start() {
+	go func() {
+		for {
+			time.Sleep(5 * time.Minute)
+			s.refresh()
+		}
+	}()
 }
`
	rule := NewTokenGoroutineRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("泄漏的 goroutine 应被检测到")
	}
}

func TestTokenGoroutineRule_SafeWithContext(t *testing.T) {
	input := `--- a/service.go
+++ b/service.go
@@ -10,3 +10,10 @@
 func (s *Service) Start(ctx context.Context) {
+	go func() {
+		for {
+			select {
+			case <-ctx.Done():
+				return
+			case <-time.After(5 * time.Minute):
+				s.refresh()
+			}
+		}
+	}()
 }
`
	rule := NewTokenGoroutineRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("有 context 控制不应报告，发现 %d 个", len(results))
	}
}

func TestTokenGoroutineRule_OneShot(t *testing.T) {
	input := `--- a/handler.go
+++ b/handler.go
@@ -10,2 +10,4 @@
 func (h *Handler) Process() {
+	go func() {
+		h.sendNotification()
+	}()
 }
`
	rule := NewTokenGoroutineRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("一次性 goroutine 不应报告，发现 %d 个", len(results))
	}
}

// ========== RES-AST-001: 资源泄漏 ==========

func TestTokenResourceRule_FileOpenNoClose(t *testing.T) {
	input := `--- a/loader.go
+++ b/loader.go
@@ -5,3 +5,5 @@
 func load(path string) error {
+	f, err := os.Open(path)
+	if err != nil { return err }
 	return nil
 }
`
	rule := NewTokenResourceRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("文件未关闭应被检测到")
	}
}

func TestTokenResourceRule_FileOpenWithDefer(t *testing.T) {
	input := `--- a/loader.go
+++ b/loader.go
@@ -5,4 +5,6 @@
 func load(path string) error {
+	f, err := os.Open(path)
+	if err != nil { return err }
+	defer f.Close()
 	return nil
 }
`
	rule := NewTokenResourceRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("有 defer Close 不应报告，发现 %d 个", len(results))
	}
}

func TestTokenResourceRule_HTTPResponseNoClose(t *testing.T) {
	input := `--- a/client.go
+++ b/client.go
@@ -5,3 +5,5 @@
 func fetch(url string) ([]byte, error) {
+	resp, err := http.Get(url)
+	if err != nil { return nil, err }
 	return io.ReadAll(resp.Body)
 }
`
	rule := NewTokenResourceRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("HTTP 响应未关闭应被检测到")
	}
}

// ========== ERR-AST-001: 错误处理 ==========

func TestTokenErrorRule_IgnoredError(t *testing.T) {
	input := `--- a/handler.go
+++ b/handler.go
@@ -5,2 +5,3 @@
 func process() {
+	result, _ := doSomething()
 }
`
	rule := NewTokenErrorRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("用 _ 忽略错误应被检测到")
	}
}

func TestTokenErrorRule_SafeIgnoredWrite(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -5,2 +5,3 @@
 func main() {
+	fmt.Println("hello")
 }
`
	rule := NewTokenErrorRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("fmt.Println 不应报告，发现 %d 个", len(results))
	}
}

func TestTokenErrorRule_PanicInLibrary(t *testing.T) {
	input := `--- a/parser.go
+++ b/parser.go
@@ -5,2 +5,3 @@
 func parse() {
+	panic("not implemented")
 }
`
	rule := NewTokenErrorRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("库代码 panic 应被检测到")
	}
}

func TestTokenErrorRule_PanicInTest(t *testing.T) {
	input := `--- a/parser_test.go
+++ b/parser_test.go
@@ -5,2 +5,3 @@
 func TestParse(t *testing.T) {
+	panic("test")
 }
`
	rule := NewTokenErrorRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("测试中 panic 不应报告，发现 %d 个", len(results))
	}
}

func TestTokenErrorRule_LogFatalInLibrary(t *testing.T) {
	input := `--- a/server.go
+++ b/server.go
@@ -5,2 +5,3 @@
 func start() {
+	log.Fatal(err)
 }
`
	rule := NewTokenErrorRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("库代码 log.Fatal 应被检测到")
	}
}

func TestTokenErrorRule_LogFatalInMain(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -5,2 +5,3 @@
 func main() {
+	log.Fatal(err)
 }
`
	rule := NewTokenErrorRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("main.go 中 log.Fatal 不应报告，发现 %d 个", len(results))
	}
}

func TestTokenErrorRule_SwallowedError(t *testing.T) {
	input := `--- a/handler.go
+++ b/handler.go
@@ -5,3 +5,5 @@
 func process() error {
+	if err != nil {
+		return nil
+	}
 	return nil
 }
`
	rule := NewTokenErrorRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("错误被吞没应被检测到")
	}
}

// ========== TST-AST-001: 测试缺失 ==========

func TestTokenMissingTestRule_NoTest(t *testing.T) {
	input := `--- a/greeter.go
+++ b/greeter.go
@@ -3,2 +3,5 @@
 package greeter
+
+func Greet(name string) string {
+	return "hello, " + name
+}
`
	rule := NewTokenMissingTestRule()
	results := checkTokenRule(t, input, rule)
	if len(results) == 0 {
		t.Fatal("无测试的导出函数应被检测到")
	}
}

func TestTokenMissingTestRule_UnexportedFunc(t *testing.T) {
	input := `--- a/util.go
+++ b/util.go
@@ -3,2 +3,5 @@
 package util
+
+func helper() string {
+	return ""
+}
`
	rule := NewTokenMissingTestRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("非导出函数不应报告，发现 %d 个", len(results))
	}
}

func TestTokenMissingTestRule_MainFunc(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -3,2 +3,5 @@
 package main
+
+func main() {
+	// entry
+}
`
	rule := NewTokenMissingTestRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("main 函数不应报告，发现 %d 个", len(results))
	}
}

func TestTokenMissingTestRule_TestFile(t *testing.T) {
	input := `--- a/handler_test.go
+++ b/handler_test.go
@@ -3,2 +3,5 @@
 package handler
+
+func TestSomething(t *testing.T) {
+	// test
+}
`
	rule := NewTokenMissingTestRule()
	results := checkTokenRule(t, input, rule)
	if len(results) != 0 {
		t.Errorf("测试文件不应被检查，发现 %d 个", len(results))
	}
}

func TestTokenMissingTestRule_CrossFileCheckFiles(t *testing.T) {
	input := `--- a/greeter.go
+++ b/greeter.go
@@ -3,2 +3,5 @@
 package greeter
+
+func Greet(name string) string {
+	return "hello, " + name
+}
--- a/greeter_test.go
+++ b/greeter_test.go
@@ -0,0 +1,5 @@
+package greeter
+
+func TestGreet(t *testing.T) {
+	got := Greet("test")
+	_ = got
+}
`
	rule := NewTokenMissingTestRule()
	files := mustParseMultiFile(t, input)
	results, _ := rule.CheckFiles(files)
	if len(results) != 0 {
		t.Errorf("跨文件有测试不应报告，发现 %d 个", len(results))
	}
}

// ========== 辅助函数测试 ==========

func TestIsSensitiveIdent(t *testing.T) {
	tests := []struct {
		ident string
		want  bool
	}{
		{"password", true},
		{"apiKey", true},
		{"secret_key", true},
		{"host", false},
		{"name", false},
	}
	for _, tt := range tests {
		got := isSensitiveIdent(tt.ident)
		if got != tt.want {
			t.Errorf("isSensitiveIdent(%q) = %v, 期望 %v", tt.ident, got, tt.want)
		}
	}
}

func TestIsLikelyNotSecret(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{`"secret123456"`, false},
		{`"your-password"`, true},
		{`"short"`, true},
		{`"example-value"`, true},
		{`"real-token-abc123"`, false},
	}
	for _, tt := range tests {
		got := isLikelyNotSecret(tt.value)
		if got != tt.want {
			t.Errorf("isLikelyNotSecret(%q) = %v, 期望 %v", tt.value, got, tt.want)
		}
	}
}
