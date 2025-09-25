//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package metric

import "path/filepath"

const DefaultMetricsExtension = ".metrics.json"

// PathFunc builds the absolute path where metric configurations should be stored.
type PathFunc func(baseDir, appName, evalSetID string) string

// Options configure the local metric manager.
type Options struct {
    BaseDir  string
    PathFunc PathFunc
}

// NewOptions constructs Options with defaults mirroring the eval set manager layout.
func NewOptions(opts ...Option) *Options {
    options := &Options{
        BaseDir:  "metrics",
        PathFunc: defaultPathFunc,
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

// WithPathFunc overrides how metric file paths are generated.
func WithPathFunc(fn PathFunc) Option {
    return func(o *Options) {
        o.PathFunc = fn
    }
}

func defaultPathFunc(baseDir, appName, evalSetID string) string {
    return filepath.Join(baseDir, appName, evalSetID+DefaultMetricsExtension)
}
