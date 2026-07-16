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
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regloop"
)

// gateConfig mirrors regloop.ReleaseGate in the on-disk configuration.
type gateConfig struct {
	MinTotalGain     float64  `json:"minTotalGain"`
	AllowNewHardFail bool     `json:"allowNewHardFail"`
	MaxRounds        int      `json:"maxRounds"`
	MaxModelCalls    int      `json:"maxModelCalls"`
	ProtectedCaseIDs []string `json:"protectedCaseIds"`
}

// loopConfig is the reproducible run configuration loaded from promptiter.json
// plus the baseline prompt source file.
type loopConfig struct {
	MaxRounds                  int        `json:"maxRounds"`
	MinScoreGain               float64    `json:"minScoreGain"`
	TargetScore                float64    `json:"targetScore"`
	MaxRoundsWithoutAcceptance int        `json:"maxRoundsWithoutAcceptance"`
	Gate                       gateConfig `json:"gate"`

	// BaselineInstruction is read from baseline.instruction.txt, not the JSON.
	BaselineInstruction string `json:"-"`
}

// loadLoopConfig reads promptiter.json and baseline.instruction.txt from the
// app data directory, making the whole run reproducible from files.
func loadLoopConfig(dataDir string) (*loopConfig, error) {
	cfgPath := filepath.Join(dataDir, appName, "promptiter.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("read promptiter config: %w", err)
	}
	cfg := &loopConfig{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse promptiter config: %w", err)
	}
	if cfg.MaxRounds <= 0 {
		return nil, fmt.Errorf("promptiter config: maxRounds must be > 0")
	}
	if cfg.MinScoreGain < 0 {
		return nil, fmt.Errorf("promptiter config: minScoreGain must be >= 0")
	}
	if cfg.TargetScore <= 0 {
		return nil, fmt.Errorf("promptiter config: targetScore must be > 0")
	}
	if cfg.MaxRoundsWithoutAcceptance <= 0 {
		return nil, fmt.Errorf("promptiter config: maxRoundsWithoutAcceptance must be > 0")
	}
	if cfg.Gate.MinTotalGain < 0 {
		return nil, fmt.Errorf("promptiter config: gate.minTotalGain must be >= 0")
	}
	// Budgets use 0 = disabled; a negative value is a misconfiguration, not a
	// silent disable.
	if cfg.Gate.MaxRounds < 0 {
		return nil, fmt.Errorf("promptiter config: gate.maxRounds must be >= 0 (0 disables)")
	}
	if cfg.Gate.MaxModelCalls < 0 {
		return nil, fmt.Errorf("promptiter config: gate.maxModelCalls must be >= 0 (0 disables)")
	}
	instrPath := filepath.Join(dataDir, appName, "baseline.instruction.txt")
	instr, err := os.ReadFile(instrPath)
	if err != nil {
		return nil, fmt.Errorf("read baseline instruction: %w", err)
	}
	cfg.BaselineInstruction = strings.TrimSpace(string(instr))
	if cfg.BaselineInstruction == "" {
		return nil, fmt.Errorf("baseline instruction is empty")
	}
	return cfg, nil
}

// releaseGate builds the default harness release gate from the loaded config.
// Scenarios may override it (see scenario.gateOverride).
func (c *loopConfig) releaseGate() regloop.ReleaseGate {
	return regloop.ReleaseGate{
		MinTotalGain:     c.Gate.MinTotalGain,
		AllowNewHardFail: c.Gate.AllowNewHardFail,
		ProtectedCaseIDs: c.Gate.ProtectedCaseIDs,
		MaxRounds:        c.Gate.MaxRounds,
		MaxModelCalls:    c.Gate.MaxModelCalls,
	}
}
