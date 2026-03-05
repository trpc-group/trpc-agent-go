//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const skillsLegacyMigrationStateKey = "processor:skills:legacy_migrated"

func maybeMigrateLegacySkillState(
	ctx context.Context,
	inv *agent.Invocation,
	ch chan<- *event.Event,
) {
	if inv == nil || inv.Session == nil {
		return
	}
	if _, ok := inv.GetState(skillsLegacyMigrationStateKey); ok {
		return
	}
	inv.SetState(skillsLegacyMigrationStateKey, true)

	hasLoaded := inv.Session.HasStateKeyWithPrefix(
		skill.StateKeyLoadedPrefix,
	)
	hasDocs := inv.Session.HasStateKeyWithPrefix(
		skill.StateKeyDocsPrefix,
	)
	if !hasLoaded && !hasDocs {
		return
	}

	state := inv.Session.SnapshotState()
	if len(state) == 0 {
		return
	}

	var owners map[string]string
	delta := make(map[string][]byte)

	for k, v := range state {
		if len(v) == 0 {
			continue
		}

		switch {
		case strings.HasPrefix(k, skill.StateKeyLoadedPrefix):
			if owners == nil {
				owners = legacySkillOwners(inv.Session.GetEvents())
			}
			name := strings.TrimPrefix(k, skill.StateKeyLoadedPrefix)
			migrateLegacyStateKey(
				inv,
				delta,
				k,
				v,
				name,
				owners,
				skill.LoadedKey,
			)
		case strings.HasPrefix(k, skill.StateKeyDocsPrefix):
			if owners == nil {
				owners = legacySkillOwners(inv.Session.GetEvents())
			}
			name := strings.TrimPrefix(k, skill.StateKeyDocsPrefix)
			migrateLegacyStateKey(
				inv,
				delta,
				k,
				v,
				name,
				owners,
				skill.DocsKey,
			)
		default:
			continue
		}
	}

	if len(delta) == 0 {
		return
	}
	agent.EmitEvent(ctx, inv, ch, event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithObject(model.ObjectTypeStateUpdate),
		event.WithStateDelta(delta),
	))
}

type scopedKeyBuilder func(agentName string, skillName string) string

func migrateLegacyStateKey(
	inv *agent.Invocation,
	delta map[string][]byte,
	legacyKey string,
	legacyVal []byte,
	skillName string,
	owners map[string]string,
	buildKey scopedKeyBuilder,
) {
	if inv == nil || inv.Session == nil {
		return
	}
	name := strings.TrimSpace(skillName)
	if name == "" {
		return
	}

	owner := strings.TrimSpace(owners[name])
	if owner == "" {
		owner = strings.TrimSpace(inv.AgentName)
	}
	if owner == "" {
		return
	}

	scopedKey := buildKey(owner, name)
	existing, ok := inv.Session.GetState(scopedKey)
	if ok && len(existing) > 0 {
		inv.Session.SetState(legacyKey, nil)
		delta[legacyKey] = nil
		return
	}

	inv.Session.SetState(scopedKey, legacyVal)
	delta[scopedKey] = legacyVal

	inv.Session.SetState(legacyKey, nil)
	delta[legacyKey] = nil
}

func legacySkillOwners(events []event.Event) map[string]string {
	owners := make(map[string]string)
	for i := len(events) - 1; i >= 0; i-- {
		owners = addOwnersFromEvent(events[i], owners)
	}
	return owners
}

func addOwnersFromEvent(
	ev event.Event,
	owners map[string]string,
) map[string]string {
	if ev.Response == nil {
		return owners
	}
	if ev.Object != model.ObjectTypeToolResponse {
		return owners
	}
	if len(ev.Choices) == 0 {
		return owners
	}
	author := strings.TrimSpace(ev.Author)
	if author == "" {
		return owners
	}

	for j := len(ev.Choices) - 1; j >= 0; j-- {
		msg := ev.Choices[j].Message
		if msg.Role != model.RoleTool {
			continue
		}
		if msg.ToolName != skillToolLoad &&
			msg.ToolName != skillToolSelectDocs {
			continue
		}
		name := strings.TrimSpace(skillNameFromToolResponse(msg))
		if name == "" {
			continue
		}
		if _, ok := owners[name]; ok {
			continue
		}
		owners[name] = author
	}
	return owners
}
