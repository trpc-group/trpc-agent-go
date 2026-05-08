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

import (
	"context"
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

// SessionIDFunc builds a session ID for the inbound message.
type SessionIDFunc func(InboundMessage) (string, error)

// RunOptionInput describes one gateway run before invoking the runner.
type RunOptionInput struct {
	Inbound    InboundMessage
	UserID     string
	SessionID  string
	RequestID  string
	Message    model.Message
	Trace      *debugrecorder.Trace
	Extensions map[string]json.RawMessage
}

// RunOptionResolver decorates context and options for one gateway run.
type RunOptionResolver func(
	ctx context.Context,
	input RunOptionInput,
) (context.Context, []agent.RunOption)

type options struct {
	basePath      string
	messagesPath  string
	streamPath    string
	streamPathSet bool
	statusPath    string
	cancelPath    string
	healthPath    string

	maxBodyBytes int64
	maxPartBytes int64

	partFetcher          partFetcher
	allowPrivatePartURLs bool
	allowedPartPatterns  []string
	audioTranscriber     audioTranscriber
	appName              string

	sessionIDFunc SessionIDFunc

	allowUsers      map[string]struct{}
	requireMention  bool
	mentionPatterns []string

	runOptionResolver RunOptionResolver

	recorder *debugrecorder.Recorder
	uploads  *uploads.Store

	personaStore    *persona.Store
	memoryFileStore *memoryfile.Store
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
		maxPartBytes:   defaultMaxContentPartBytes,
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
	if o.streamPathSet {
		if strings.TrimSpace(o.streamPath) == "" {
			o.streamPath = defaultMessagesStreamPath
		}
	} else {
		o.streamPath = o.messagesPath + gwproto.MessagesStreamSuffix
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
	if o.maxPartBytes <= 0 {
		o.maxPartBytes = defaultMaxContentPartBytes
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

// WithMessagesStreamPath sets the relative path for the streaming
// messages endpoint.
func WithMessagesStreamPath(path string) Option {
	return func(o *options) {
		o.streamPath = path
		o.streamPathSet = true
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

// WithMaxContentPartBytes sets the maximum bytes to fetch for one
// content part.
func WithMaxContentPartBytes(max int64) Option {
	return func(o *options) {
		o.maxPartBytes = max
	}
}

// WithContentPartFetcher sets a custom content-part fetcher.
func WithContentPartFetcher(fetcher partFetcher) Option {
	return func(o *options) {
		o.partFetcher = fetcher
	}
}

// WithAllowPrivateContentPartURLs allows content parts to fetch URLs that
// resolve to loopback or private network addresses.
func WithAllowPrivateContentPartURLs(enabled bool) Option {
	return func(o *options) {
		o.allowPrivatePartURLs = enabled
	}
}

// WithAllowedContentPartDomains restricts content-part URL fetches to the
// provided domains or URL patterns.
//
// Each entry is either:
//   - "example.com" (allows all paths), or
//   - "example.com/path" (allows /path/...).
//
// The host match is case-insensitive and allows subdomains.
func WithAllowedContentPartDomains(domains ...string) Option {
	return func(o *options) {
		if len(domains) == 0 {
			o.allowedPartPatterns = nil
			return
		}
		out := make([]string, 0, len(domains))
		for _, domain := range domains {
			domain = strings.TrimSpace(domain)
			if domain == "" {
				continue
			}
			out = append(out, domain)
		}
		o.allowedPartPatterns = out
	}
}

// WithPersonaStore sets the preset persona store used for per-chat
// system-message injection.
func WithPersonaStore(store *persona.Store) Option {
	return func(o *options) {
		o.personaStore = store
	}
}

// WithMemoryFileStore sets the file-based memory store used for per-run
// context injection.
func WithMemoryFileStore(store *memoryfile.Store) Option {
	return func(o *options) {
		o.memoryFileStore = store
	}
}

// WithAppName sets the app name used by file-based memory injection.
func WithAppName(appName string) Option {
	return func(o *options) {
		o.appName = strings.TrimSpace(appName)
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

// WithDebugRecorder enables file-based debug recording for gateway requests.
//
// When set, the gateway creates a per-request trace when no trace is present
// in the request context.
func WithDebugRecorder(rec *debugrecorder.Recorder) Option {
	return func(o *options) {
		o.recorder = rec
	}
}

// WithRunOptionResolver decorates one gateway run before runner.Run.
func WithRunOptionResolver(resolver RunOptionResolver) Option {
	return func(o *options) {
		if resolver == nil {
			o.runOptionResolver = nil
			return
		}
		prev := o.runOptionResolver
		if prev == nil {
			o.runOptionResolver = resolver
			return
		}
		o.runOptionResolver = func(
			ctx context.Context,
			input RunOptionInput,
		) (context.Context, []agent.RunOption) {
			prevCtx, prevOpts := prev(ctx, input)
			if prevCtx == nil {
				prevCtx = ctx
			}
			nextCtx, nextOpts := resolver(prevCtx, input)
			if nextCtx == nil {
				nextCtx = prevCtx
			}
			return nextCtx, append(prevOpts, nextOpts...)
		}
	}
}

// WithUploadStore persists inbound file parts to stable host paths.
func WithUploadStore(store *uploads.Store) Option {
	return func(o *options) {
		o.uploads = store
	}
}

// WithAudioTranscriber overrides inbound audio transcription.
func WithAudioTranscriber(transcriber audioTranscriber) Option {
	return func(o *options) {
		o.audioTranscriber = transcriber
	}
}
