//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

// ServiceOpts is the options for the mysql memory service.
type ServiceOpts struct {
	dsn             string
	instanceName    string
	memoryLimit     int
	tableName       string
	autoCreateTable bool

	// Tool related settings.
	toolCreators map[string]memory.ToolCreator
	enabledTools map[string]bool
	extraOptions []any
}

// ServiceOpt is the option for the mysql memory service.
type ServiceOpt func(*ServiceOpts)

// WithMySQLClientDSN creates a mysql client from DSN and sets it to the service.
// DSN format: [username[:password]@][protocol[(address)]]/dbname[?param1=value1&...&paramN=valueN]
// Example: user:password@tcp(localhost:3306)/dbname?parseTime=true
func WithMySQLClientDSN(dsn string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.dsn = dsn
	}
}

// WithMySQLInstance uses a mysql instance from storage.
// Note: WithMySQLClientDSN has higher priority than WithMySQLInstance.
// If both are specified, WithMySQLClientDSN will be used.
func WithMySQLInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithMemoryLimit sets the limit of memories per user.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.memoryLimit = limit
	}
}

// WithTableName sets the table name for storing memories.
// Default is "memories".
// Table name must start with a letter or underscore and contain only alphanumeric characters and underscores.
// Maximum length is 64 characters.
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

// WithExtraOptions sets the extra options for the mysql session service.
// this option mainly used for the customized mysql client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}
