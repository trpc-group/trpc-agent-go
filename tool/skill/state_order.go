//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	skillstate "trpc.group/trpc-go/trpc-agent-go/skill"
)

func appendLoadedOrderStateDelta(
	inv *agent.Invocation,
	agentName string,
	delta map[string][]byte,
	skillName string,
) map[string][]byte {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return delta
	}

	if delta == nil {
		delta = make(map[string][]byte, 1)
	}

	orderKey := skillstate.LoadedOrderKey(agentName)
	var current []string
	if inv != nil && inv.Session != nil {
		if raw, ok := inv.Session.GetState(orderKey); ok {
			current = skillstate.ParseLoadedOrder(raw)
		}
	}

	next := skillstate.TouchLoadedOrder(current, skillName)
	if b := skillstate.MarshalLoadedOrder(next); len(b) > 0 {
		delta[orderKey] = b
	}
	return delta
}
