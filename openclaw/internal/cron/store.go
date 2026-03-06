//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cron

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

const (
	storeVersion  = 1
	storeFilePerm = 0o600
	storeDirPerm  = 0o700

	storeTempPattern = defaultJobsFile + ".tmp-*"
)

type storeData struct {
	Version int    `json:"version"`
	Jobs    []*Job `json:"jobs"`
}

func loadJobs(path string) ([]*Job, error) {
	if path == "" {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var data storeData
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	if data.Version != 0 && data.Version != storeVersion {
		return nil, errors.New("cron: unsupported store version")
	}
	return cloneJobs(data.Jobs), nil
}

func saveJobs(path string, jobs []*Job) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), storeDirPerm); err != nil {
		return err
	}

	cloned := cloneJobs(jobs)
	sort.Slice(cloned, func(i, j int) bool {
		return cloned[i].ID < cloned[j].ID
	})

	payload := storeData{
		Version: storeVersion,
		Jobs:    cloned,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(filepath.Dir(path), storeTempPattern)
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() {
		_ = os.Remove(tmp)
	}()

	if err := file.Chmod(storeFilePerm); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if mkErr := os.MkdirAll(
			filepath.Dir(path),
			storeDirPerm,
		); mkErr != nil {
			return mkErr
		}
		return os.Rename(tmp, path)
	}
	return nil
}

func cloneJobs(jobs []*Job) []*Job {
	if len(jobs) == 0 {
		return nil
	}
	out := make([]*Job, 0, len(jobs))
	for _, job := range jobs {
		if job == nil {
			continue
		}
		out = append(out, job.clone())
	}
	return out
}
