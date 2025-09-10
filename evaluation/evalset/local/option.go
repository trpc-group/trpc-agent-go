//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local file storage implementation for evaluation sets.
package local

// Option configures Manager.
type Option func(*Manager)

// WithBaseDir sets the root directory for storing eval set JSON files.
// Default is "./evalsets" if not specified.
func WithBaseDir(dir string) Option {
	return func(m *Manager) {
		m.baseDir = dir
	}
}
