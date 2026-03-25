//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package structure provides internal helpers for static structure export.
package structure

import (
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

// PathAllocator allocates stable child paths under one parent node.
type PathAllocator struct {
	parentNodeID string
	used         map[string]int
}

// NewPathAllocator creates a new path allocator for one parent node.
func NewPathAllocator(parentNodeID string) *PathAllocator {
	return &PathAllocator{
		parentNodeID: parentNodeID,
		used:         make(map[string]int),
	}
}

// Next returns the next stable child path for the given local name.
func (a *PathAllocator) Next(localName string) string {
	escaped := EscapeLocalName(localName)
	count := a.used[escaped] + 1
	a.used[escaped] = count
	if count == 1 {
		return joinEscapedNodeID(a.parentNodeID, escaped)
	}
	return joinEscapedNodeID(a.parentNodeID, fmt.Sprintf("%s~%d", escaped, count))
}

// EscapeLocalName escapes one path segment into a stable node-id segment.
func EscapeLocalName(name string) string {
	if name == "" {
		return "_"
	}
	replacer := strings.NewReplacer("~", "~0", "/", "~1")
	escaped := replacer.Replace(name)
	if escaped == "" {
		return "_"
	}
	return escaped
}

// JoinNodeID joins a parent node id and a local name into a child node id.
func JoinNodeID(parentNodeID string, localName string) string {
	return joinEscapedNodeID(parentNodeID, EscapeLocalName(localName))
}

// RebaseSnapshot rewrites one snapshot to a new mounted root node id.
func RebaseSnapshot(
	snapshot *structure.Snapshot,
	newRootNodeID string,
) (*structure.Snapshot, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("snapshot is nil")
	}
	oldRoot := snapshot.EntryNodeID
	if oldRoot == "" {
		return nil, fmt.Errorf("entry node id is empty")
	}
	rebased := &structure.Snapshot{
		EntryNodeID: newRootNodeID,
		Nodes:       make([]structure.Node, 0, len(snapshot.Nodes)),
		Edges:       make([]structure.Edge, 0, len(snapshot.Edges)),
		Surfaces:    make([]structure.Surface, 0, len(snapshot.Surfaces)),
	}
	for _, node := range snapshot.Nodes {
		nodeID, err := rebaseNodeID(node.NodeID, oldRoot, newRootNodeID)
		if err != nil {
			return nil, err
		}
		node.NodeID = nodeID
		rebased.Nodes = append(rebased.Nodes, node)
	}
	for _, edge := range snapshot.Edges {
		fromNodeID, err := rebaseNodeID(edge.FromNodeID, oldRoot, newRootNodeID)
		if err != nil {
			return nil, err
		}
		toNodeID, err := rebaseNodeID(edge.ToNodeID, oldRoot, newRootNodeID)
		if err != nil {
			return nil, err
		}
		rebased.Edges = append(rebased.Edges, structure.Edge{
			FromNodeID: fromNodeID,
			ToNodeID:   toNodeID,
		})
	}
	for _, surface := range snapshot.Surfaces {
		nodeID, err := rebaseNodeID(surface.NodeID, oldRoot, newRootNodeID)
		if err != nil {
			return nil, err
		}
		surface.NodeID = nodeID
		surface.SurfaceID = ""
		rebased.Surfaces = append(rebased.Surfaces, surface)
	}
	return rebased, nil
}

// TerminalNodeIDs returns the static terminal node ids of a snapshot.
func TerminalNodeIDs(snapshot *structure.Snapshot) []string {
	if snapshot == nil || len(snapshot.Nodes) == 0 {
		return nil
	}
	nodeIDs := make(map[string]struct{}, len(snapshot.Nodes))
	adjacency := make(map[string][]string, len(snapshot.Edges))
	outgoing := make(map[string]struct{}, len(snapshot.Edges))
	for _, node := range snapshot.Nodes {
		nodeIDs[node.NodeID] = struct{}{}
	}
	for _, edge := range snapshot.Edges {
		_, fromExists := nodeIDs[edge.FromNodeID]
		_, toExists := nodeIDs[edge.ToNodeID]
		if !fromExists || !toExists {
			continue
		}
		adjacency[edge.FromNodeID] = append(adjacency[edge.FromNodeID], edge.ToNodeID)
		outgoing[edge.FromNodeID] = struct{}{}
	}
	reachable := reachableNodeIDs(snapshot.EntryNodeID, nodeIDs, adjacency)
	if len(reachable) == 0 {
		return nil
	}
	terminals := make([]string, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if _, exists := reachable[node.NodeID]; !exists {
			continue
		}
		if _, exists := outgoing[node.NodeID]; exists {
			continue
		}
		terminals = append(terminals, node.NodeID)
	}
	sort.Strings(terminals)
	return terminals
}

// RootOnly returns a snapshot that keeps only the root node and its surfaces.
func RootOnly(snapshot *structure.Snapshot) *structure.Snapshot {
	if snapshot == nil || snapshot.EntryNodeID == "" {
		return &structure.Snapshot{}
	}
	root := structure.Snapshot{
		EntryNodeID: snapshot.EntryNodeID,
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID == snapshot.EntryNodeID {
			root.Nodes = append(root.Nodes, node)
			break
		}
	}
	for _, surface := range snapshot.Surfaces {
		if surface.NodeID == snapshot.EntryNodeID {
			root.Surfaces = append(root.Surfaces, surface)
		}
	}
	return &root
}

func joinEscapedNodeID(parentNodeID string, escaped string) string {
	if parentNodeID == "" {
		return escaped
	}
	if escaped == "" {
		return parentNodeID
	}
	return parentNodeID + "/" + escaped
}

func rebaseNodeID(nodeID string, oldRoot string, newRoot string) (string, error) {
	if nodeID == oldRoot {
		return newRoot, nil
	}
	prefix := oldRoot + "/"
	if !strings.HasPrefix(nodeID, prefix) {
		return "", fmt.Errorf("node id %q is outside root %q", nodeID, oldRoot)
	}
	return newRoot + strings.TrimPrefix(nodeID, oldRoot), nil
}

func reachableNodeIDs(
	entryNodeID string,
	nodeIDs map[string]struct{},
	adjacency map[string][]string,
) map[string]struct{} {
	if _, ok := nodeIDs[entryNodeID]; !ok {
		return nil
	}
	reachable := map[string]struct{}{entryNodeID: {}}
	queue := []string{entryNodeID}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[nodeID] {
			if _, ok := reachable[next]; ok {
				continue
			}
			reachable[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return reachable
}
