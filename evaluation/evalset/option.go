//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evalset provides configuration options for evaluation set managers.
package evalset

import "path/filepath"

const DefaultEvalSetExtension = ".evalset.json"

// PathFunc builds the absolute path where an eval set should be stored.
type PathFunc func(baseDir, appName, evalSetID string) string

// Options configure the local evaluation set manager.
type Options struct {
	BaseDir  string
	PathFunc PathFunc
}

// NewOptions constructs Options with sensible defaults mirroring the Python implementation.
func NewOptions(opts ...Option) *Options {
	options := &Options{
		BaseDir:  "evalsets",
		PathFunc: defaultPathFunc,
	}
	for _, o := range opts {
		o(options)
	}
	return options
}

// Option configures Options.
type Option func(*Options)

// WithBaseDir sets the root directory for storing eval set JSON files.
func WithBaseDir(dir string) Option {
	return func(o *Options) {
		o.BaseDir = dir
	}
}

// WithEvalSetPathFunc overrides how eval set file paths are generated.
func WithEvalSetPathFunc(fn PathFunc) Option {
	return func(o *Options) {
		o.PathFunc = fn
	}
}

func defaultPathFunc(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, evalSetID+DefaultEvalSetExtension)
}
