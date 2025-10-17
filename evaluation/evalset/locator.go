//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalset

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// defaultEvalSetFileSuffix is the default suffix for eval set files.
const defaultEvalSetFileSuffix = ".evalset.json"

// Locator provides Build and List methods for locating eval set files.
type Locator interface {
	// Build builds the path of an eval set file for the given appName and evalSetID.
	Build(baseDir, appName, evalSetID string) string
	// List lists all eval set IDs for the given appName.
	List(baseDir, appName string) ([]string, error)
}

// locator is the default Locator implementation.
type locator struct {
}

// Build builds the path of an eval set file.
func (l *locator) Build(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, evalSetID+defaultEvalSetFileSuffix)
}

// List lists all eval set IDs for the given appName.
func (l *locator) List(baseDir, appName string) ([]string, error) {
	dir := filepath.Join(baseDir, appName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	var results []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), defaultEvalSetFileSuffix) {
			name := strings.TrimSuffix(entry.Name(), defaultEvalSetFileSuffix)
			results = append(results, name)
		}
	}
	return results, nil
}
