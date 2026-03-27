//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

func printSnapshot(snapshot *structure.Snapshot) {
	nodes := append([]structure.Node(nil), snapshot.Nodes...)
	edges := append([]structure.Edge(nil), snapshot.Edges...)
	surfaces := append([]structure.Surface(nil), snapshot.Surfaces...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromNodeID != edges[j].FromNodeID {
			return edges[i].FromNodeID < edges[j].FromNodeID
		}
		return edges[i].ToNodeID < edges[j].ToNodeID
	})
	sort.Slice(surfaces, func(i, j int) bool { return surfaces[i].SurfaceID < surfaces[j].SurfaceID })
	surfacesByNode := make(map[string][]structure.Surface, len(nodes))
	for _, surface := range surfaces {
		surfacesByNode[surface.NodeID] = append(surfacesByNode[surface.NodeID], surface)
	}
	fmt.Println("GraphAgent static structure snapshot")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Structure ID: %s\n", snapshot.StructureID)
	fmt.Printf("Entry Node:   %s\n", snapshot.EntryNodeID)
	fmt.Printf("Node Count:   %d\n", len(nodes))
	fmt.Printf("Edge Count:   %d\n", len(edges))
	fmt.Printf("Surface Count: %d\n", len(surfaces))
	fmt.Println()
	fmt.Println("Nodes")
	for _, node := range nodes {
		fmt.Printf("- %s [%s] name=%s\n", node.NodeID, node.Kind, node.Name)
	}
	fmt.Println()
	fmt.Println("Edges")
	for _, edge := range edges {
		fmt.Printf("- %s -> %s\n", edge.FromNodeID, edge.ToNodeID)
	}
	fmt.Println()
	fmt.Println("Surfaces")
	for _, node := range nodes {
		nodeSurfaces := surfacesByNode[node.NodeID]
		if len(nodeSurfaces) == 0 {
			continue
		}
		fmt.Printf("- %s\n", node.NodeID)
		for _, surface := range nodeSurfaces {
			fmt.Printf("  - %s: %s\n", surface.Type, formatSurfaceValue(surface))
		}
	}
	fmt.Println()
	printHighlights(snapshot.EntryNodeID, edges)
}

func formatSurfaceValue(surface structure.Surface) string {
	switch surface.Type {
	case structure.SurfaceTypeInstruction, structure.SurfaceTypeGlobalInstruction:
		if surface.Value.Text == nil {
			return "(empty)"
		}
		return fmt.Sprintf("%q", *surface.Value.Text)
	case structure.SurfaceTypeModel:
		if surface.Value.Model == nil {
			return "(none)"
		}
		return surface.Value.Model.Name
	case structure.SurfaceTypeTool:
		if len(surface.Value.Tools) == 0 {
			return "(none)"
		}
		items := make([]string, 0, len(surface.Value.Tools))
		for _, ref := range surface.Value.Tools {
			if ref.Description == "" {
				items = append(items, ref.ID)
				continue
			}
			items = append(items, fmt.Sprintf("%s (%s)", ref.ID, ref.Description))
		}
		return strings.Join(items, ", ")
	case structure.SurfaceTypeSkill:
		if len(surface.Value.Skills) == 0 {
			return "(none)"
		}
		items := make([]string, 0, len(surface.Value.Skills))
		for _, ref := range surface.Value.Skills {
			if ref.Description == "" {
				items = append(items, ref.ID)
				continue
			}
			items = append(items, fmt.Sprintf("%s (%s)", ref.ID, ref.Description))
		}
		return strings.Join(items, ", ")
	case structure.SurfaceTypeFewShot:
		return fmt.Sprintf("%d example group(s)", len(surface.Value.FewShot))
	default:
		return "(unsupported)"
	}
}

func printHighlights(rootNodeID string, edges []structure.Edge) {
	outgoing := make(map[string][]string)
	incoming := make(map[string][]string)
	for _, edge := range edges {
		outgoing[edge.FromNodeID] = append(outgoing[edge.FromNodeID], edge.ToNodeID)
		incoming[edge.ToNodeID] = append(incoming[edge.ToNodeID], edge.FromNodeID)
	}
	for nodeID := range outgoing {
		sort.Strings(outgoing[nodeID])
	}
	for nodeID := range incoming {
		sort.Strings(incoming[nodeID])
	}
	fmt.Println("Highlights")
	fmt.Println("These summaries are derived from static possible edges.")
	fmt.Println("- Branch points")
	if !printBranchPoints(outgoing) {
		fmt.Println("  - (none)")
	}
	fmt.Println("- Fan-in points")
	if !printFanInPoints(rootNodeID, incoming) {
		fmt.Println("  - (none)")
	}
	fmt.Println("- Loop regions")
	if !printLoopRegions(edges, outgoing) {
		fmt.Println("  - (none)")
	}
}

