//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package window provides shared event-window semantics for session backends.
package window

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// EventWindowFromOrderedEvents builds an event window from events that are
// already ordered in persisted chronological order.
func EventWindowFromOrderedEvents(
	key session.Key,
	events []event.Event,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	entries := make([]session.EventWindowEntry, 0, len(events))
	for _, evt := range events {
		entries = append(entries, session.EventWindowEntry{
			Event:     evt,
			CreatedAt: evt.Timestamp,
		})
	}
	return EventWindowFromOrderedEntries(key, entries, req)
}

// EventWindowFromOrderedEntries builds an event window from entries that are
// already ordered in persisted chronological order.
func EventWindowFromOrderedEntries(
	key session.Key,
	entries []session.EventWindowEntry,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if err := req.Key.CheckSessionKey(); err != nil {
		return nil, err
	}
	if req.Key != key {
		key = req.Key
	}
	anchorEventID := strings.TrimSpace(req.AnchorEventID)
	if anchorEventID == "" {
		return nil, fmt.Errorf("anchor event id is required")
	}
	if req.Before < 0 || req.After < 0 {
		return nil, fmt.Errorf("event window requires before >= 0 and after >= 0")
	}

	roleFilter := MakeRoleFilter(req.Roles)
	anchorIndex := -1
	for idx := range entries {
		if entries[idx].Event.ID != anchorEventID {
			continue
		}
		if !EventAllowed(&entries[idx].Event, roleFilter) {
			continue
		}
		anchorIndex = idx
		break
	}
	if anchorIndex < 0 {
		return nil, fmt.Errorf("%w: %s", session.ErrEventWindowAnchorNotFound, anchorEventID)
	}

	before := collectBefore(entries, anchorIndex, req.Before, roleFilter)
	after := collectAfter(entries, anchorIndex, req.After, roleFilter)
	out := make([]session.EventWindowEntry, 0, len(before)+1+len(after))
	out = append(out, before...)
	out = append(out, entries[anchorIndex])
	out = append(out, after...)

	return &session.EventWindow{
		SessionKey:    key,
		AnchorEventID: anchorEventID,
		Entries:       out,
	}, nil
}

// MakeRoleFilter normalizes optional role filters.
func MakeRoleFilter(roles []model.Role) map[model.Role]struct{} {
	if len(roles) == 0 {
		return nil
	}
	filter := make(map[model.Role]struct{}, len(roles))
	for _, role := range roles {
		role = model.Role(strings.TrimSpace(string(role)))
		if role == "" {
			continue
		}
		filter[role] = struct{}{}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

// EventAllowed reports whether an event is meaningful history for a window.
func EventAllowed(
	evt *event.Event,
	roleFilter map[model.Role]struct{},
) bool {
	_, role, ok := ExtractEventText(evt)
	if !ok {
		return false
	}
	if len(roleFilter) == 0 {
		return true
	}
	_, ok = roleFilter[role]
	return ok
}

// ExtractEventText returns meaningful message text and the effective role used
// by search and exact window loading.
func ExtractEventText(
	evt *event.Event,
) (string, model.Role, bool) {
	if evt == nil || evt.Response == nil || evt.Response.IsPartial ||
		len(evt.Response.Choices) == 0 {
		return "", "", false
	}

	msg := evt.Response.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		return "", "", false
	}

	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	if msg.ToolID != "" || role == model.RoleTool {
		role = model.RoleTool
	}
	if role != model.RoleUser && role != model.RoleAssistant && role != model.RoleTool {
		return "", "", false
	}

	text := strings.TrimSpace(msg.Content)
	if text == "" && len(msg.ContentParts) > 0 {
		var parts []string
		for _, part := range msg.ContentParts {
			if part.Text == nil {
				continue
			}
			partText := strings.TrimSpace(*part.Text)
			if partText == "" {
				continue
			}
			parts = append(parts, partText)
		}
		text = strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if text == "" {
		return "", "", false
	}
	if role == model.RoleTool {
		toolName := strings.TrimSpace(msg.ToolName)
		if toolName != "" {
			text = toolName + ": " + text
		}
	}
	return text, role, true
}

func collectBefore(
	entries []session.EventWindowEntry,
	anchorIndex int,
	limit int,
	roleFilter map[model.Role]struct{},
) []session.EventWindowEntry {
	if limit <= 0 {
		return nil
	}
	out := make([]session.EventWindowEntry, 0, limit)
	for idx := anchorIndex - 1; idx >= 0 && len(out) < limit; idx-- {
		if !EventAllowed(&entries[idx].Event, roleFilter) {
			continue
		}
		out = append(out, entries[idx])
	}
	reverse(out)
	return out
}

func collectAfter(
	entries []session.EventWindowEntry,
	anchorIndex int,
	limit int,
	roleFilter map[model.Role]struct{},
) []session.EventWindowEntry {
	if limit <= 0 {
		return nil
	}
	out := make([]session.EventWindowEntry, 0, limit)
	for idx := anchorIndex + 1; idx < len(entries) && len(out) < limit; idx++ {
		if !EventAllowed(&entries[idx].Event, roleFilter) {
			continue
		}
		out = append(out, entries[idx])
	}
	return out
}

func reverse(entries []session.EventWindowEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}
