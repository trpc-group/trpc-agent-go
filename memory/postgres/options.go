//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// ServiceOpts is the options for the postgres memory service.
type ServiceOpts struct {
	connString      string
	instanceName    string
	tableName       string
	memoryLimit     int
	autoCreateTable bool

	// Tool related settings.
	toolCreators map[string]memory.ToolCreator
	enabledTools map[string]bool
	extraOptions []any
}

// ServiceOpt is the option for the postgres memory service.
type ServiceOpt func(*ServiceOpts)

// WithPostgresConnString creates a postgres client from connection string and sets it to the service.
func WithPostgresConnString(connString string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.connString = connString
	}
}

// WithPostgresInstance uses a postgres instance from storage.
// Note: WithPostgresConnString has higher priority than WithPostgresInstance.
// If both are specified, WithPostgresConnString will be used.
func WithPostgresInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithTableName sets the table name for storing memories.
// Default is "memories".
// Table name must start with a letter or underscore and contain only alphanumeric characters and underscores.
// Maximum length is 63 characters (PostgreSQL limit).
func WithTableName(tableName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.tableName = tableName
	}
}

// WithAutoCreateTable enables automatic table creation.
// If enabled, the service will create the table if it doesn't exist.
func WithAutoCreateTable(autoCreate bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.autoCreateTable = autoCreate
	}
}

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

// WithCustomTool sets a custom memory tool implementation.
// The tool will be enabled by default.
// If the tool name is invalid, this option will do nothing.
func WithCustomTool(toolName string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		opts.toolCreators[toolName] = creator
		opts.enabledTools[toolName] = true
	}
}

// WithToolEnabled sets which tool is enabled.
// If the tool name is invalid, this option will do nothing.
func WithToolEnabled(toolName string, enabled bool) ServiceOpt {
	return func(opts *ServiceOpts) {
		if !imemory.IsValidToolName(toolName) {
			return
		}
		opts.enabledTools[toolName] = enabled
	}
}

// WithExtraOptions sets the extra options for the postgres session service.
// this option mainly used for the customized postgres client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}
