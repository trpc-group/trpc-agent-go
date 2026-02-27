//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const offsetStoreVersion = 1

// OffsetStore persists the getUpdates offset.
type OffsetStore interface {
	Read(ctx context.Context) (int, bool, error)
	Write(ctx context.Context, offset int) error
}

// FileOffsetStore stores offsets in a local JSON file.
type FileOffsetStore struct {
	path string
}

// NewFileOffsetStore creates a file-based offset store.
func NewFileOffsetStore(path string) (*FileOffsetStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("telegram: empty offset store path")
	}
	return &FileOffsetStore{path: path}, nil
}

type offsetStoreState struct {
	Version int `json:"version"`
	Offset  int `json:"offset"`
}

// Read returns the stored offset.
func (s *FileOffsetStore) Read(
	ctx context.Context,
) (int, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf(
			"telegram: read offset store: %w",
			err,
		)
	}

	var state offsetStoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		return 0, false, fmt.Errorf(
			"telegram: decode offset store: %w",
			err,
		)
	}
	if state.Version != offsetStoreVersion {
		return 0, false, fmt.Errorf(
			"telegram: unexpected offset store version: %d",
			state.Version,
		)
	}
	if state.Offset < 0 {
		return 0, false, errors.New("telegram: negative offset")
	}
	return state.Offset, true, nil
}

// Write persists the next offset.
func (s *FileOffsetStore) Write(ctx context.Context, offset int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if offset < 0 {
		return errors.New("telegram: negative offset")
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf(
			"telegram: create offset store dir: %w",
			err,
		)
	}

	payload := offsetStoreState{
		Version: offsetStoreVersion,
		Offset:  offset,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf(
			"telegram: encode offset store: %w",
			err,
		)
	}
	data = append(data, '\n')

	tmp := fmt.Sprintf("%s.%s.tmp", s.path, randomHex(8))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf(
			"telegram: write offset store: %w",
			err,
		)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf(
			"telegram: rename offset store: %w",
			err,
		)
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "rand"
	}
	return hex.EncodeToString(b)
}
