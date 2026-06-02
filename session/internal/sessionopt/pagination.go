//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sessionopt provides internal pagination helpers for session options.
package sessionopt

import (
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// SortByUpdatedDesc sorts sessions by UpdatedAt descending, using ID descending
// as a tiebreaker for deterministic pagination results.
func SortByUpdatedDesc(sessions []*session.Session) {
	slices.SortFunc(sessions, func(a, b *session.Session) int {
		if cmp := b.UpdatedAt.Compare(a.UpdatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(b.ID, a.ID)
	})
}

// ApplyListPage applies offset/limit pagination based on Options.
// If ListSessionPage is nil, sessions are returned as-is.
func ApplyListPage(sessions []*session.Session, opt *session.Options) []*session.Session {
	if opt == nil || opt.ListSessionPage == nil {
		return sessions
	}
	page := opt.ListSessionPage
	n := len(sessions)
	if page.Offset >= n {
		return []*session.Session{}
	}
	remaining := n - page.Offset
	limit := page.Limit
	if limit > remaining {
		limit = remaining
	}
	return sessions[page.Offset : page.Offset+limit]
}
