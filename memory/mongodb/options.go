//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	// defaultDatabase is the default MongoDB database name.
	defaultDatabase = "trpc-agent-go-mongo-memory"
	// defaultCollectionName is the default collection name for memories.
	defaultCollectionName = "memories"
	// defaultDBInitTimeout is the default timeout for database initialization.
	defaultDBInitTimeout = 30 * time.Second
)

var (
	defaultOptions = ServiceOpts{
		database:         defaultDatabase,
		collectionName:   defaultCollectionName,
		memoryLimit:      imemory.DefaultMemoryLimit,
		searchMinScore:   imemory.DefaultSearchMinScore,
		maxSearchResults: imemory.DefaultMaxSearchResults,
		toolCreators:     imemory.AllToolCreators,
		enabledTools:     imemory.DefaultEnabledTools,
		asyncMemoryNum:   imemory.DefaultAsyncMemoryNum,
	}
)

// ServiceOpts is the options for the mongodb memory service.
type ServiceOpts struct {
	uri            string
	instanceName   string
	database       string
	collectionName string
	memoryLimit    int
	softDelete     bool
	// keyword-search settings.
	searchMinScore   float64
	maxSearchResults int

	// Tool related settings.
	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	toolExposed       map[string]struct{}
	toolHidden        map[string]struct{}
	userExplicitlySet map[string]struct{}
	extraOptions      []any

	// skipDBInit skips database initialization (index creation).
	// Useful when user doesn't have createIndex permissions or when
	// indexes are managed externally.
	skipDBInit bool

	// Memory extractor for auto memory mode.
	extractor extractor.MemoryExtractor

	// Async memory worker configuration.
	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration
	// disableAutoMemoryOnExternalContext skips auto extraction for polluted sessions.
	disableAutoMemoryOnExternalContext bool
}

func (o ServiceOpts) clone() ServiceOpts {
	opts := o

	opts.toolCreators = make(map[string]memory.ToolCreator, len(o.toolCreators))
	for name, toolCreator := range o.toolCreators {
		opts.toolCreators[name] = toolCreator
	}

	opts.enabledTools = maps.Clone(o.enabledTools)
	opts.toolExposed = maps.Clone(o.toolExposed)
	opts.toolHidden = maps.Clone(o.toolHidden)

	// Initialize userExplicitlySet map (empty for new clone).
	opts.userExplicitlySet = make(map[string]struct{})

	return opts
}

// ServiceOpt is the option for the mongodb memory service.
type ServiceOpt func(*ServiceOpts)

// WithMongoClientURI sets the MongoDB connection URI directly (recommended).
// Example: "mongodb://user:pass@host1:27017,host2:27017/?replicaSet=rs0"
//
// Note: WithMongoClientURI has the highest priority.
// If both WithMongoClientURI and WithMongoInstance are specified, the URI is used.
func WithMongoClientURI(uri string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.uri = uri
	}
}

// WithMongoInstance uses a mongodb instance previously registered via
// storage/mongodb.RegisterMongoDBInstance.
// Direct WithMongoClientURI takes precedence over WithMongoInstance.
func WithMongoInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithDatabase sets the MongoDB database name.
// Default is "trpc-agent-go-mongo-memory".
func WithDatabase(database string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.database = database
	}
}

// WithCollectionName sets the collection name for storing memories.
// Default is "memories".
// Collection name must start with a letter or underscore and contain only
// alphanumeric characters and underscores. Maximum length is 64 characters.
// Panics if the collection name is invalid.
func WithCollectionName(collectionName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		if err := validateCollectionName(collectionName); err != nil {
			panic(fmt.Sprintf("invalid collection name: %v", err))
		}
		opts.collectionName = collectionName
	}
}

// WithSkipDBInit skips database initialization (index creation).
// Useful when:
// - User doesn't have createIndex permissions
// - Indexes are managed by migration tools
// - Running in production environment where indexes are pre-created
func WithSkipDBInit(skip bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.skipDBInit = skip
	}
}

// WithSoftDelete enables or disables soft delete behavior.
// When enabled, delete operations set deleted_at and queries filter deleted documents.
// Default is disabled (hard delete).
func WithSoftDelete(enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.softDelete = enabled
	}
}

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

