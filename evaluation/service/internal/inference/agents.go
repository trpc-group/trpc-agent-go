//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inference

import (
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

type agentInvocationBucket struct {
	invocationID       string
	parentInvocationID string
	branch             string
	author             string
	firstSeenIndex     int
}

func (b *agentInvocationBucket) observeEvent(e *event.Event) {
	if b == nil || e == nil {
		return
	}
	if b.parentInvocationID == "" && e.ParentInvocationID != "" {
		b.parentInvocationID = e.ParentInvocationID
	}
	if b.branch == "" && e.Branch != "" {
		b.branch = e.Branch
	}
	if b.author == "" && e.Author != "" {
		b.author = e.Author
	}
}

func deriveAgentName(branch, author string) string {
	if branch != "" {
		parts := strings.Split(branch, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" {
				return parts[i]
			}
		}
	}
	switch author {
	case "", "user", "graph-node", "graph-pregel":
		return ""
	default:
		return author
	}
}

// buildAgentInvocations builds a stable, best-effort list of executed agents from event buckets.
func buildAgentInvocations(
	buckets map[string]*agentInvocationBucket,
	mainRootInvocationID string,
) []*evalset.Agent {
	if len(buckets) == 0 {
		return nil
	}

	lessByFirstSeen := func(a, b string) bool {
		ab := buckets[a]
		bb := buckets[b]
		if ab == nil || bb == nil {
			return a < b
		}
		if ab.firstSeenIndex != bb.firstSeenIndex {
			return ab.firstSeenIndex < bb.firstSeenIndex
		}
		return a < b
	}

	childrenByParent := make(map[string][]string, len(buckets))
	for id, bucket := range buckets {
		if bucket == nil || bucket.parentInvocationID == "" {
			continue
		}
		childrenByParent[bucket.parentInvocationID] = append(childrenByParent[bucket.parentInvocationID], id)
	}
	for parentID, children := range childrenByParent {
		sort.Slice(children, func(i, j int) bool {
			return lessByFirstSeen(children[i], children[j])
		})
		childrenByParent[parentID] = children
	}

	roots := make([]string, 0, len(buckets))
	for id, bucket := range buckets {
		if bucket == nil {
			continue
		}
		if bucket.parentInvocationID == "" || buckets[bucket.parentInvocationID] == nil {
			roots = append(roots, id)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		a, b := roots[i], roots[j]
		if mainRootInvocationID != "" {
			if a == mainRootInvocationID && b != mainRootInvocationID {
				return true
			}
			if b == mainRootInvocationID && a != mainRootInvocationID {
				return false
			}
		}
		return lessByFirstSeen(a, b)
	})

	visited := make(map[string]struct{}, len(buckets))
	out := make([]*evalset.Agent, 0, len(buckets))

	var walk func(string)
	walk = func(invocationID string) {
		if invocationID == "" {
			return
		}
		if _, ok := visited[invocationID]; ok {
			return
		}
		bucket := buckets[invocationID]
		if bucket == nil {
			return
		}
		visited[invocationID] = struct{}{}
		out = append(out, &evalset.Agent{
			InvocationID:       bucket.invocationID,
			ParentInvocationID: bucket.parentInvocationID,
			Name:               deriveAgentName(bucket.branch, bucket.author),
			Branch:             bucket.branch,
		})
		for _, childID := range childrenByParent[invocationID] {
			walk(childID)
		}
	}

	for _, rootID := range roots {
		walk(rootID)
	}

	if len(visited) == len(buckets) {
		return out
	}

	remaining := make([]string, 0, len(buckets)-len(visited))
	for id := range buckets {
		if _, ok := visited[id]; ok {
			continue
		}
		remaining = append(remaining, id)
	}
	sort.Slice(remaining, func(i, j int) bool {
		return lessByFirstSeen(remaining[i], remaining[j])
	})
	for _, id := range remaining {
		walk(id)
	}

	return out
}
