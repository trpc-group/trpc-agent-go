//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

func TestSecurityRule_SQLInjection(t *testing.T) {
	rule := NewSecurityRule()
	file := finding.ChangedFileInfo{File: "handler.go"}

	content := `func GetUser(db *sql.DB, id string) {
	rows := db.Query("SELECT * FROM users WHERE id = " + request.UserID)
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_SECURITY_INJECTION", findings[0].RuleID)
	assert.Equal(t, finding.CategorySecurity, findings[0].Category)
	assert.Equal(t, finding.SeverityCritical, findings[0].Severity)
	assert.Equal(t, 2, findings[0].Line)
	assert.Contains(t, findings[0].Evidence, "SELECT")
}

func TestSecurityRule_CommandInjection(t *testing.T) {
	rule := NewSecurityRule()
	file := finding.ChangedFileInfo{File: "exec.go"}

	content := `func runCmd(cmd string) {
	out, _ := exec.Command("bash", "-c", "echo " + cmd).Output()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, "GO_SECURITY_INJECTION", findings[0].RuleID)
}

func TestSecurityRule_HardcodedKey(t *testing.T) {
	rule := NewSecurityRule()
	file := finding.ChangedFileInfo{File: "config.go"}

	content := `var apiKey = "sk-abc123def456ghi789jkl012"
func main() {}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Len(t, findings, 1)
}

func TestSecurityRule_NonGoFile(t *testing.T) {
	rule := NewSecurityRule()
	file := finding.ChangedFileInfo{File: "Makefile"}

	content := `SELECT * FROM users`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestSecurityRule_CleanCode(t *testing.T) {
	rule := NewSecurityRule()
	file := finding.ChangedFileInfo{File: "safe.go"}

	content := `func GetUser(db *sql.DB, id string) {
	rows, _ := db.Query("SELECT * FROM users WHERE id = ?", id)
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestSecurityRule_SuccessPaths(t *testing.T) {
	rule := NewSecurityRule()
	file := finding.ChangedFileInfo{File: "safe_exec.go"}

	content := `func runCmd() {
	out, _ := exec.Command("go", "build", "./...").Output()
}`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestHardcodedKeyRule_OpenAIKey(t *testing.T) {
	rule := NewHardcodedKeyRule()
	file := finding.ChangedFileInfo{File: "config.go"}
	content := `apiKey := "sk-abc123def456ghi789jkl012mnop345"`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Len(t, findings, 1)
	assert.Equal(t, "GO_SECURITY_HARDCODED_KEY", findings[0].RuleID)
	assert.Equal(t, finding.CategorySensitiveInfo, findings[0].Category)
}

func TestHardcodedKeyRule_GitHubToken(t *testing.T) {
	rule := NewHardcodedKeyRule()
	file := finding.ChangedFileInfo{File: "config.go"}
	content := `token := "ghp_abc123def456ghi789jkl012mnop345qrs678tuv"`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Len(t, findings, 1)
}

func TestHardcodedKeyRule_PrivateKey(t *testing.T) {
	rule := NewHardcodedKeyRule()
	file := finding.ChangedFileInfo{File: "key.go"}
	content := `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA...
-----END RSA PRIVATE KEY-----`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Len(t, findings, 1)
}

func TestHardcodedKeyRule_Clean(t *testing.T) {
	rule := NewHardcodedKeyRule()
	file := finding.ChangedFileInfo{File: "config.go"}
	content := `var dbPassword = os.Getenv("DB_PASSWORD")`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	assert.Empty(t, findings)
}

func TestSecurityRule_ExecShCWithConcat(t *testing.T) {
	rule := NewSecurityRule()
	file := finding.ChangedFileInfo{File: "exec.go"}

	content := `exec.Command("sh", "-c", "echo "+input)`
	findings, err := rule.Check(context.Background(), file, content)
	require.NoError(t, err)
	// Matches both command injection and exec-with-concat patterns.
	assert.Len(t, findings, 2)
}

func TestDetectInlineSecret(t *testing.T) {
	assert.NotEmpty(t, detectInlineSecret("sk-abc123def456ghi789jkl012mnop345"))
	assert.NotEmpty(t, detectInlineSecret("ghp_abc123def456ghi789jkl012mnop345qrs678tuv"))
	assert.NotEmpty(t, detectInlineSecret("-----BEGIN EC PRIVATE KEY-----"))
	assert.Empty(t, detectInlineSecret("var x = \"hello world\""))
}
