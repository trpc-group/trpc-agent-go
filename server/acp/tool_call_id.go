//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

type activeToolCall struct {
	acpToolCallID     acpsdk.ToolCallId
	toolName          string
	permissionClaimed bool
}

type toolCallIDMapper struct {
	mu       sync.Mutex
	allocate func() acpsdk.ToolCallId
	active   map[string]*activeToolCall
	changed  chan struct{}
}

func newToolCallIDMapper(
	allocate func() acpsdk.ToolCallId,
) *toolCallIDMapper {
	return &toolCallIDMapper{
		allocate: allocate,
		active:   make(map[string]*activeToolCall),
		changed:  make(chan struct{}),
	}
}

func (m *toolCallIDMapper) resolve(
	ctx context.Context,
	providerID string,
	toolName string,
) acpsdk.ToolCallId {
	if providerID == "" {
		return ""
	}
	m.mu.Lock()
	if call := m.active[providerID]; call != nil {
		acpToolCallID := call.acpToolCallID
		activeToolName := call.toolName
		filledName := false
		conflict := false
		switch {
		case activeToolName == "" && toolName != "":
			call.toolName = toolName
			filledName = true
			m.notifyLocked()
		case activeToolName != "" && toolName != "" && activeToolName != toolName:
			conflict = true
		}
		m.mu.Unlock()
		if filledName {
			log.DebugContext(
				ctx,
				"Filled late ACP tool call name",
				"provider_tool_call_id",
				providerID,
				"acp_tool_call_id",
				acpToolCallID,
				"tool_name",
				toolName,
			)
		}
		if conflict {
			log.WarnContext(
				ctx,
				"ACP tool call ID conflict: provider tool call ID reused while active",
				"provider_tool_call_id",
				providerID,
				"acp_tool_call_id",
				acpToolCallID,
				"active_tool_name",
				activeToolName,
				"incoming_tool_name",
				toolName,
			)
		}
		return acpToolCallID
	}
	call := &activeToolCall{
		acpToolCallID: m.allocate(),
		toolName:      toolName,
	}
	m.active[providerID] = call
	acpToolCallID := call.acpToolCallID
	m.notifyLocked()
	m.mu.Unlock()
	log.DebugContext(
		ctx,
		"Allocated ACP tool call ID",
		"provider_tool_call_id",
		providerID,
		"acp_tool_call_id",
		acpToolCallID,
		"tool_name",
		toolName,
	)
	return acpToolCallID
}

func (m *toolCallIDMapper) current(
	providerID string,
) (acpsdk.ToolCallId, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	call := m.active[providerID]
	if call == nil {
		return "", false
	}
	return call.acpToolCallID, true
}

// claimPermission waits until event conversion has observed the active call.
// Waiting avoids reusing a previous call when the runner starts the next model
// round before the ACP event loop has consumed the previous tool result.
func (m *toolCallIDMapper) claimPermission(
	ctx context.Context,
	providerID string,
	toolName string,
) (acpsdk.ToolCallId, error) {
	waitLogged := false
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		m.mu.Lock()
		call := m.active[providerID]
		if call != nil &&
			call.toolName == toolName &&
			!call.permissionClaimed {
			call.permissionClaimed = true
			acpToolCallID := call.acpToolCallID
			m.mu.Unlock()
			log.DebugContext(
				ctx,
				"Claimed ACP tool call ID for permission check",
				"provider_tool_call_id",
				providerID,
				"acp_tool_call_id",
				acpToolCallID,
				"tool_name",
				toolName,
			)
			return acpToolCallID, nil
		}
		changed := m.changed
		m.mu.Unlock()
		if !waitLogged {
			log.DebugContext(
				ctx,
				"Waiting for ACP tool call ID before permission check",
				"provider_tool_call_id",
				providerID,
				"tool_name",
				toolName,
			)
			waitLogged = true
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-changed:
		}
	}
}

func (m *toolCallIDMapper) complete(
	ctx context.Context,
	providerID string,
) (acpsdk.ToolCallId, bool) {
	m.mu.Lock()
	call := m.active[providerID]
	if call == nil {
		m.mu.Unlock()
		log.WarnContext(
			ctx,
			"ACP tool result has no active tool call mapping",
			"provider_tool_call_id",
			providerID,
		)
		return "", false
	}
	acpToolCallID := call.acpToolCallID
	toolName := call.toolName
	delete(m.active, providerID)
	m.notifyLocked()
	m.mu.Unlock()
	log.DebugContext(
		ctx,
		"Completed ACP tool call ID",
		"provider_tool_call_id",
		providerID,
		"acp_tool_call_id",
		acpToolCallID,
		"tool_name",
		toolName,
	)
	return acpToolCallID, true
}

func (m *toolCallIDMapper) notifyLocked() {
	close(m.changed)
	m.changed = make(chan struct{})
}
