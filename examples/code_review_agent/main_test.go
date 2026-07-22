//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunClosesStorageWhenReportWriteFails(t *testing.T) {
	temp := t.TempDir()
	output := filepath.Join(temp, "out")
	require.NoError(t, os.MkdirAll(filepath.Join(output, "review_report.json"), 0o700))
	diff, err := filepath.Abs(filepath.Join("fixtures", "01_clean.diff"))
	require.NoError(t, err)
	database := filepath.Join(temp, "review.db")

	err = run([]string{
		"--diff-file", diff,
		"--db-path", database,
		"--output-dir", output,
		"--dry-run=true",
	})
	require.ErrorContains(t, err, "write json report")

	// On Windows this rename fails while SQLite still has the database open,
	// so it also verifies that the error return ran the deferred close.
	require.NoError(t, os.Rename(database, database+".closed"))
}
