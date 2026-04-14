//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
)

type normalizedStep struct {
	Label        string
	NodeID       string
	Predecessors []string
	Input        string
	Output       string
	Error        string
}

func printExecutionTrace(executionTrace *atrace.Trace, staticNodeIDs []string) {
	steps, countsByNode := normalizeSteps(executionTrace)
	edges := collectTraceEdges(steps)
	skippedNodeIDs := collectSkippedNodeIDs(staticNodeIDs, countsByNode)
	repeatedNodeIDs := collectRepeatedNodeIDs(countsByNode)
	fmt.Println("GraphAgent execution trace")
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Root Agent: %s\n", executionTrace.RootAgentName)
	fmt.Printf("Session ID: %s\n", executionTrace.SessionID)
	fmt.Printf("Status: %s\n", executionTrace.Status)
	fmt.Printf("Step Count: %d\n", len(steps))
	fmt.Println()
	fmt.Println("Step order")
	for index, step := range steps {
		fmt.Printf("%d. %s\n", index+1, step.Label)
		fmt.Printf("   node: %s\n", step.NodeID)
		if len(step.Predecessors) == 0 {
			fmt.Println("   predecessors: (root)")
		} else {
			fmt.Printf("   predecessors: %s\n", strings.Join(step.Predecessors, ", "))
		}
		fmt.Printf("   input: %s\n", step.Input)
		fmt.Printf("   output: %s\n", step.Output)
		if step.Error == "" {
			fmt.Println("   error: (none)")
		} else {
			fmt.Printf("   error: %s\n", step.Error)
		}
	}
	fmt.Println()
	fmt.Println("Trace edges")
	for _, edge := range edges {
		fmt.Printf("- %s\n", edge)
	}
	fmt.Println()
	fmt.Println("Summary")
	if len(repeatedNodeIDs) == 0 {
		fmt.Println("- Repeated nodes: (none)")
	} else {
		items := make([]string, 0, len(repeatedNodeIDs))
		for _, nodeID := range repeatedNodeIDs {
			items = append(items, fmt.Sprintf("%s x%d", nodeID, countsByNode[nodeID]))
		}
		fmt.Printf("- Repeated nodes: %s\n", strings.Join(items, ", "))
	}
	if len(skippedNodeIDs) == 0 {
		fmt.Println("- Skipped nodes: (none)")
	} else {
		fmt.Printf("- Skipped nodes: %s\n", strings.Join(skippedNodeIDs, ", "))
	}
	fmt.Printf("- Final step labels: %s\n", strings.Join(finalStepLabels(steps), ", "))
}

func normalizeSteps(executionTrace *atrace.Trace) ([]normalizedStep, map[string]int) {
	countsByNode := make(map[string]int)
	stepLabels := make(map[string]string, len(executionTrace.Steps))
	for _, step := range executionTrace.Steps {
		countsByNode[step.NodeID]++
		stepLabels[step.StepID] = fmt.Sprintf("%s#%d", step.NodeID, countsByNode[step.NodeID])
	}
	normalized := make([]normalizedStep, 0, len(executionTrace.Steps))
	for _, step := range executionTrace.Steps {
		predecessors := make([]string, 0, len(step.PredecessorStepIDs))
		for _, predecessorStepID := range step.PredecessorStepIDs {
			label, ok := stepLabels[predecessorStepID]
			if !ok {
				predecessors = append(predecessors, "<missing:"+predecessorStepID+">")
				continue
			}
			predecessors = append(predecessors, label)
		}
		sort.Strings(predecessors)
		normalized = append(normalized, normalizedStep{
			Label:        stepLabels[step.StepID],
			NodeID:       step.NodeID,
			Predecessors: predecessors,
			Input:        summarizeSnapshot(step.Input),
			Output:       summarizeSnapshot(step.Output),
			Error:        step.Error,
		})
	}
	return normalized, countsByNode
}

func summarizeSnapshot(snapshot *atrace.Snapshot) string {
	if snapshot == nil {
		return "(none)"
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(snapshot.Text), &payload); err != nil {
		return snapshot.Text
	}
	parts := make([]string, 0, 3)
	if userInput, ok := payload["user_input"].(string); ok && userInput != "" {
		parts = append(parts, fmt.Sprintf("user_input=%q", userInput))
	}
	if routeCount, ok := payload["route_count"].(float64); ok {
		parts = append(parts, fmt.Sprintf("route_count=%d", int(routeCount)))
	}
	if visited, ok := payload["visited"].([]any); ok {
		parts = append(parts, fmt.Sprintf("visited=%s", formatStringList(visited)))
	}
	if len(parts) == 0 {
		return snapshot.Text
	}
	return strings.Join(parts, ", ")
}

func formatStringList(values []any) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			items = append(items, text)
		}
	}
	return "[" + strings.Join(items, ", ") + "]"
}

func collectTraceEdges(steps []normalizedStep) []string {
	edges := make([]string, 0)
	for _, step := range steps {
		for _, predecessor := range step.Predecessors {
			edges = append(edges, predecessor+" -> "+step.Label)
		}
	}
	return edges
}

func collectSkippedNodeIDs(staticNodeIDs []string, countsByNode map[string]int) []string {
	skipped := make([]string, 0)
	for _, nodeID := range staticNodeIDs {
		if countsByNode[nodeID] == 0 {
			skipped = append(skipped, nodeID)
		}
	}
	return skipped
}

func collectRepeatedNodeIDs(countsByNode map[string]int) []string {
	repeated := make([]string, 0)
	for nodeID, count := range countsByNode {
		if count > 1 {
			repeated = append(repeated, nodeID)
		}
	}
	sort.Strings(repeated)
	return repeated
}

func finalStepLabels(steps []normalizedStep) []string {
	isPredecessor := make(map[string]struct{})
	for _, step := range steps {
		for _, predecessor := range step.Predecessors {
			isPredecessor[predecessor] = struct{}{}
		}
	}
	finals := make([]string, 0)
	for _, step := range steps {
		if _, ok := isPredecessor[step.Label]; ok {
			continue
		}
		finals = append(finals, step.Label)
	}
	sort.Strings(finals)
	return finals
}
