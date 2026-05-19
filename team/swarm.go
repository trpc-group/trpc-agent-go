//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// SwarmConfig defines optional safety limits for swarm-style handoffs.
//
// All fields are optional. A zero value means "no limit" for that field.
type SwarmConfig struct {
	// MaxHandoffs limits how many transfers can happen in a single run.
	MaxHandoffs int

	// NodeTimeout limits how long a single member agent may run after a
	// transfer. A zero value means no per-node timeout.
	NodeTimeout time.Duration

	// RepetitiveHandoffWindow is the sliding window size used to detect
	// repetitive handoff loops. A zero value disables this check.
	RepetitiveHandoffWindow int

	// RepetitiveHandoffMinUnique is the minimum number of unique agents that
	// must appear in the window. If fewer appear, the transfer is rejected.
	// A zero value disables this check.
	RepetitiveHandoffMinUnique int
}

// SwarmHandoffInputArgs describes one Swarm handoff input rewrite.
type SwarmHandoffInputArgs struct {
	FromAgentName   string
	ToAgentName     string
	RootInput       model.Message
	ParentInput     model.Message
	TransferMessage string
}

// SwarmHandoffInputBuilder builds the target agent input message for a
// Swarm handoff.
type SwarmHandoffInputBuilder func(
	ctx context.Context,
	args SwarmHandoffInputArgs,
) (model.Message, error)

type swarmSessionIDArgs struct {
	ParentSessionID string
	TeamName        string
	EntryAgentName  string
	ToAgentName     string
}

type swarmSessionScope int

const (
	swarmSessionScopeDefault swarmSessionScope = iota
	swarmSessionScopeShared
	swarmSessionScopePerAgent
)

type swarmTurnRouting int

const (
	swarmTurnRoutingDefault swarmTurnRouting = iota
	swarmTurnRoutingEntry
	swarmTurnRoutingTargetTakesOver
)

type swarmHandoffPolicy struct {
	sessionScope swarmSessionScope
	turnRouting  swarmTurnRouting
}

// DefaultSwarmConfig returns conservative defaults that prevent unbounded
// transfer loops while keeping behavior predictable.
func DefaultSwarmConfig() SwarmConfig {
	return SwarmConfig{
		MaxHandoffs:                20,
		RepetitiveHandoffWindow:    8,
		RepetitiveHandoffMinUnique: 3,
	}
}

func defaultSwarmSessionID(args swarmSessionIDArgs) string {
	if strings.TrimSpace(args.ToAgentName) != "" &&
		strings.TrimSpace(args.ToAgentName) == strings.TrimSpace(args.EntryAgentName) {
		return strings.TrimSpace(args.ParentSessionID)
	}
	parts := []string{
		encodeSwarmSessionIDPart(args.ParentSessionID),
		encodeSwarmSessionIDPart(args.TeamName),
		encodeSwarmSessionIDPart(args.ToAgentName),
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "/")
}

func encodeSwarmSessionIDPart(part string) string {
	return url.PathEscape(strings.TrimSpace(part))
}

func (p swarmHandoffPolicy) normalizedSessionScope() swarmSessionScope {
	if p.sessionScope == swarmSessionScopeDefault {
		return swarmSessionScopeShared
	}
	return p.sessionScope
}

func (p swarmHandoffPolicy) normalizedTurnRouting() swarmTurnRouting {
	if p.turnRouting == swarmTurnRoutingDefault {
		return swarmTurnRoutingEntry
	}
	return p.turnRouting
}

func (p swarmHandoffPolicy) usesIsolatedSession() bool {
	return p.normalizedSessionScope() != swarmSessionScopeShared
}

func (p swarmHandoffPolicy) targetTakesOver() bool {
	return p.normalizedTurnRouting() == swarmTurnRoutingTargetTakesOver
}

func (p swarmHandoffPolicy) needsRootState() bool {
	return p.usesIsolatedSession() || p.targetTakesOver()
}
