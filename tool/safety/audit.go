//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// JSONLSink appends one JSON object per line. Writes and Close are safe for
// concurrent use, and the file is forced to owner-only mode (0600).
type JSONLSink struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

// NewJSONLSink opens path for append without creating parent directories.
func NewJSONLSink(path string) (*JSONLSink, error) {
	if path == "" {
		return nil, errors.New("safety: audit path cannot be empty")
	}
	file, err := openSecureAuditFile(path)
	if err != nil {
		return nil, fmt.Errorf("safety: open audit file: %w", err)
	}
	return &JSONLSink{file: file}, nil
}

// WriteAudit implements AuditSink.
func (s *JSONLSink) WriteAudit(ctx context.Context, event AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("safety: encode audit event: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return errors.New("safety: audit sink is closed")
	}
	if _, err := s.file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("safety: append audit event: %w", err)
	}
	return nil
}

// Close flushes and closes the audit file.
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file == nil {
		return nil
	}
	if err := s.file.Sync(); err != nil {
		_ = s.file.Close()
		return fmt.Errorf("safety: sync audit file: %w", err)
	}
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("safety: close audit file: %w", err)
	}
	return nil
}
