//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package metric provides options for the metric manager.
package metric

import "path/filepath"

const (
	// DefaultBaseDir is the default base directory for metric files.
	DefaultBaseDir = "metrics"
	// DefaultMetricsExtension is the default extension for metric files.
	DefaultMetricsExtension = ".metrics.json"
)

// PathBuilder builds the absolute path where metric configurations should be stored.
type PathBuilder func(baseDir, appName, evalSetID string) string

// Options configure the local metric manager.
type Options struct {
	// BaseDir is the base directory for metric files.
	BaseDir string
	// PathBuilder is the function to build the absolute path where metric configurations should be stored.
	PathBuilder PathBuilder
}

// NewOptions constructs Options with defaults mirroring the eval set manager layout.
func NewOptions(opts ...Option) *Options {
	options := &Options{
		BaseDir:     DefaultBaseDir,
		PathBuilder: defaultPathBuilder,
	}
	for _, o := range opts {
		o(options)
	}
	return options
}

// Option configures Options.
type Option func(*Options)

// WithBaseDir sets the root directory for storing metric JSON files.
func WithBaseDir(dir string) Option {
	return func(o *Options) {
		o.BaseDir = dir
	}
}

// WithPathBuilder overrides how metric file paths are generated.
func WithPathFunc(p PathBuilder) Option {
	return func(o *Options) {
		o.PathBuilder = p
	}
}

func defaultPathBuilder(baseDir, appName, evalSetID string) string {
	return filepath.Join(baseDir, appName, evalSetID+DefaultMetricsExtension)
}
