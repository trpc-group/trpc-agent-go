//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	storeVersion = 1

	storeDirPerm  = 0o700
	storeFilePerm = 0o600

	storeTempSuffix = ".tmp"
)

const (
	errInterruptedByRestart = "interrupted by previous runtime restart"
)

// Store persists task runs.
type Store interface {
	Load(ctx context.Context) ([]Run, error)
	Save(ctx context.Context, runs []Run) error
}

// MemoryStore stores runs in memory. It is useful for tests and stateless
// applications.
type MemoryStore struct {
	mu   sync.Mutex
	runs []Run
}

// NewMemoryStore creates an in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Load implements Store.
func (s *MemoryStore) Load(ctx context.Context) ([]Run, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRuns(s.runs), nil
}

// Save implements Store.
func (s *MemoryStore) Save(ctx context.Context, runs []Run) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = cloneRuns(runs)
	return nil
}

// FileStore persists runs into one JSON file.
type FileStore struct {
	path string
}

// NewFileStore creates a JSON file store.
func NewFileStore(path string) (*FileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("taskrun: empty store path")
	}
	return &FileStore{path: filepath.Clean(path)}, nil
}

// Load implements Store.
func (s *FileStore) Load(ctx context.Context) ([]Run, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Version != 0 && file.Version != storeVersion {
		return nil, fmt.Errorf(
			"taskrun: unsupported store version: %d",
			file.Version,
		)
	}
	return cloneRuns(file.Runs), nil
}

// Save implements Store.
func (s *FileStore) Save(ctx context.Context, runs []Run) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), storeDirPerm); err != nil {
		return err
	}

	items := cloneRuns(runs)
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

	tmpPath := s.path + storeTempSuffix
	if err := os.WriteFile(tmpPath, data, storeFilePerm); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}

type storeFile struct {
	Version int   `json:"version"`
	Runs    []Run `json:"runs,omitempty"`
}

func normalizeLoadedRuns(
	runs map[string]*Run,
	now time.Time,
	finalizer Finalizer,
) bool {
	changed := false
	for _, run := range runs {
		if run == nil || run.Status.IsTerminal() {
			continue
		}
		view := failedRunView(*run, errInterruptedByRestart, now)
		if finalizer != nil {
			view.Metadata = mergeMetadata(
				view.Metadata,
				runFinalizer(context.Background(), finalizer, view),
			)
		}
		*run = cloneRun(view)
		changed = true
	}
	return changed
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
