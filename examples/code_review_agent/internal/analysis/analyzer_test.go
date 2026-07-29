//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

const securityDiff = `diff --git a/handler.go b/handler.go
new file mode 100644
--- /dev/null
+++ b/handler.go
@@ -0,0 +1,7 @@
+package main
+
+import "os/exec"
+
+func handle(cmd string) {
+	exec.Command("/bin/sh", "-c", cmd).Run()
+}`

const goroutineDiff = `diff --git a/worker.go b/worker.go
new file mode 100644
--- /dev/null
+++ b/worker.go
@@ -0,0 +1,8 @@
+package main
+
+func process(items []string) {
+	for _, item := range items {
+		go func() {
+			handle(item)
+		}()
+	}
+}`

const resourceDiff = `diff --git a/fileop.go b/fileop.go
new file mode 100644
--- /dev/null
+++ b/fileop.go
@@ -0,0 +1,8 @@
+package main
+
+import "os"
+
+func readFile(path string) ([]byte, error) {
+	f, err := os.Open(path)
+	if err != nil {
+		return nil, err
+	}
+}`

func TestAnalyzerSecurityRule(t *testing.T) {
	pd, err := diffparse.Parse(securityDiff)
	assert.NoError(t, err)

	a := NewAnalyzer()
	findings := a.Analyze(pd)

	hasSecurityRule := false
	for _, f := range findings {
		if f.Category == reviewmodel.CategorySecurity {
			hasSecurityRule = true
			assert.Equal(t, reviewmodel.SeverityCritical, f.Severity)
			assert.Equal(t, "handler.go", f.FilePath)
		}
	}
	assert.True(t, hasSecurityRule, "should detect security issue")
}

func TestAnalyzerGoroutineRule(t *testing.T) {
	pd, err := diffparse.Parse(goroutineDiff)
	assert.NoError(t, err)

	a := NewAnalyzer()
	findings := a.Analyze(pd)

	hasGoroutineRule := false
	for _, f := range findings {
		if f.Category == reviewmodel.CategoryGoroutine {
			hasGoroutineRule = true
			assert.Equal(t, reviewmodel.SeverityHigh, f.Severity)
		}
	}
	assert.True(t, hasGoroutineRule, "should detect goroutine issue")
}

func TestAnalyzerResourceRule(t *testing.T) {
	pd, err := diffparse.Parse(resourceDiff)
	assert.NoError(t, err)

	a := NewAnalyzer()
	findings := a.Analyze(pd)

	hasResourceRule := false
	for _, f := range findings {
		if f.Category == reviewmodel.CategoryResource {
			hasResourceRule = true
			assert.Contains(t, f.Title, "Resource")
		}
	}
	assert.True(t, hasResourceRule, "should detect resource issue")
}

func TestAnalyzerSecretDetection(t *testing.T) {
	diffWithSecret := `diff --git a/config.go b/config.go
new file mode 100644
--- /dev/null
+++ b/config.go
@@ -0,0 +1,3 @@
+package main
+
+var apiKey = "sk-1234567890abcdef1234567890abcdef"`

	pd, err := diffparse.Parse(diffWithSecret)
	assert.NoError(t, err)

	a := NewAnalyzer()
	findings := a.Analyze(pd)

	hasSecret := false
	for _, f := range findings {
		if f.Category == reviewmodel.CategorySensitive {
			hasSecret = true
			assert.Equal(t, reviewmodel.SeverityCritical, f.Severity)
		}
	}
	assert.True(t, hasSecret, "should detect secrets")
}

func TestAnalyzerNoFalsePositive(t *testing.T) {
	cleanDiff := `diff --git a/clean.go b/clean.go
new file mode 100644
--- /dev/null
+++ b/clean.go
@@ -0,0 +1,5 @@
+package main
+
+import "fmt"
+
+func greet(name string) string { return fmt.Sprintf("hello %s", name) }`

	pd, err := diffparse.Parse(cleanDiff)
	assert.NoError(t, err)

	a := NewAnalyzer()
	findings := a.Analyze(pd)

	// fmt.Sprintf in non-exec context should not trigger security rule
	for _, f := range findings {
		assert.NotEqual(t, reviewmodel.CategorySecurity, f.Category,
			"regular fmt.Sprintf should not trigger security rule")
	}
}

func TestDeduplicate(t *testing.T) {
	findings := []reviewmodel.Finding{
		{
			FilePath:   "file.go",
			Line:       10,
			Category:   reviewmodel.CategoryResource,
			Confidence: 0.6,
			RuleID:     "R1",
		},
		{
			FilePath:   "file.go",
			Line:       10,
			Category:   reviewmodel.CategoryResource,
			Confidence: 0.9,
			RuleID:     "R2",
		},
		{
			FilePath:   "file.go",
			Line:       20,
			Category:   reviewmodel.CategoryResource,
			Confidence: 0.5,
			RuleID:     "R1",
		},
	}

	result := Deduplicate(findings)
	assert.Len(t, result, 2, "should deduplicate same file+line+category")

	for _, r := range result {
		if r.Line == 10 {
			assert.Equal(t, 0.9, r.Confidence, "should keep highest confidence")
		}
	}
}

func TestNormalize(t *testing.T) {
	findings := []reviewmodel.Finding{
		{FilePath: "f.go", Line: 1, Category: "security", Confidence: 0.3, Severity: reviewmodel.SeverityHigh},
		{FilePath: "f.go", Line: 2, Category: "goroutine", Confidence: 0.55, Severity: reviewmodel.SeverityHigh},
		{FilePath: "f.go", Line: 3, Category: "resource", Confidence: 0.8, Severity: reviewmodel.SeverityHigh},
	}

	result := Normalize(findings)

	var low, mid, high bool
	for _, r := range result {
		switch {
		case r.Confidence == 0.3:
			low = true
			assert.Equal(t, reviewmodel.SeverityWarning, r.Severity)
			assert.True(t, r.NeedsHumanReview)
		case r.Confidence == 0.55:
			mid = true
			assert.True(t, r.NeedsHumanReview)
			assert.Equal(t, reviewmodel.SeverityHigh, r.Severity)
		case r.Confidence == 0.8:
			high = true
			assert.False(t, r.NeedsHumanReview)
		}
	}
	assert.True(t, low && mid && high)
}

func TestSeverityCounts(t *testing.T) {
	findings := []reviewmodel.Finding{
		{Severity: reviewmodel.SeverityCritical},
		{Severity: reviewmodel.SeverityCritical},
		{Severity: reviewmodel.SeverityHigh},
		{Severity: reviewmodel.SeverityMedium},
		{Severity: reviewmodel.SeverityMedium},
		{Severity: reviewmodel.SeverityMedium},
		{Severity: reviewmodel.SeverityLow},
	}
	counts := SeverityCounts(findings)
	assert.Equal(t, 2, counts[reviewmodel.SeverityCritical])
	assert.Equal(t, 1, counts[reviewmodel.SeverityHigh])
	assert.Equal(t, 3, counts[reviewmodel.SeverityMedium])
	assert.Equal(t, 1, counts[reviewmodel.SeverityLow])
}
