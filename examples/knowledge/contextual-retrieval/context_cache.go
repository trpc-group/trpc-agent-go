//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	contextCacheRecordVersion = "1"
	maxContextCacheLineBytes  = 8 << 20
)

type contextDescriptor struct {
	ProviderIdentity           string
	PromptVersion              string
	EmbeddingTextFormatVersion string
	ParentSHA256               string
	ChunkSHA256                string
}

func (d contextDescriptor) key() string {
	return hashParts(
		d.ProviderIdentity,
		d.PromptVersion,
		d.EmbeddingTextFormatVersion,
		d.ParentSHA256,
		d.ChunkSHA256,
	)
}

type contextCacheRecord struct {
	Version                    string `json:"version"`
	Key                        string `json:"key"`
	ProviderIdentity           string `json:"provider_identity"`
	PromptVersion              string `json:"prompt_version"`
	EmbeddingTextFormatVersion string `json:"embedding_text_format_version"`
	ParentSHA256               string `json:"parent_sha256"`
	ChunkSHA256                string `json:"chunk_sha256"`
	ContextSHA256              string `json:"context_sha256"`
	Context                    string `json:"context"`
}

func newContextCacheRecord(d contextDescriptor, contextText string) contextCacheRecord {
	return contextCacheRecord{
		Version:                    contextCacheRecordVersion,
		Key:                        d.key(),
		ProviderIdentity:           d.ProviderIdentity,
		PromptVersion:              d.PromptVersion,
		EmbeddingTextFormatVersion: d.EmbeddingTextFormatVersion,
		ParentSHA256:               d.ParentSHA256,
		ChunkSHA256:                d.ChunkSHA256,
		ContextSHA256:              hashText(contextText),
		Context:                    contextText,
	}
}

func (r contextCacheRecord) descriptor() contextDescriptor {
	return contextDescriptor{
		ProviderIdentity:           r.ProviderIdentity,
		PromptVersion:              r.PromptVersion,
		EmbeddingTextFormatVersion: r.EmbeddingTextFormatVersion,
		ParentSHA256:               r.ParentSHA256,
		ChunkSHA256:                r.ChunkSHA256,
	}
}

func (r contextCacheRecord) validate() error {
	if r.Version != contextCacheRecordVersion {
		return fmt.Errorf("unsupported cache record version %q", r.Version)
	}
	if strings.TrimSpace(r.Context) == "" {
		return errors.New("cache record has empty context")
	}
	if r.Key != r.descriptor().key() {
		return errors.New("cache record key does not match its input hashes")
	}
	if r.ContextSHA256 != hashText(r.Context) {
		return errors.New("cache record context hash mismatch")
	}
	return nil
}

type contextCache struct {
	path    string
	mu      sync.Mutex
	records map[string]contextCacheRecord
}

func openContextCache(path string) (*contextCache, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("context cache path is required")
	}

	cache := &contextCache{
		path:    path,
		records: make(map[string]contextCacheRecord),
	}
	if err := cache.load(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *contextCache) load() error {
	info, err := os.Lstat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect context cache: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("context cache must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return errors.New("context cache must be a regular file")
	}
	if err := os.Chmod(c.path, 0o600); err != nil {
		return fmt.Errorf("secure context cache permissions: %w", err)
	}

	f, err := os.Open(c.path)
	if err != nil {
		return fmt.Errorf("open context cache: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxContextCacheLineBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record contextCacheRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return fmt.Errorf("decode context cache line %d: %w", lineNumber, err)
		}
		if err := record.validate(); err != nil {
			return fmt.Errorf("validate context cache line %d: %w", lineNumber, err)
		}
		if existing, ok := c.records[record.Key]; ok && existing.ContextSHA256 != record.ContextSHA256 {
			return fmt.Errorf("context cache line %d conflicts with an earlier record", lineNumber)
		}
		c.records[record.Key] = record
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read context cache: %w", err)
	}
	return nil
}

func (c *contextCache) get(d contextDescriptor) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	record, ok := c.records[d.key()]
	if !ok {
		return "", false
	}
	return record.Context, true
}

func (c *contextCache) put(d contextDescriptor, contextText string) error {
	contextText = strings.TrimSpace(contextText)
	record := newContextCacheRecord(d, contextText)
	if err := record.validate(); err != nil {
		return fmt.Errorf("refuse invalid context cache record: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.records[record.Key]; ok {
		if existing.ContextSHA256 != record.ContextSHA256 {
			return errors.New("context cache already contains a different context for this key")
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("create context cache directory: %w", err)
	}
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open context cache for append: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("secure context cache permissions: %w", err)
	}

	encoded, err := json.Marshal(record)
	if err == nil {
		encoded = append(encoded, '\n')
		_, err = f.Write(encoded)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("append context cache: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close context cache: %w", closeErr)
	}

	c.records[record.Key] = record
	return nil
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
