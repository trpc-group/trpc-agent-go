//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	publicsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
)

const (
	storeVersion = 1

	storeDirPerm  = 0o700
	storeFilePerm = 0o600
)

func loadRuns(path string) (map[string]*runRecord, error) {
	path = filepath.Clean(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*runRecord{}, nil
		}
		return nil, err
	}

	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version != 0 && file.Version != storeVersion {
		return nil, fmt.Errorf(
			"subagent: unsupported store version: %d",
			file.Version,
		)
	}

	runs := make(map[string]*runRecord, len(file.Runs))
	for _, item := range file.Runs {
		record := item
		if record.ID == "" {
			continue
		}
		runs[record.ID] = record.clone()
	}
	return runs, nil
}

func saveRuns(path string, runs map[string]*runRecord) error {
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), storeDirPerm); err != nil {
		return err
	}

	items := make([]runRecord, 0, len(runs))
	for _, item := range runs {
		if item == nil || item.ID == "" {
			continue
		}
		items = append(items, *item.clone())
	}
	sort.Slice(items, func(i int, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})

	file := storeFile{
		Version: storeVersion,
		Runs:    items,
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, storeFilePerm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func normalizeLoadedRuns(
	runs map[string]*runRecord,
	now time.Time,
) bool {
	changed := false
	for _, run := range runs {
		if run == nil || run.Status.IsTerminal() {
			continue
		}
		run.Status = publicsubagent.StatusFailed
		run.Error = "interrupted by previous runtime restart"
		run.UpdatedAt = now
		run.FinishedAt = cloneTime(now)
		run.Summary = summarizeResult(run.Error)
		changed = true
	}
	return changed
}

func cloneTime(value time.Time) *time.Time {
	copied := value
	return &copied
}
