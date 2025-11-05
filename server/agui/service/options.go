//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

const (
	// defaultPath is the default path for the AG-UI service.
	defaultPath = "/"
	// defaultMessagesSnapshotPath is the default path for the messages snapshot handler.
	defaultMessagesSnapshotPath = "/history"
)

// Options holds the options for an AG-UI transport implementation.
type Options struct {
	AppName                 string // AppName is the name of the application.
	Path                    string // Path is the request URL path served by the handler.
	MessagesSnapshotEnabled bool   // MessagesSnapshotEnabled enables the messages snapshot handler.
	MessagesSnapshotPath    string // MessagesSnapshotPath is the HTTP path for the messages snapshot handler.
}

// NewOptions creates a new options instance.
func NewOptions(opt ...Option) *Options {
	opts := &Options{}
	for _, o := range opt {
		o(opts)
	}
	if opts.Path == "" {
		opts.Path = defaultPath
	}
	if opts.MessagesSnapshotEnabled && opts.MessagesSnapshotPath == "" {
		opts.MessagesSnapshotPath = defaultMessagesSnapshotPath
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*Options)

// WithPath sets the request path.
func WithPath(p string) Option {
	return func(s *Options) {
		s.Path = p
	}
}

// WithMessagesSnapshot enables the messages snapshot handler and configures its dependencies.
func WithMessagesSnapshotEnabled(e bool) Option {
	return func(s *Options) {
		s.MessagesSnapshotEnabled = e
	}
}

// WithMessagesSnapshotPath sets the HTTP path for the snapshot handler.
func WithMessagesSnapshotPath(p string) Option {
	return func(s *Options) {
		s.MessagesSnapshotPath = p
	}
}
