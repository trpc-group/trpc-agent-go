//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package team

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

var (
	errNilTeam      = errors.New("team is nil")
	errNotSwarmTeam = errors.New("team is not a swarm team")
)

// UpdateSwarmMembers replaces the Swarm Team roster at runtime.
//
// This method only applies to Swarm teams (created with NewSwarm).
//
// It rewires every member's SubAgents list (via SetSubAgents) so members can
// transfer to the updated roster.
func (t *Team) UpdateSwarmMembers(members []agent.Agent) error {
	if t == nil {
		return errNilTeam
	}
	if t.mode != ModeSwarm {
		return errNotSwarmTeam
	}

	nextMembers := make([]agent.Agent, len(members))
	copy(nextMembers, members)

	memberByName, err := buildMemberIndex("", nextMembers)
	if err != nil {
		return err
	}
	if memberByName[t.entryName] == nil {
		return fmt.Errorf(
			"entry member %q not found",
			t.entryName,
		)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if err := wireSwarmRoster(nextMembers); err != nil {
		return err
	}

	t.members = nextMembers
	t.memberByName = memberByName
	return nil
}

// AddSwarmMember adds one member into a Swarm Team roster at runtime.
//
// This method only applies to Swarm teams (created with NewSwarm).
func (t *Team) AddSwarmMember(member agent.Agent) error {
	if t == nil {
		return errNilTeam
	}
	if t.mode != ModeSwarm {
		return errNotSwarmTeam
	}

	t.mu.RLock()
	cur := make([]agent.Agent, len(t.members))
	copy(cur, t.members)
	t.mu.RUnlock()

	cur = append(cur, member)
	return t.UpdateSwarmMembers(cur)
}

// RemoveSwarmMember removes a member by name from a Swarm Team roster.
//
// Returns true if the member was removed.
//
// This method only applies to Swarm teams (created with NewSwarm).
func (t *Team) RemoveSwarmMember(name string) bool {
	if t == nil || name == "" {
		return false
	}
	if t.mode != ModeSwarm {
		return false
	}

	t.mu.RLock()
	entryName := t.entryName
	cur := make([]agent.Agent, len(t.members))
	copy(cur, t.members)
	t.mu.RUnlock()

	if name == entryName {
		return false
	}

	next := make([]agent.Agent, 0, len(cur))
	removed := false
	for _, m := range cur {
		if m == nil {
			continue
		}
		if m.Info().Name == name {
			removed = true
			continue
		}
		next = append(next, m)
	}
	if !removed {
		return false
	}

	if err := t.UpdateSwarmMembers(next); err != nil {
		return false
	}
	return true
}
