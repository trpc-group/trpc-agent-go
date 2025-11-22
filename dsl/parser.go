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

// Parser parses JSON DSL into Workflow structures.
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

// Parse parses a JSON byte array into a Workflow.
func (p *Parser) Parse(data []byte) (*Workflow, error) {
	var workflow Workflow

	decoder := json.NewDecoder(bytes.NewReader(data))
	if p.Strict {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(&workflow); err != nil {
		return nil, fmt.Errorf("failed to parse DSL: %w", err)
	}

	// Auto-generate edge IDs if not provided
	for i := range workflow.Edges {
		if workflow.Edges[i].ID == "" {
			workflow.Edges[i].ID = fmt.Sprintf("edge_%d", i)
		}
	}

	// Auto-generate conditional edge IDs if not provided
	for i := range workflow.ConditionalEdges {
		if workflow.ConditionalEdges[i].ID == "" {
			workflow.ConditionalEdges[i].ID = fmt.Sprintf("cond_edge_%d", i)
		}
	}

	return &workflow, nil
}

// ParseFile parses a JSON file into a Workflow.
func (p *Parser) ParseFile(filename string) (*Workflow, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filename, err)
	}
	return p.Parse(data)
}

// ParseString parses a JSON string into a Workflow.
func (p *Parser) ParseString(jsonStr string) (*Workflow, error) {
	return p.Parse([]byte(jsonStr))
}

// ToJSON serializes a Workflow to JSON.
func ToJSON(workflow *Workflow) ([]byte, error) {
	return json.MarshalIndent(workflow, "", "  ")
}

// ToJSONString serializes a Workflow to a JSON string.
func ToJSONString(workflow *Workflow) (string, error) {
	data, err := ToJSON(workflow)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteToFile writes a Workflow to a JSON file.
func WriteToFile(workflow *Workflow, filename string) error {
	data, err := ToJSON(workflow)
	if err != nil {
		return fmt.Errorf("failed to serialize workflow: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filename, err)
	}

	return nil
}
