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
	"net/url"
	"strings"
)

const stateKeyScopeDelimiter = "/"

func escapeScopeSegment(value string) string {
	if strings.Contains(value, stateKeyScopeDelimiter) {
		return url.PathEscape(value)
	}
	return value
}

// LoadedKey returns the session state key used to mark a skill as loaded for
// a specific agent.
//
// When agentName is empty, it falls back to the legacy unscoped key.
func LoadedKey(agentName string, skillName string) string {
	agentName = strings.TrimSpace(agentName)
	skillName = strings.TrimSpace(skillName)
	if agentName == "" {
		return StateKeyLoadedPrefix + skillName
	}
	agentName = escapeScopeSegment(agentName)
	return StateKeyLoadedByAgentPrefix + agentName +
		stateKeyScopeDelimiter + skillName
}

// DocsKey returns the session state key used to store doc selection for a
// specific agent.
//
// When agentName is empty, it falls back to the legacy unscoped key.
func DocsKey(agentName string, skillName string) string {
	agentName = strings.TrimSpace(agentName)
	skillName = strings.TrimSpace(skillName)
	if agentName == "" {
		return StateKeyDocsPrefix + skillName
	}
	agentName = escapeScopeSegment(agentName)
	return StateKeyDocsByAgentPrefix + agentName +
		stateKeyScopeDelimiter + skillName
}

// LoadedPrefix returns the prefix used to scan loaded-skill keys for the
// provided agentName.
//
// When agentName is empty, it returns the legacy prefix.
func LoadedPrefix(agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return StateKeyLoadedPrefix
	}
	agentName = escapeScopeSegment(agentName)
	return StateKeyLoadedByAgentPrefix + agentName + stateKeyScopeDelimiter
}

// DocsPrefix returns the prefix used to scan doc-selection keys for the
// provided agentName.
//
// When agentName is empty, it returns the legacy prefix.
func DocsPrefix(agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return StateKeyDocsPrefix
	}
	agentName = escapeScopeSegment(agentName)
	return StateKeyDocsByAgentPrefix + agentName + stateKeyScopeDelimiter
}
