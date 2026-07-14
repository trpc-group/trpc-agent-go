//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// GoldenTrace caches operation snapshots for regression detection.
type GoldenTrace struct {
	CaseName  string     `json:"case_name"`
	CreatedAt time.Time  `json:"created_at"`
	Snapshots []Snapshot `json:"snapshots"`
}

// GoldenTracePath returns the file path for a golden trace.
func GoldenTracePath(dir, caseName string) string {
	return filepath.Join(dir, caseName+".golden.json")
}

// SaveGoldenTrace writes a golden trace atomically via .tmp + rename pattern.
func SaveGoldenTrace(dir string, trace *GoldenTrace) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return err
	}
	path := GoldenTracePath(dir, trace.CaseName)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// LoadGoldenTrace loads a golden trace from disk. Returns (nil, false, nil) if not found.
// Returns a non-nil error if the file exists but contains corrupted JSON.
func LoadGoldenTrace(dir, caseName string) (*GoldenTrace, bool, error) {
	path := GoldenTracePath(dir, caseName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read golden trace: %w", err)
	}
	var gt GoldenTrace
	if err := json.Unmarshal(b, &gt); err != nil {
		return nil, false, fmt.Errorf("parse golden trace %s: %w", caseName, err)
	}
	return &gt, true, nil
}
