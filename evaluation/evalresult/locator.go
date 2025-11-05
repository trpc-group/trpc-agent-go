//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalresult

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// defaultResultFileSuffix is the default suffix for eval set result files.
const defaultResultFileSuffix = ".evalset_result.json"

// Locator provides Build and List methods for locating eval set result files.
type Locator interface {
	// Build builds the path of an eval set result file for the given appName and evalSetResultID.
	Build(baseDir, appName, evalSetResultID string) string
	// List lists all eval set result IDs for the given appName.
	List(baseDir, appName string) ([]string, error)
}

// locator is the default Locator implementation.
type locator struct {
}

// Build builds the path of an eval set result file.
func (l *locator) Build(baseDir, appName, evalSetResultID string) string {
	return filepath.Join(baseDir, appName, evalSetResultID+defaultResultFileSuffix)
}

// List lists all eval set result IDs for the given appName.
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
		if strings.HasSuffix(entry.Name(), defaultResultFileSuffix) {
			name := strings.TrimSuffix(entry.Name(), defaultResultFileSuffix)
			results = append(results, name)
		}
	}
	return results, nil
}
