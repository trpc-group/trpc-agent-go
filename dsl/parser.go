//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
package dsl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Parser parses JSON DSL into Graph structures.
type Parser struct {
	// Strict mode enables strict JSON parsing (disallow unknown fields)
	Strict bool
}

// NewParser creates a new DSL parser.
func NewParser() *Parser {
	return &Parser{
		Strict: false,
	}
}

// NewStrictParser creates a new parser with strict mode enabled.
func NewStrictParser() *Parser {
	return &Parser{
		Strict: true,
	}
}

// Parse parses a JSON byte array into a Graph.
func (p *Parser) Parse(data []byte) (*Graph, error) {
	var graphDef Graph

	decoder := json.NewDecoder(bytes.NewReader(data))
	if p.Strict {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(&graphDef); err != nil {
		return nil, fmt.Errorf("failed to parse DSL: %w", err)
	}

	// Auto-generate edge IDs if not provided
	for i := range graphDef.Edges {
		if graphDef.Edges[i].ID == "" {
			graphDef.Edges[i].ID = fmt.Sprintf("edge_%d", i)
		}
	}

	// Auto-generate conditional edge IDs if not provided
	for i := range graphDef.ConditionalEdges {
		if graphDef.ConditionalEdges[i].ID == "" {
			graphDef.ConditionalEdges[i].ID = fmt.Sprintf("cond_edge_%d", i)
		}
	}

	return &graphDef, nil
}

// ParseFile parses a JSON file into a Graph.
func (p *Parser) ParseFile(filename string) (*Graph, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filename, err)
	}
	return p.Parse(data)
}

// ParseString parses a JSON string into a Graph.
func (p *Parser) ParseString(jsonStr string) (*Graph, error) {
	return p.Parse([]byte(jsonStr))
}

// ToJSON serializes a Graph to JSON.
func ToJSON(graphDef *Graph) ([]byte, error) {
	return json.MarshalIndent(graphDef, "", "  ")
}

// ToJSONString serializes a Graph to a JSON string.
func ToJSONString(graphDef *Graph) (string, error) {
	data, err := ToJSON(graphDef)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteToFile writes a Graph to a JSON file.
func WriteToFile(graphDef *Graph, filename string) error {
	data, err := ToJSON(graphDef)
	if err != nil {
		return fmt.Errorf("failed to serialize graph: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filename, err)
	}

	return nil
}
