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

const (
	defaultBaseDir = "evalsets"
	// DefaultEvalSetExtension is the default extension for eval set files.
	DefaultEvalSetExtension = ".evalset.json"
)

// PathBuilder builds the absolute path where an eval set should be stored.
type PathBuilder func(baseDir, appName, evalSetID string) string

// Options configure the local evaluation set manager.
type Options struct {
	BaseDir     string
	PathBuilder PathBuilder
}

// NewOptions constructs Options with the default values.
func NewOptions(opts ...Option) *Options {
	options := &Options{
		BaseDir:     defaultBaseDir,
		PathBuilder: defaultPathBuilder,
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
func WithEvalSetPathFunc(p PathBuilder) Option {
	return func(o *Options) {
		o.PathBuilder = p
	}
}

func defaultPathBuilder(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, evalSetID+DefaultEvalSetExtension)
}