// WithMinSearchScore sets the minimum keyword-search score. Scores below
// this value are filtered out. Default is 0.3.
func WithMinSearchScore(score float64) ServiceOpt {
	return func(opts *ServiceOpts) {
		if score < 0 {
			return
		}
		opts.searchMinScore = score
	}
}

// WithMaxResults sets the maximum number of keyword-search results.
// Default is 10. Use 0 to disable truncation.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if maxResults < 0 {
			return
		}
		opts.maxSearchResults = maxResults
	}
}

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
// If the tool name is invalid or creator is nil, this option will do nothing.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) || creator == nil {
			return
		}
		if opts.toolCreators == nil {
			opts.toolCreators = make(map[string]memory.ToolCreator)
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = struct{}{}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
// User settings via WithToolEnabled take precedence over auto mode
// defaults, regardless of option order.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if opts.enabledTools == nil {
			opts.enabledTools = make(map[string]struct{})
		}
		if opts.userExplicitlySet == nil {
			opts.userExplicitlySet = make(map[string]struct{})
		}
		if enabled {
			opts.enabledTools[toolName] = struct{}{}
		} else {
			delete(opts.enabledTools, toolName)
		}
		opts.userExplicitlySet[toolName] = struct{}{}
	}
}

// WithAutoMemoryExposedTools exposes enabled tools via Tools() in auto memory
// mode so the agent can call them directly. Invalid tool names are ignored.
func WithAutoMemoryExposedTools(toolNames ...string) ServiceOpt {
	return func(opts *ServiceOpts) {
		for _, toolName := range toolNames {
			WithToolExposed(toolName, true)(opts)
		}
	}
}

// WithToolExposed controls whether an enabled memory tool is exposed via
// Tools(). Use WithAutoMemoryExposedTools for the common auto memory case.
func WithToolExposed(toolName string, exposed bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		if exposed {
			if opts.toolExposed == nil {
				opts.toolExposed = make(map[string]struct{})
			}
			opts.toolExposed[toolName] = struct{}{}
			delete(opts.toolHidden, toolName)
			return
		}
		if opts.toolHidden == nil {
			opts.toolHidden = make(map[string]struct{})
		}
		opts.toolHidden[toolName] = struct{}{}
		delete(opts.toolExposed, toolName)
	}
}

// WithExtraOptions sets the extra options for the mongodb memory service.
// These options will be passed to the mongodb client builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithExtractor sets the memory extractor for auto memory mode.
// When enabled, auto mode defaults are applied to enabledTools,
// but user settings via WithToolEnabled (before or after) take precedence.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extractor = e
	}
}

// WithAsyncMemoryNum sets the number of async memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if num < 1 {
			num = imemory.DefaultAsyncMemoryNum
		}
		opts.asyncMemoryNum = num
	}
}

// WithMemoryQueueSize sets the queue size for memory jobs.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *ServiceOpts) {
		if size < 1 {
			size = imemory.DefaultMemoryQueueSize
		}
		opts.memoryQueueSize = size
	}
}

// WithMemoryJobTimeout sets the timeout for each memory job.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryJobTimeout = timeout
	}
}

// WithDisableAutoMemoryOnExternalContext stops future automatic memory
// extraction for sessions that consumed framework-owned external context.
func WithDisableAutoMemoryOnExternalContext(disable bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.disableAutoMemoryOnExternalContext = disable
	}
}

// collectionNamePattern is the regex pattern for validating collection names.
// Only allows alphanumeric characters and underscores, must start with a letter or underscore.
var collectionNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// validateCollectionName validates the collection name.
// Collection name must:
// - Start with a letter or underscore.
// - Contain only alphanumeric characters and underscores.
// - Not be empty.
// - Not exceed 64 characters.
func validateCollectionName(collectionName string) error {
	if collectionName == "" {
		return errors.New("collection name cannot be empty")
	}
	const maxCollectionNameLength = 64
	if len(collectionName) > maxCollectionNameLength {
		return fmt.Errorf("collection name too long: %d characters (max %d)",
			len(collectionName), maxCollectionNameLength)
	}
	if !collectionNamePattern.MatchString(collectionName) {
		return fmt.Errorf("invalid collection name: %s (must start with letter/underscore "+
			"and contain only alphanumeric characters and underscores)", collectionName)
	}
	return nil
}
