//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gateway

import "strings"

// SessionIDFunc builds a session ID for the inbound message.
type SessionIDFunc func(InboundMessage) (string, error)

type options struct {
	basePath     string
	messagesPath string
	statusPath   string
	cancelPath   string
	healthPath   string

	maxBodyBytes int64

	sessionIDFunc SessionIDFunc

	allowUsers      map[string]struct{}
	requireMention  bool
	mentionPatterns []string
}

// Option is a function that configures a gateway server.
type Option func(*options)

func newOptions(opts ...Option) options {
	o := options{
		basePath:       defaultBasePath,
		messagesPath:   defaultMessagesPath,
		statusPath:     defaultStatusPath,
		cancelPath:     defaultCancelPath,
		healthPath:     defaultHealthPath,
		maxBodyBytes:   defaultMaxBodyBytes,
		sessionIDFunc:  nil,
		allowUsers:     nil,
		requireMention: false,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if strings.TrimSpace(o.basePath) == "" {
		o.basePath = defaultBasePath
	}
	if strings.TrimSpace(o.messagesPath) == "" {
		o.messagesPath = defaultMessagesPath
	}
	if strings.TrimSpace(o.statusPath) == "" {
		o.statusPath = defaultStatusPath
	}
	if strings.TrimSpace(o.cancelPath) == "" {
		o.cancelPath = defaultCancelPath
	}
	if strings.TrimSpace(o.healthPath) == "" {
		o.healthPath = defaultHealthPath
	}
	if o.maxBodyBytes <= 0 {
		o.maxBodyBytes = defaultMaxBodyBytes
	}
	return o
}

// WithBasePath sets the base path for all gateway endpoints except health.
func WithBasePath(basePath string) Option {
	return func(o *options) {
		o.basePath = basePath
	}
}

// WithMessagesPath sets the relative path for the messages endpoint.
func WithMessagesPath(path string) Option {
	return func(o *options) {
		o.messagesPath = path
	}
}

// WithStatusPath sets the relative path for the status endpoint.
func WithStatusPath(path string) Option {
	return func(o *options) {
		o.statusPath = path
	}
}

// WithCancelPath sets the relative path for the cancel endpoint.
func WithCancelPath(path string) Option {
	return func(o *options) {
		o.cancelPath = path
	}
}

// WithHealthPath sets the health check endpoint path.
func WithHealthPath(path string) Option {
	return func(o *options) {
		o.healthPath = path
	}
}

// WithMaxBodyBytes sets the maximum bytes to read from an HTTP body.
func WithMaxBodyBytes(max int64) Option {
	return func(o *options) {
		o.maxBodyBytes = max
	}
}

// WithSessionIDFunc sets a custom session ID function.
func WithSessionIDFunc(fn SessionIDFunc) Option {
	return func(o *options) {
		o.sessionIDFunc = fn
	}
}

// WithAllowUsers sets a user allowlist.
//
// When set, only the listed user IDs are allowed to send messages.
// If called with no arguments, the allowlist becomes empty and all users are
// denied.
func WithAllowUsers(users ...string) Option {
	return func(o *options) {
		if o.allowUsers == nil {
			o.allowUsers = make(map[string]struct{})
		}
		for _, user := range users {
			user = strings.TrimSpace(user)
			if user == "" {
				continue
			}
			o.allowUsers[user] = struct{}{}
		}
	}
}

// WithRequireMentionInThreads enables mention gating for thread messages.
func WithRequireMentionInThreads(enabled bool) Option {
	return func(o *options) {
		o.requireMention = enabled
	}
}

// WithMentionPatterns sets patterns used for mention gating.
func WithMentionPatterns(patterns ...string) Option {
	return func(o *options) {
		if len(patterns) == 0 {
			o.mentionPatterns = nil
			return
		}
		copied := make([]string, 0, len(patterns))
		for _, pattern := range patterns {
			pattern = strings.TrimSpace(pattern)
			if pattern == "" {
				continue
			}
			copied = append(copied, pattern)
		}
		o.mentionPatterns = copied
	}
}
