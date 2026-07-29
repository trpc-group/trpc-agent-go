//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteReport writes an indented JSON report atomically. It creates missing
// parent directories with mode 0700 and replaces the destination with a mode
// 0600 file only after the complete report has been encoded.
func WriteReport(path string, report *DiffReport) error {
	if report == nil {
		return fmt.Errorf("report is nil")
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal replay report: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create replay report directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".replay-report-*.tmp")
	if err != nil {
		return fmt.Errorf("create replay report temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()

	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write replay report: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set replay report permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close replay report: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace replay report: %w", err)
	}
	return nil
}
