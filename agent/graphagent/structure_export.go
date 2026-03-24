//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graphagent

import (
	"context"
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Export exports the static structure of the graph agent.
func (ga *GraphAgent) Export(
	ctx context.Context,
	exportChild structure.ChildExporter,
) (*structure.Snapshot, error) {
	rootNodeID := istructure.EscapeLocalName(ga.name)
	snapshot := &structure.Snapshot{
		EntryNodeID: rootNodeID,
		Nodes: []structure.Node{
			{
				NodeID: rootNodeID,
				Kind:   structure.NodeKindAgent,
				Name:   ga.name,
			},
		},
	}
	nodes := ga.graph.Nodes()
	nodePaths := make(map[string]string, len(nodes))
	for _, node := range nodes {
		nodePath := istructure.JoinNodeID(rootNodeID, node.ID)
		nodePaths[node.ID] = nodePath
		snapshot.Nodes = append(snapshot.Nodes, structure.Node{
			NodeID: nodePath,
			Kind:   nodeKindFromGraphNodeType(node.Type),
			Name:   node.Name,
		})
	}
	entryNodeID := ga.graph.EntryPoint()
	if entryPath, ok := nodePaths[entryNodeID]; ok {
		snapshot.Edges = append(snapshot.Edges, structure.Edge{
			FromNodeID: rootNodeID,
			ToNodeID:   entryPath,
		})
	}
	for _, node := range nodes {
		nodePath := nodePaths[node.ID]
		for _, edge := range ga.graph.Edges(node.ID) {
			if edge == nil || edge.From == graph.Start || edge.To == graph.End {
				continue
			}
			toNodeID, ok := nodePaths[edge.To]
			if !ok {
				continue
			}
			snapshot.Edges = append(snapshot.Edges, structure.Edge{
				FromNodeID: nodePath,
				ToNodeID:   toNodeID,
			})
		}
		targets := collectConditionalTargets(ga.graph, node)
		for _, target := range targets {
			toNodeID, ok := nodePaths[target]
			if !ok {
				continue
			}
			snapshot.Edges = append(snapshot.Edges, structure.Edge{
				FromNodeID: nodePath,
				ToNodeID:   toNodeID,
			})
		}
		snapshot.Surfaces = append(snapshot.Surfaces, exportGraphNodeSurfaces(ctx, node, nodePath)...)
		if node.Type != graph.NodeTypeAgent {
			continue
		}
		childAgent := ga.FindSubAgent(node.ID)
		if childAgent == nil {
			return nil, fmt.Errorf("sub-agent %q not found", node.ID)
		}
		childSnapshot, err := exportChild(ctx, childAgent)
		if err != nil {
			return nil, err
		}
		mountPath := istructure.JoinNodeID(nodePath, childAgent.Info().Name)
		rebased, err := istructure.RebaseSnapshot(childSnapshot, mountPath)
		if err != nil {
			return nil, err
		}
		snapshot.Nodes = append(snapshot.Nodes, rebased.Nodes...)
		snapshot.Edges = append(snapshot.Edges, rebased.Edges...)
		snapshot.Surfaces = append(snapshot.Surfaces, rebased.Surfaces...)
		snapshot.Edges = append(snapshot.Edges, structure.Edge{
			FromNodeID: nodePath,
			ToNodeID:   rebased.EntryNodeID,
		})
	}
	return snapshot, nil
}

func nodeKindFromGraphNodeType(nodeType graph.NodeType) structure.NodeKind {
	switch nodeType {
	case graph.NodeTypeLLM:
		return structure.NodeKindLLM
	case graph.NodeTypeTool:
		return structure.NodeKindTool
	case graph.NodeTypeAgent:
		return structure.NodeKindAgent
	default:
		return structure.NodeKindFunction
	}
}

func exportGraphNodeSurfaces(
	ctx context.Context,
	node *graph.Node,
	nodeID string,
) []structure.Surface {
	switch node.Type {
	case graph.NodeTypeLLM:
		surfaces := []structure.Surface{
			{
				NodeID: nodeID,
				Type:   structure.SurfaceTypeInstruction,
				Value: structure.SurfaceValue{
					Text: stringPtr(node.Instruction()),
				},
			},
		}
		if currentModel := node.Model(); currentModel != nil {
			modelInfo := currentModel.Info()
			surfaces = append(surfaces, structure.Surface{
				NodeID: nodeID,
				Type:   structure.SurfaceTypeModel,
				Value: structure.SurfaceValue{
					Model: &structure.ModelRef{Name: modelInfo.Name},
				},
			})
		}
		if node.HasTools() {
			surfaces = append(surfaces, structure.Surface{
				NodeID: nodeID,
				Type:   structure.SurfaceTypeTool,
				Value: structure.SurfaceValue{
					Tools: toolRefsFromTools(node.Tools(ctx)),
				},
			})
		}
		return surfaces
	case graph.NodeTypeTool:
		if !node.HasTools() {
			return nil
		}
		return []structure.Surface{
			{
				NodeID: nodeID,
				Type:   structure.SurfaceTypeTool,
				Value: structure.SurfaceValue{
					Tools: toolRefsFromTools(node.Tools(ctx)),
				},
			},
		}
	default:
		return nil
	}
}

func toolRefsFromTools(tools []tool.Tool) []structure.ToolRef {
	if len(tools) == 0 {
		return nil
	}
	refs := make([]structure.ToolRef, 0, len(tools))
	for _, currentTool := range tools {
		if currentTool == nil || currentTool.Declaration() == nil {
			continue
		}
		declaration := currentTool.Declaration()
		refs = append(refs, structure.ToolRef{
			ID:           declaration.Name,
			Description:  declaration.Description,
			InputSchema:  declaration.InputSchema,
			OutputSchema: declaration.OutputSchema,
		})
	}
	return refs
}

func stringPtr(value string) *string {
	return &value
}

func collectConditionalTargets(g *graph.Graph, node *graph.Node) []string {
	targets := append([]string(nil), node.EndTargets()...)
	if conditionalEdge, ok := g.ConditionalEdge(node.ID); ok && conditionalEdge != nil {
		for _, target := range conditionalEdge.PathMap {
			if target == "" || target == graph.End {
				continue
			}
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	return uniqueTargets(targets)
}

func uniqueTargets(targets []string) []string {
	if len(targets) == 0 {
		return nil
	}
	out := targets[:0]
	var last string
	for i, target := range targets {
		if i == 0 || target != last {
			out = append(out, target)
			last = target
		}
	}
	return out
}
