//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package dataset provides utilities for loading evaluation datasets.
package dataset

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MTBench101Entry represents a single entry from MT-Bench-101.
// MT-Bench-101: A Fine-Grained Benchmark for Multi-Turn Dialogues (ACL 2024).
// Source: https://github.com/mtbench101/mt-bench-101.
type MTBench101Entry struct {
	ID      int              `json:"id"`
	Task    string           `json:"task"`
	History []MTBench101Turn `json:"history"`
}

// MTBench101Turn represents a single turn in MT-Bench-101 history.
type MTBench101Turn struct {
	User string `json:"user"`
	Bot  string `json:"bot"`
}

// DatasetLoader provides methods to load evaluation datasets.
type DatasetLoader struct {
	dataDir string
}

// NewDatasetLoader creates a new DatasetLoader.
func NewDatasetLoader(dataDir string) *DatasetLoader {
	return &DatasetLoader{dataDir: dataDir}
}

// LoadMTBench101 loads the MT-Bench-101 dataset.
// Expected file format: JSONL with fields: id, turns, category, task.
// If taskFilter is provided (non-empty), only entries with matching tasks are
// returned.
func (l *DatasetLoader) LoadMTBench101(
	filename string,
	taskFilter ...string,
) ([]*MTBench101Entry, error) {
	path := filepath.Join(l.dataDir, filename)
	entries, err := loadJSONL[*MTBench101Entry](path)
	if err != nil {
		return nil, err
	}

	allowed := make(map[string]bool)
	for _, t := range taskFilter {
		t = strings.TrimSpace(t)
		if t != "" {
			allowed[t] = true
		}
	}
	if len(allowed) == 0 {
		return entries, nil
	}

	filtered := make([]*MTBench101Entry, 0, len(entries))
	for _, entry := range entries {
		if allowed[entry.Task] {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

// loadJSONL loads a JSONL file into a slice of entries.
// Each line in the file should be a separate JSON object.
func loadJSONL[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	var entries []T
	decoder := json.NewDecoder(file)
	for {
		var entry T
		if err := decoder.Decode(&entry); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("decode entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ConvertMTBench101ToEvalCases converts MT-Bench-101 entries to EvalCases.
func ConvertMTBench101ToEvalCases(entries []*MTBench101Entry) []*evalset.EvalCase {
	cases := make([]*evalset.EvalCase, 0, len(entries))
	for _, entry := range entries {
		evalCase := mtBench101ToEvalCase(entry)
		if evalCase != nil {
			cases = append(cases, evalCase)
		}
	}
	return cases
}

func mtBench101ToEvalCase(entry *MTBench101Entry) *evalset.EvalCase {
	if len(entry.History) == 0 {
		return nil
	}

	invocations := make([]*evalset.Invocation, 0, len(entry.History)*2)
	for i, turn := range entry.History {
		// Add user turn.
		invocations = append(invocations, &evalset.Invocation{
			InvocationID: fmt.Sprintf("%d_user", i+1),
			UserContent: &model.Message{
				Role:    model.RoleUser,
				Content: turn.User,
			},
		})

		// Add bot turn as FinalResponse.
		if turn.Bot != "" {
			invocations[len(invocations)-1].FinalResponse = &model.Message{
				Role:    model.RoleAssistant,
				Content: turn.Bot,
			}
		}
	}

	// Note: EvalCase doesn't have Metadata field.
	return &evalset.EvalCase{
		EvalID:       fmt.Sprintf("%s_%d", entry.Task, entry.ID),
		Conversation: invocations,
	}
}
