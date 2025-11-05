//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local file storage evaluation result manager implementation.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/clone"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/epochtime"
)

const (
	defaultTempFileSuffix = ".tmp"
	defaultDirPermission  = 0o755
	defaultFilePermission = 0o644
)

// manager implements evalresult.Manager backed by the local filesystem.
type manager struct {
	mu      sync.RWMutex
	baseDir string
	locator evalresult.Locator
}

// New creates a new local file evaluation result manager.
func New(opt ...evalresult.Option) evalresult.Manager {
	opts := evalresult.NewOptions(opt...)
	m := &manager{
		baseDir: opts.BaseDir,
		locator: opts.Locator,
	}
	return m
}

// Save stores an evaluation result.
// Returns an error if the eval set result is nil or the eval set id is empty.
func (m *manager) Save(_ context.Context, appName string, evalSetResult *evalresult.EvalSetResult) (string, error) {
	if appName == "" {
		return "", errors.New("app name is empty")
	}
	if evalSetResult == nil {
		return "", errors.New("eval set result is nil")
	}
	if evalSetResult.EvalSetID == "" {
		return "", errors.New("the eval set id of eval set result is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSetResultID := evalSetResult.EvalSetResultID
	if evalSetResultID == "" {
		evalSetResultID = fmt.Sprintf("%s_%s_%s", appName, evalSetResult.EvalSetID, uuid.New().String())
	}
	cloned, err := clone.Clone(evalSetResult)
	if err != nil {
		return "", fmt.Errorf("clone eval set result: %w", err)
	}
	cloned.EvalSetResultID = evalSetResultID
	if cloned.EvalSetResultName == "" {
		cloned.EvalSetResultName = evalSetResultID
	}
	if cloned.CreationTimestamp == nil {
		cloned.CreationTimestamp = &epochtime.EpochTime{Time: time.Now()}
	}
	if err := m.store(appName, cloned); err != nil {
		return "", fmt.Errorf("store eval set result %s.%s: %w", appName, evalSetResultID, err)
	}
	return evalSetResultID, nil
}

// Get retrieves an evaluation result by evalSetResultID.
func (m *manager) Get(_ context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetResultID == "" {
		return nil, errors.New("eval set result id is empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSetResult, err := m.load(appName, evalSetResultID)
	if err != nil {
		return nil, fmt.Errorf("load eval set result %s.%s: %w", appName, evalSetResultID, err)
	}
	return evalSetResult, nil
}

// List returns all available evaluation results.
func (m *manager) List(_ context.Context, appName string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSetResultIDs, err := m.locator.List(m.baseDir, appName)
	if err != nil {
		return nil, fmt.Errorf("list eval set results for app %s: %w", appName, err)
	}
	return evalSetResultIDs, nil
}

// evalSetResultPath builds the path to the EvalSetResult file.
func (m *manager) evalSetResultPath(appName, evalSetResultID string) string {
	return m.locator.Build(m.baseDir, appName, evalSetResultID)
}

// load loads the EvalSetResult from the file system.
func (m *manager) load(appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	path := m.evalSetResultPath(appName, evalSetResultID)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file %s: %w", path, err)
	}
	defer f.Close()
	var payload string
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode file %s: %w", path, err)
	}
	var res evalresult.EvalSetResult
	if err := json.Unmarshal([]byte(payload), &res); err != nil {
		return nil, fmt.Errorf("unmarshal eval set result %s: %w", path, err)
	}
	return &res, nil
}

// store stores the EvalSetResult to the file system.
func (m *manager) store(appName string, evalSetResult *evalresult.EvalSetResult) error {
	if evalSetResult == nil {
		return errors.New("eval set result is nil")
	}
	path := m.evalSetResultPath(appName, evalSetResult.EvalSetResultID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirPermission); err != nil {
		return fmt.Errorf("mkdir all %s: %w", dir, err)
	}
	tmp := path + defaultTempFileSuffix
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultFilePermission)
	if err != nil {
		return fmt.Errorf("open file %s: %w", tmp, err)
	}
	data, err := json.Marshal(evalSetResult)
	if err != nil {
		file.Close()
		return fmt.Errorf("json marshal: %w", err)
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(string(data)); err != nil {
		file.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode file %s: %w", tmp, err)
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename file %s to %s: %w", tmp, path, err)
	}
	return nil
}
