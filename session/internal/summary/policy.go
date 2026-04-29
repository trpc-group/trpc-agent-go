//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SummaryDispatchPolicy controls which branch summaries are allowed to run and
// whether branch-triggered updates should also refresh the full-session summary.
type SummaryDispatchPolicy struct {
	FilterAllowlist    map[string]struct{}
	CascadeFullSession bool
}

// NewSummaryDispatchPolicy normalizes summary dispatch settings.
func NewSummaryDispatchPolicy(
	filterAllowlist []string,
	cascadeFullSession bool,
) SummaryDispatchPolicy {
	return SummaryDispatchPolicy{
		FilterAllowlist:    normalizeSummaryFilterAllowlist(filterAllowlist),
		CascadeFullSession: cascadeFullSession,
	}
}

// SummaryTargets returns the summary keys that should be refreshed for the
// given trigger filterKey.
func (p SummaryDispatchPolicy) SummaryTargets(filterKey string) []string {
	if filterKey == session.SummaryFilterKeyAllContents {
		return []string{session.SummaryFilterKeyAllContents}
	}
	targets := make([]string, 0, 2)
	if p.allowsBranch(filterKey) {
		targets = append(targets, filterKey)
	}
	if p.CascadeFullSession {
		targets = append(targets, session.SummaryFilterKeyAllContents)
	}
	if len(targets) == 0 {
		return nil
	}
	return targets
}

// AllowsFilterKey reports whether the given filterKey may be summarized when a
// caller explicitly requests that key.
func (p SummaryDispatchPolicy) AllowsFilterKey(filterKey string) bool {
	if filterKey == session.SummaryFilterKeyAllContents {
		return true
	}
	return p.allowsBranch(filterKey)
}

func (p SummaryDispatchPolicy) allowsBranch(filterKey string) bool {
	if filterKey == "" {
		return true
	}
	if p.FilterAllowlist == nil {
		return true
	}
	for allowKey := range p.FilterAllowlist {
		if matchSummaryFilterKey(allowKey, filterKey) {
			return true
		}
	}
	return false
}

func normalizeSummaryFilterAllowlist(
	filterAllowlist []string,
) map[string]struct{} {
	if filterAllowlist == nil {
		return nil
	}
	normalized := make(map[string]struct{}, len(filterAllowlist))
	for _, raw := range filterAllowlist {
		filterKey := strings.TrimSpace(raw)
		if filterKey == "" {
			continue
		}
		normalized[filterKey] = struct{}{}
	}
	return normalized
}

func matchSummaryFilterKey(allowedKey, filterKey string) bool {
	if allowedKey == "" || filterKey == "" {
		return true
	}

	allowedKey += event.FilterKeyDelimiter
	filterKey += event.FilterKeyDelimiter
	return strings.HasPrefix(allowedKey, filterKey) || strings.HasPrefix(filterKey, allowedKey)
}
