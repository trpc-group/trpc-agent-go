//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package graph

import "fmt"

// Builder provides a fluent interface for building graphs.
type Builder struct {
	graph *Graph
}

// NewBuilder creates a new graph builder.
func NewBuilder() *Builder {
	return &Builder{
		graph: New(),
	}
}

// AddStartNode adds a start node to the graph.
func (b *Builder) AddStartNode(id, name string) *Builder {
	node := &Node{
		ID:   id,
		Type: NodeTypeStart,
		Name: name,
	}
	b.graph.AddNode(node)
	return b
}

// AddEndNode adds an end node to the graph.
func (b *Builder) AddEndNode(id, name string) *Builder {
	node := &Node{
		ID:   id,
		Type: NodeTypeEnd,
		Name: name,
	}
	b.graph.AddNode(node)
	return b
}

// AddFunctionNode adds a function node to the graph.
func (b *Builder) AddFunctionNode(id, name, description string, fn NodeFunc) *Builder {
	node := &Node{
		ID:          id,
		Type:        NodeTypeFunction,
		Name:        name,
		Description: description,
		Function:    fn,
	}
	b.graph.AddNode(node)
	return b
}

// AddAgentNode adds an agent node to the graph.
func (b *Builder) AddAgentNode(id, name, description, agentName string) *Builder {
	node := &Node{
		ID:          id,
		Type:        NodeTypeAgent,
		Name:        name,
		Description: description,
		AgentName:   agentName,
	}
	b.graph.AddNode(node)
	return b
}

// AddConditionNode adds a condition node to the graph.
func (b *Builder) AddConditionNode(id, name, description string, condition ConditionFunc) *Builder {
	node := &Node{
		ID:          id,
		Type:        NodeTypeCondition,
		Name:        name,
		Description: description,
		Condition:   condition,
	}
	b.graph.AddNode(node)
	return b
}

// AddEdge adds an edge between two nodes.
func (b *Builder) AddEdge(from, to string) *Builder {
	edge := &Edge{
		From: from,
		To:   to,
	}
	b.graph.AddEdge(edge)
	return b
}

// AddConditionalEdge adds a conditional edge between two nodes.
func (b *Builder) AddConditionalEdge(from, to, condition string) *Builder {
	edge := &Edge{
		From:      from,
		To:        to,
		Condition: condition,
	}
	b.graph.AddEdge(edge)
	return b
}

// Build returns the constructed graph.
func (b *Builder) Build() (*Graph, error) {
	if err := b.graph.Validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}
	return b.graph, nil
}

// MustBuild returns the constructed graph or panics if invalid.
func (b *Builder) MustBuild() *Graph {
	graph, err := b.Build()
	if err != nil {
		panic(err)
	}
	return graph
}
