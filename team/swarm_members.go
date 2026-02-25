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

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.updateSwarmMembersLocked(nextMembers)
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

	t.mu.Lock()
	defer t.mu.Unlock()

	next := make([]agent.Agent, len(t.members)+1)
	copy(next, t.members)
	next[len(next)-1] = member
	return t.updateSwarmMembersLocked(next)
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

	t.mu.Lock()
	defer t.mu.Unlock()

	if name == t.entryName {
		return false
	}

	next := make([]agent.Agent, 0, len(t.members))
	removed := false
	for _, m := range t.members {
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

	if err := t.updateSwarmMembersLocked(next); err != nil {
		return false
	}
	return true
}

func (t *Team) updateSwarmMembersLocked(members []agent.Agent) error {
	memberByName, err := buildMemberIndex("", members)
	if err != nil {
		return err
	}
	if memberByName[t.entryName] == nil {
		return fmt.Errorf(
			"entry member %q not found",
			t.entryName,
		)
	}

	if err := wireSwarmRoster(members); err != nil {
		return err
	}

	t.members = members
	t.memberByName = memberByName
	return nil
}
