//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

func TestParseGoVetOutput_Standard(t *testing.T) {
	output := `# command-line-arguments
./main.go:15:2: unreachable code
./handler.go:42:6: variable 'err' is not used
`
	findings := ParseGoVetOutput(output, "task-1")
	require.Len(t, findings, 2)

	assert.Equal(t, "main.go", findings[0].File)
	assert.Equal(t, 15, findings[0].Line)
	assert.Equal(t, 2, findings[0].Column)
	assert.Equal(t, "unreachable code", findings[0].Title)
	assert.Equal(t, "go_vet", string(findings[0].Source))

	assert.Equal(t, "handler.go", findings[1].File)
	assert.Equal(t, 42, findings[1].Line)
}

func TestParseGoVetOutput_Empty(t *testing.T) {
	findings := ParseGoVetOutput("", "task-1")
	assert.Empty(t, findings)

	findings = ParseGoVetOutput("ok  	package\n", "task-1")
	assert.Empty(t, findings)
}

func TestParseGoVetOutput_NoColumn(t *testing.T) {
	// Some go vet output doesn't include column number.
	output := "./main.go:10: missing return at end of function\n"
	findings := ParseGoVetOutput(output, "task-1")
	require.Len(t, findings, 1)
	assert.Equal(t, "main.go", findings[0].File)
	assert.Equal(t, 10, findings[0].Line)
	assert.Equal(t, 0, findings[0].Column) // no column
}

func TestParseGoVetOutput_VetDiagnostic(t *testing.T) {
	output := "./server.go:88:2: call to `cancel` function not used\n"
	findings := ParseGoVetOutput(output, "task-1")
	require.Len(t, findings, 1)
	assert.Equal(t, "server.go", findings[0].File)
	assert.Equal(t, 88, findings[0].Line)
	assert.Contains(t, findings[0].Recommendation, "go vet")
}

func TestParseStaticcheckOutput(t *testing.T) {
	output := "./main.go:10:2: this value of err is never used (SA4006)\n"
	findings := ParseStaticcheckOutput(output, "task-1")
	require.Len(t, findings, 1)
	assert.Equal(t, "main.go", findings[0].File)
	assert.Equal(t, finding.SourceStaticcheck, findings[0].Source)
	assert.Equal(t, "STATICCHECK_DIAGNOSTIC", findings[0].RuleID)
}

func TestParseGoTestOutput_Failure(t *testing.T) {
	output := `--- FAIL: TestHandler (0.01s)
    handler_test.go:25: expected 200, got 500
`
	findings := ParseGoTestOutput(output, "task-1")
	require.Len(t, findings, 1)
	assert.Equal(t, "handler_test.go", findings[0].File)
	assert.Equal(t, 25, findings[0].Line)
	assert.Contains(t, findings[0].Title, "Handler")
	assert.Equal(t, finding.SeverityHigh, findings[0].Severity)
}

func TestParseGoTestOutput_Pass(t *testing.T) {
	output := `ok  	package	0.002s
`
	findings := ParseGoTestOutput(output, "task-1")
	assert.Empty(t, findings)
}

func TestParseGoVetOutput_SkipNonDiagnostic(t *testing.T) {
	output := `# package
ok  	package
?  	package
FAIL
go: downloading dep
---
`
	findings := ParseGoVetOutput(output, "task-1")
	assert.Empty(t, findings)
}

func TestParseStaticcheckOutput_Empty(t *testing.T) {
	findings := ParseStaticcheckOutput("", "task-1")
	assert.Empty(t, findings)
}

func TestParseGoVetOutput_WithFilePath(t *testing.T) {
	// Absolute path should be preserved.
	output := "/home/user/project/main.go:15:2: unreachable code\n"
	findings := ParseGoVetOutput(output, "task-1")
	require.Len(t, findings, 1)
	assert.Equal(t, "/home/user/project/main.go", findings[0].File)
}

func TestParseGoVetOutput_MixedProgress(t *testing.T) {
	output := `# package/path
ok  	package/path	0.002s
./util.go:5:6: exported func X should have comment
`
	findings := ParseGoVetOutput(output, "task-1")
	require.Len(t, findings, 1)
	assert.Equal(t, "util.go", findings[0].File)
	assert.Equal(t, 5, findings[0].Line)
}
