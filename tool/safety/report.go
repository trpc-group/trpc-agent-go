//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// reportVersion is the schema version included in every report.
const reportVersion = "1.0.0"

// NewReport creates a Report from a ScanResult.
// The report includes the current time as GeneratedAt and the schema version.
func NewReport(result ScanResult) Report {
	now := time.Now()
	return Report{
		Version:     reportVersion,
		GeneratedAt: &now,
		Decision:    result.Decision,
		RiskLevel:   result.RiskLevel,
		Findings:    result.Findings,
		ToolName:    result.ToolName,
		Command:     result.Command,
		Backend:     result.Backend,
		Intercepted: result.Intercepted,
	}
}

// WriteReportJSON writes a Report as JSON to w.
func WriteReportJSON(w io.Writer, r Report) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	_, err = w.Write(data)
	if err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// WriteReportFile writes a Report as JSON to a file using an atomic write.
// It writes to a .tmp file first, then renames to prevent corruption on crash.
func WriteReportFile(path string, r Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp report file: %w", err)
	}
	tmpName := tmpFile.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return fmt.Errorf("write tmp report file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp report file: %w", err)
	}

	if err := replaceReportFile(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename tmp report file: %w", err)
	}

	return nil
}

func replaceReportFile(tmpName, path string) error {
	if err := os.Rename(tmpName, path); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}

	var lastErr error
	for i := 0; i < 5; i++ {
		_ = os.Remove(path)
		if err := os.Rename(tmpName, path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return lastErr
}