func printBranchPoints(outgoing map[string][]string) bool {
	nodeIDs := make([]string, 0, len(outgoing))
	for nodeID, targets := range outgoing {
		if len(targets) > 1 {
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		fmt.Printf("  - %s can branch to %s\n", nodeID, strings.Join(outgoing[nodeID], ", "))
	}
	return len(nodeIDs) > 0
}

func printFanInPoints(rootNodeID string, incoming map[string][]string) bool {
	nodeIDs := make([]string, 0, len(incoming))
	for nodeID, sources := range incoming {
		filtered := filterSources(sources, rootNodeID)
		if len(filtered) > 1 {
			nodeIDs = append(nodeIDs, nodeID)
			incoming[nodeID] = filtered
		}
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		fmt.Printf("  - %s can receive input from %s\n", nodeID, strings.Join(incoming[nodeID], ", "))
	}
	return len(nodeIDs) > 0
}

func printLoopRegions(edges []structure.Edge, outgoing map[string][]string) bool {
	components := findStronglyConnectedComponents(edges, outgoing)
	if len(components) == 0 {
		return false
	}
	for _, component := range components {
		fmt.Printf("  - %s form one loop region\n", strings.Join(component, ", "))
	}
	return true
}

func canReach(outgoing map[string][]string, start string, target string) bool {
	if start == target {
		return true
	}
	visited := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		for _, next := range outgoing[nodeID] {
			if visited[next] {
				continue
			}
			if next == target {
				return true
			}
			visited[next] = true
			queue = append(queue, next)
		}
	}
	return false
}

func filterSources(sources []string, ignored string) []string {
	filtered := make([]string, 0, len(sources))
	for _, source := range sources {
		if source == ignored {
			continue
		}
		filtered = append(filtered, source)
	}
	return filtered
}

func findStronglyConnectedComponents(
	edges []structure.Edge,
	outgoing map[string][]string,
) [][]string {
	reverse := make(map[string][]string)
	nodeSet := make(map[string]struct{})
	for _, edge := range edges {
		nodeSet[edge.FromNodeID] = struct{}{}
		nodeSet[edge.ToNodeID] = struct{}{}
		reverse[edge.ToNodeID] = append(reverse[edge.ToNodeID], edge.FromNodeID)
	}
	nodeIDs := make([]string, 0, len(nodeSet))
	for nodeID := range nodeSet {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	order := make([]string, 0, len(nodeIDs))
	visited := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if visited[nodeID] {
			continue
		}
		dfsFinishOrder(nodeID, outgoing, visited, &order)
	}
	for nodeID := range visited {
		visited[nodeID] = false
	}
	components := make([][]string, 0)
	for i := len(order) - 1; i >= 0; i-- {
		nodeID := order[i]
		if visited[nodeID] {
			continue
		}
		component := make([]string, 0)
		dfsCollectComponent(nodeID, reverse, visited, &component)
		if !isLoopComponent(component, outgoing) {
			continue
		}
		sort.Strings(component)
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool {
		return strings.Join(components[i], ",") < strings.Join(components[j], ",")
	})
	return components
}

func dfsFinishOrder(
	nodeID string,
	outgoing map[string][]string,
	visited map[string]bool,
	order *[]string,
) {
	visited[nodeID] = true
	for _, next := range outgoing[nodeID] {
		if visited[next] {
			continue
		}
		dfsFinishOrder(next, outgoing, visited, order)
	}
	*order = append(*order, nodeID)
}

func dfsCollectComponent(
	nodeID string,
	reverse map[string][]string,
	visited map[string]bool,
	component *[]string,
) {
	visited[nodeID] = true
	*component = append(*component, nodeID)
	for _, next := range reverse[nodeID] {
		if visited[next] {
			continue
		}
		dfsCollectComponent(next, reverse, visited, component)
	}
}

func isLoopComponent(component []string, outgoing map[string][]string) bool {
	if len(component) > 1 {
		return true
	}
	if len(component) == 0 {
		return false
	}
	nodeID := component[0]
	for _, next := range outgoing[nodeID] {
		if next == nodeID {
			return true
		}
	}
	return false
}
