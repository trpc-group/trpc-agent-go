//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package contextual

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const contextCacheVersion = 1

type contextCacheFile struct {
	Version  int               `json:"version"`
	Contexts map[string]string `json:"contexts"`
}

type contextCache struct {
	path string

	mu       sync.Mutex
	contexts map[string]string
}

func openContextCache(path string) (*contextCache, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("context cache path is required")
	}
	cache := &contextCache{
		path:     path,
		contexts: make(map[string]string),
	}
	if err := cache.load(); err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *contextCache) load() error {
	cacheFile, err := os.Open(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open context cache: %w", err)
	}
	defer func() {
		_ = cacheFile.Close()
	}()

	info, err := cacheFile.Stat()
	if err != nil {
		return fmt.Errorf("inspect context cache: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("context cache must be a regular file")
	}

	data, err := io.ReadAll(cacheFile)
	if err != nil {
		return fmt.Errorf("read context cache: %w", err)
	}
	var contents contextCacheFile
	if err := json.Unmarshal(data, &contents); err != nil {
		return fmt.Errorf("decode context cache: %w", err)
	}
	if contents.Version != contextCacheVersion {
		return fmt.Errorf("unsupported context cache version %d", contents.Version)
	}
	for key, contextText := range contents.Contexts {
		if strings.TrimSpace(key) == "" {
			return errors.New("context cache contains an empty key")
		}
		if strings.TrimSpace(contextText) == "" {
			return fmt.Errorf("context cache entry %q is empty", key)
		}
		c.contexts[key] = contextText
	}
	return nil
}

func (c *contextCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	contextText, ok := c.contexts[key]
	return contextText, ok
}

func (c *contextCache) put(key, contextText string) error {
	key = strings.TrimSpace(key)
	contextText = strings.TrimSpace(contextText)
	if key == "" {
		return errors.New("context cache key is required")
	}
	if contextText == "" {
		return errors.New("context cache value is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.contexts[key]; ok {
		if existing != contextText {
			return errors.New("context cache already contains a different context for this key")
		}
		return nil
	}

	updated := make(map[string]string, len(c.contexts)+1)
	for existingKey, existingContext := range c.contexts {
		updated[existingKey] = existingContext
	}
	updated[key] = contextText
	if err := c.write(updated); err != nil {
		return err
	}
	c.contexts = updated
	return nil
}

func (c *contextCache) write(contexts map[string]string) error {
	directory := filepath.Dir(c.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create context cache directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".context-cache-*")
	if err != nil {
		return fmt.Errorf("create temporary context cache: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary context cache: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(contextCacheFile{
		Version:  contextCacheVersion,
		Contexts: contexts,
	}); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode context cache: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync context cache: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close context cache: %w", err)
	}
	if err := os.Rename(temporaryPath, c.path); err != nil {
		return fmt.Errorf("replace context cache: %w", err)
	}
	return nil
}

func contextGenerationIdentity(modelName, modelEndpoint string) (string, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "", errors.New("context model name is required")
	}
	modelEndpoint = strings.TrimRight(strings.TrimSpace(modelEndpoint), "/")
	return hashParts(
		"context-generation/v1",
		contextPromptVersion,
		contextSystemPrompt,
		modelName,
		modelEndpoint,
		strconv.Itoa(contextMaxTokens),
		strconv.FormatFloat(contextTemperature, 'g', -1, 64),
		contextFinishReasonPolicy,
	), nil
}

func contextCacheKey(generationIdentity, parentText, chunkText, baseText string) string {
	return hashParts(
		generationIdentity,
		parentText,
		chunkText,
		baseText,
	)
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
