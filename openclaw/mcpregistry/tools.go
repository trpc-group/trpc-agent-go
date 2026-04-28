//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcpregistry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

const (
	ToolAdd    = "mcp_registry_add"
	ToolUpdate = "mcp_registry_update"
	ToolRemove = "mcp_registry_remove"
	ToolList   = "mcp_registry_list"
	ToolTest   = "mcp_registry_test"
)

type registryTools struct {
	store   *FileStore
	appName string
}

type upsertInput struct {
	Name             string            `json:"name"`
	Scope            string            `json:"scope,omitempty"`
	Description      string            `json:"description,omitempty"`
	Transport        string            `json:"transport,omitempty"`
	ServerURL        string            `json:"server_url,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	TimeoutMS        int64             `json:"timeout_ms,omitempty"`
	ClearServerURL   bool              `json:"clear_server_url,omitempty"`
	ClearHeaders     bool              `json:"clear_headers,omitempty"`
	ClearCommand     bool              `json:"clear_command,omitempty"`
	ClearArgs        bool              `json:"clear_args,omitempty"`
	ClearTimeout     bool              `json:"clear_timeout,omitempty"`
	ClearDescription bool              `json:"clear_description,omitempty"`
	AllowUpdate      bool              `json:"allow_update,omitempty"`
}

type removeInput struct {
	Name  string `json:"name"`
	Scope string `json:"scope,omitempty"`
}

type listInput struct{}

type listOutput struct {
	Servers []EntryView `json:"servers"`
}

type testInput struct {
	Name  string `json:"name"`
	Scope string `json:"scope,omitempty"`
}

type testOutput struct {
	Accessible        bool   `json:"accessible"`
	Selector          string `json:"selector,omitempty"`
	QualifiedSelector string `json:"qualified_selector,omitempty"`
	Message           string `json:"message"`
}

type removeOutput struct {
	Removed bool   `json:"removed"`
	Name    string `json:"name"`
	Scope   string `json:"scope,omitempty"`
}

// NewTools creates registry management tools backed by store.
func NewTools(store *FileStore, appName string) []tool.Tool {
	tools := registryTools{
		store:   store,
		appName: strings.TrimSpace(appName),
	}
	return []tool.Tool{
		function.NewFunctionTool(
			tools.add,
			function.WithName(ToolAdd),
			function.WithDescription(addToolDescription),
		),
		function.NewFunctionTool(
			tools.update,
			function.WithName(ToolUpdate),
			function.WithDescription(updateToolDescription),
		),
		function.NewFunctionTool(
			tools.remove,
			function.WithName(ToolRemove),
			function.WithDescription(removeToolDescription),
		),
		function.NewFunctionTool(
			tools.list,
			function.WithName(ToolList),
			function.WithDescription(listToolDescription),
		),
		function.NewFunctionTool(
			tools.test,
			function.WithName(ToolTest),
			function.WithDescription(testToolDescription),
		),
	}
}

func (t registryTools) add(
	ctx context.Context,
	input upsertInput,
) (EntryView, error) {
	return t.upsert(ctx, input, false)
}

func (t registryTools) update(
	ctx context.Context,
	input upsertInput,
) (EntryView, error) {
	input.AllowUpdate = true
	return t.upsert(ctx, input, true)
}

func (t registryTools) upsert(
	ctx context.Context,
	input upsertInput,
	updateOnly bool,
) (EntryView, error) {
	runtime, err := RuntimeContextFromContext(ctx, t.appName)
	if err != nil {
		return EntryView{}, err
	}
	conn := mcp.ConnectionConfig{
		Transport:   input.Transport,
		ServerURL:   input.ServerURL,
		Headers:     input.Headers,
		Command:     input.Command,
		Args:        input.Args,
		Timeout:     time.Duration(input.TimeoutMS) * time.Millisecond,
		Description: input.Description,
	}
	return t.store.Upsert(ctx, UpsertRequest{
		Context:          runtime,
		Name:             input.Name,
		Scope:            Scope(input.Scope),
		Description:      input.Description,
		Connection:       conn,
		ClearServerURL:   input.ClearServerURL,
		ClearHeaders:     input.ClearHeaders,
		ClearCommand:     input.ClearCommand,
		ClearArgs:        input.ClearArgs,
		ClearTimeout:     input.ClearTimeout,
		ClearDescription: input.ClearDescription,
		UpdateOnly:       updateOnly,
		AllowUpdate:      input.AllowUpdate,
	})
}

func (t registryTools) remove(
	ctx context.Context,
	input removeInput,
) (removeOutput, error) {
	runtime, err := RuntimeContextFromContext(ctx, t.appName)
	if err != nil {
		return removeOutput{}, err
	}
	removed, scope, err := t.store.Delete(ctx, DeleteRequest{
		Context: runtime,
		Name:    input.Name,
		Scope:   Scope(input.Scope),
	})
	if err != nil {
		return removeOutput{}, err
	}
	return removeOutput{
		Removed: removed,
		Name:    strings.TrimSpace(input.Name),
		Scope:   string(scope),
	}, nil
}

func (t registryTools) list(
	ctx context.Context,
	_ listInput,
) (listOutput, error) {
	runtime, err := RuntimeContextFromContext(ctx, t.appName)
	if err != nil {
		return listOutput{}, err
	}
	servers, err := t.store.List(ctx, runtime)
	if err != nil {
		return listOutput{}, err
	}
	return listOutput{Servers: servers}, nil
}

func (t registryTools) test(
	ctx context.Context,
	input testInput,
) (testOutput, error) {
	name, err := normalizeName(input.Name)
	if err != nil {
		return testOutput{}, err
	}
	runtime, err := RuntimeContextFromContext(ctx, t.appName)
	if err != nil {
		return testOutput{}, err
	}
	servers, err := t.store.List(ctx, runtime)
	if err != nil {
		return testOutput{}, err
	}
	scope := Scope(strings.ToLower(strings.TrimSpace(input.Scope)))
	for _, server := range servers {
		if server.Name != name {
			continue
		}
		if scope != "" && server.Scope != scope {
			continue
		}
		return testOutput{
			Accessible:        true,
			Selector:          server.Selector,
			QualifiedSelector: server.QualifiedSelector,
			Message: "Registry entry is visible. Use mcp_list_tools " +
				"with selector to test the live MCP connection.",
		}, nil
	}
	return testOutput{
		Accessible: false,
		Message: fmt.Sprintf(
			"MCP registry entry %q is not visible in this turn",
			name,
		),
	}, nil
}

const addToolDescription = "" +
	"Persist an MCP server for future turns. Use this when the user asks " +
	"to configure, remember, add, or persist an MCP connection. Default " +
	"scope is session, so /new or another chat will not inherit it. Use " +
	"scope=chat only when the user wants the current chat or group to " +
	"share it. Use scope=user for the current actor across chats. Use " +
	"scope=global only for an explicit instance-wide admin-style request. " +
	"Never echo secrets after calling this tool."

const updateToolDescription = "" +
	"Update an existing MCP registry entry in the selected scope. Use this " +
	"when the user changes the URL, token, headers, command, or " +
	"description of an already configured MCP server. For transport changes, " +
	"use clear_server_url/clear_headers/clear_command/clear_args to remove " +
	"fields from the previous transport. Never echo secrets."

const removeToolDescription = "" +
	"Remove an MCP registry entry from the selected scope. Default scope is " +
	"session. Use this when the user asks to forget, remove, clear, or " +
	"delete a configured MCP server."

const listToolDescription = "" +
	"List MCP registry entries visible to the current turn. Use this " +
	"before saying no MCP server is configured. Returned URLs and headers " +
	"are safe views with sensitive values redacted."

const testToolDescription = "" +
	"Check whether a named MCP registry entry is visible in the current " +
	"turn and return the selector to use with mcp_list_tools. This tool " +
	"does not expose secrets."
