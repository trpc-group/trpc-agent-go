//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

func TestAgentRun_WithRealStore(t *testing.T) {
	store, err := storage.NewSQLiteStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	reg := runner.NewRuleRegistry()
	require.NoError(t, reg.Register(&rules.SecurityRule{
		RuleBase: runner.RuleBase{IDValue: "GO_SECURITY_INJECTION", CategoryValue: finding.CategorySecurity, DefaultSev: finding.SeverityCritical},
	}))

	agent := runner.NewCRAgent(reg, nil, store)

	diffContent := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,9 @@
 package main
+
+import "database/sql"
+
+func query(db *sql.DB, requestID string) {
+	db.Query("SELECT * FROM users WHERE id = " + request.UserID)
+}
`
	result, err := agent.Run(context.Background(), runner.ReviewInput{
		TaskID:      "test-agent-run",
		DiffSource:  "test",
		DiffContent: diffContent,
		DryRun:      true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.TaskID)

	task, err := store.GetTask(context.Background(), "test-agent-run")
	require.NoError(t, err)
	assert.Equal(t, "completed", task.Status)
}
