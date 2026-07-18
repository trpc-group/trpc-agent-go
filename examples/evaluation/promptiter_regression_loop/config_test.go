//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

func TestLoadConfigIsStable(t *testing.T) {
	first, err := loadConfig(defaultPaths(), 2003)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadConfig(defaultPaths(), 2003)
	if err != nil {
		t.Fatal(err)
	}
	if first.configHash == "" || first.configHash != second.configHash || len(first.inputs) != 6 {
		t.Fatalf("config hashes or inputs are unstable: %q %q %+v", first.configHash, second.configHash, first.inputs)
	}
	for _, input := range first.inputs {
		if len(input.SHA256) != 64 {
			t.Fatalf("input hash = %+v", input)
		}
	}
}

func TestLoadConfigRejectsUnreadableAndInvalidJSON(t *testing.T) {
	paths := defaultPaths()
	paths.metrics = filepath.Join(t.TempDir(), "missing.json")
	if _, err := loadConfig(paths, 1); err == nil || !strings.Contains(err.Error(), "read metrics") {
		t.Fatalf("missing metrics error = %v", err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths = defaultPaths()
	paths.promptiter = invalid
	if _, err := loadConfig(paths, 1); err == nil || !strings.Contains(err.Error(), "decode promptiter") {
		t.Fatalf("invalid JSON error = %v", err)
	}
}

func TestValidateConfigRejectsInvalidFixtures(t *testing.T) {
	valid, err := loadConfig(defaultPaths(), 2003)
	if err != nil {
		t.Fatal(err)
	}
	threeCases := func(id string) *evalset.EvalSet {
		return &evalset.EvalSet{EvalSetID: id, EvalCases: []*evalset.EvalCase{{}, {}, {}}}
	}
	tests := []struct {
		name   string
		mutate func(*loopConfig)
	}{
		{name: "empty baseline", mutate: func(c *loopConfig) { c.baseline = "" }},
		{name: "missing evalset", mutate: func(c *loopConfig) { c.train = nil }},
		{name: "missing ID", mutate: func(c *loopConfig) { c.train = threeCases("") }},
		{name: "same ID", mutate: func(c *loopConfig) { c.validation.EvalSetID = c.train.EvalSetID }},
		{name: "wrong case count", mutate: func(c *loopConfig) { c.train.EvalCases = nil }},
		{name: "no metrics", mutate: func(c *loopConfig) { c.metrics = nil }},
		{name: "rounds uncovered", mutate: func(c *loopConfig) { c.maxRounds = len(c.candidates) + 1 }},
		{name: "empty engine ID", mutate: func(c *loopConfig) { c.engine.EngineID = "" }},
		{name: "negative engine cost", mutate: func(c *loopConfig) { c.engine.PromptTokens = -1 }},
		{name: "empty candidate", mutate: func(c *loopConfig) { c.candidates[0] = " " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.train = threeCases(valid.train.EvalSetID)
			config.validation = threeCases(valid.validation.EvalSetID)
			config.metrics = append([]*metric.EvalMetric(nil), valid.metrics...)
			config.candidates = append([]string(nil), valid.candidates...)
			test.mutate(&config)
			if err := validateConfig(config); err == nil {
				t.Fatal("validateConfig() error = nil")
			}
		})
	}
}

func TestParseOptions(t *testing.T) {
	options, err := parseOptions([]string{"-seed=9", "-output=custom", "-baseline-prompt=prompt.txt"}, &bytes.Buffer{})
	if err != nil || options.seed != 9 || options.outputDir != "custom" || options.paths.baseline != "prompt.txt" {
		t.Fatalf("parseOptions() = %+v, %v", options, err)
	}
	defaults, err := parseOptions(nil, &bytes.Buffer{})
	if err != nil || defaults.seed != 2003 || defaults.outputDir != defaultOutputDir {
		t.Fatalf("default parseOptions() = %+v, %v", defaults, err)
	}
	for _, arguments := range [][]string{{"unexpected"}, {"-seed=bad"}} {
		if _, err := parseOptions(arguments, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseOptions(%v) error = nil", arguments)
		}
	}
}
