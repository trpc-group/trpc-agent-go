//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local file storage implementation for evaluation results.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

// manager implements the evalresult.Manager interface using local file storage.
type manager struct {
	baseDir string
	mu      sync.Mutex
}

// NewManager creates a new local file evaluation result manager.
// Use functional options (see option.go) to override the default directory.
func NewManager(opt ...evalresult.Option) evalresult.Manager {
	opts := evalresult.NewOptions(opt...)
	m := &manager{baseDir: opts.BaseDir}
	return m
}

// Save stores an evaluation result to local file.
func (m *manager) Save(ctx context.Context, result *evalresult.EvalSetResult) error {
	_ = ctx
	if result == nil {
		return errors.New("result is nil")
	}
	if result.EvalSetResultID == "" {
		return errors.New("result id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return err
	}
	path := m.resultPath(result.EvalSetResultID)
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Get retrieves an evaluation result by evalSetResultID from local file.
func (m *manager) Get(ctx context.Context, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load(evalSetResultID)
}

// List returns all available evaluation results from local files.
func (m *manager) List(ctx context.Context) ([]*evalresult.EvalSetResult, error) {
	_ = ctx
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*evalresult.EvalSetResult{}, nil
		}
		return nil, err
	}
	var results []*evalresult.EvalSetResult
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".evalset_result.json") {
			continue
		}
		id := strings.TrimSuffix(name, ".evalset_result.json")
		res, err := m.load(id)
		if err != nil {
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (m *manager) resultPath(evalSetResultID string) string {
	filename := fmt.Sprintf("%s.evalset_result.json", evalSetResultID)
	return filepath.Join(m.baseDir, filename)
}

func (m *manager) load(evalSetResultID string) (*evalresult.EvalSetResult, error) {
	path := m.resultPath(evalSetResultID)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var res evalresult.EvalSetResult
	if err := json.NewDecoder(f).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}
