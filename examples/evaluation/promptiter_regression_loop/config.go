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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

// Config is the root pipeline configuration loaded from promptiter.json.
type Config struct {
	// AppName is the evaluation app name and the data subdirectory that holds
	// evalset and metric files.
	AppName string `json:"appName"`
	// EvalSets names the train and validation eval sets.
	EvalSets EvalSetsConfig `json:"evalsets"`
	// PromptSource is the baseline prompt file, relative to the config file
	// directory unless absolute.
	PromptSource string `json:"promptSource"`
	// TargetSurfaces lists the prompt surfaces PromptIter is allowed to patch.
	TargetSurfaces []TargetSurface `json:"targetSurfaces"`
	// Engine configures the inner PromptIter engine run.
	Engine EngineConfig `json:"engine"`
	// Gate configures the outer safety gate (S5b).
	Gate GateConfig `json:"gate"`
	// Attribution configures the failure attribution rule engine (S2).
	Attribution AttributionConfig `json:"attribution,omitempty"`
	// Seed is recorded into the audit trail and report for reproducibility.
	Seed int64 `json:"seed"`

	// baseDir is the directory containing the config file, used to resolve
	// relative paths such as PromptSource.
	baseDir string
}

// EvalSetsConfig names the eval sets used by the pipeline.
type EvalSetsConfig struct {
	// Train is the eval set ID used for gradient generation.
	Train string `json:"train"`
	// Validation is the eval set ID used for regression and gating.
	Validation string `json:"validation"`
}

// TargetSurface identifies one optimizable surface on the candidate agent.
type TargetSurface struct {
	// Node is the agent or graph node that owns the surface.
	Node string `json:"node"`
	// Type is one of instruction, global_instruction, few_shot, tool, skill.
	Type string `json:"type"`
	// Name qualifies tool and skill surfaces with the tool or skill name.
	Name string `json:"name,omitempty"`
}

// surfaceTypeByName maps config type strings to agent structure surface types.
var surfaceTypeByName = map[string]astructure.SurfaceType{
	"instruction":        astructure.SurfaceTypeInstruction,
	"global_instruction": astructure.SurfaceTypeGlobalInstruction,
	"few_shot":           astructure.SurfaceTypeFewShot,
	"tool":               astructure.SurfaceTypeTool,
	"skill":              astructure.SurfaceTypeSkill,
}

// ID compiles this target surface into a PromptIter surface ID.
func (s TargetSurface) ID() (string, error) {
	surfaceType, ok := surfaceTypeByName[s.Type]
	if !ok {
		return "", fmt.Errorf("target surface type %q is not supported", s.Type)
	}
	if s.Name != "" {
		return astructure.SurfaceID(s.Node, surfaceType, s.Name), nil
	}
	return astructure.SurfaceID(s.Node, surfaceType), nil
}

// EngineConfig configures the inner PromptIter engine run.
type EngineConfig struct {
	// MaxRounds caps outer optimization rounds. 0 uses the default of 2.
	MaxRounds int `json:"maxRounds"`
	// MinScoreGain is the inner acceptance threshold between rounds.
	// Defaults to 0.01 when omitted.
	MinScoreGain *float64 `json:"minScoreGain,omitempty"`
	// MaxRoundsWithoutAcceptance stops the run after this many consecutive
	// rejected rounds. Defaults to 2 when omitted; explicit 0 disables it.
	MaxRoundsWithoutAcceptance *int `json:"maxRoundsWithoutAcceptance,omitempty"`
	// EvalCaseParallelism caps parallel case executions per eval set.
	// 0 uses the default of 1.
	EvalCaseParallelism int `json:"evalCaseParallelism"`
}

// GateConfig configures the outer safety gate (S5b). Zero values are the
// strict defaults: no new hard fails, no regressed cases.
type GateConfig struct {
	// MinValidationScoreGain is the minimum validation score delta to accept.
	MinValidationScoreGain float64 `json:"minValidationScoreGain"`
	// MaxNewHardFails caps validation cases that newly fail with a hard-fail
	// attribution category.
	MaxNewHardFails int `json:"maxNewHardFails"`
	// MaxRegressedCases caps validation cases that newly fail or lose score.
	MaxRegressedCases int `json:"maxRegressedCases"`
	// ProtectedCases lists eval case IDs that must not regress at all.
	ProtectedCases []string `json:"protectedCases,omitempty"`
	// HardFailCategories lists attribution categories counted as hard fails.
	// Defaults to tool_call_error, route_error, format_error.
	HardFailCategories []string `json:"hardFailCategories,omitempty"`
	// MaxModelCalls is the model call budget. 0 disables the budget rule.
	MaxModelCalls int `json:"maxModelCalls"`
	// MaxWallClock is the wall clock budget as a Go duration string such as
	// "3m". Empty disables the budget rule.
	MaxWallClock string `json:"maxWallClock,omitempty"`
	// RequireTrainNotWorse also rejects candidates whose train score drops.
	RequireTrainNotWorse bool `json:"requireTrainNotWorse"`
	// ScoreEpsilon suppresses floating point noise in per-case score
	// comparisons. 0 uses the default of 1e-6.
	ScoreEpsilon float64 `json:"epsilon,omitempty"`
}

// Epsilon returns the effective score comparison epsilon.
func (g GateConfig) Epsilon() float64 {
	if g.ScoreEpsilon > 0 {
		return g.ScoreEpsilon
	}
	return defaultScoreEpsilon
}

// MaxWallClockDuration parses the wall clock budget. It returns zero when the
// budget is disabled.
func (g GateConfig) MaxWallClockDuration() (time.Duration, error) {
	if g.MaxWallClock == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(g.MaxWallClock)
	if err != nil {
		return 0, fmt.Errorf("gate.maxWallClock %q is not a valid duration: %w", g.MaxWallClock, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("gate.maxWallClock must be positive, got %q", g.MaxWallClock)
	}
	return duration, nil
}

// AttributionConfig configures the failure attribution rule engine (S2).
type AttributionConfig struct {
	// MetricCategoryHints maps metric names to failure categories, overriding
	// structural classification for custom metric names.
	MetricCategoryHints map[string]string `json:"metricCategoryHints,omitempty"`
}

// Default values applied by applyDefaults.
const (
	defaultMaxRounds                  = 2
	defaultMinScoreGain               = 0.01
	defaultMaxRoundsWithoutAcceptance = 2
	defaultEvalCaseParallelism        = 1
)

func defaultHardFailCategories() []string {
	return []string{
		string(CauseToolCallError),
		string(CauseRouteError),
		string(CauseFormatError),
	}
}

// LoadConfig reads, defaults, and validates a promptiter.json file. Unknown
// fields are rejected so configuration typos surface immediately.
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	config := &Config{}
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("decode config %q: %w", path, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path %q: %w", path, err)
	}
	config.baseDir = filepath.Dir(absPath)
	config.applyDefaults()
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}
	return config, nil
}

// PromptSourcePath resolves the baseline prompt path against the config file
// directory.
func (c *Config) PromptSourcePath() string {
	if filepath.IsAbs(c.PromptSource) || c.baseDir == "" {
		return c.PromptSource
	}
	return filepath.Join(c.baseDir, c.PromptSource)
}

func (c *Config) applyDefaults() {
	if c.Engine.MaxRounds == 0 {
		c.Engine.MaxRounds = defaultMaxRounds
	}
	if c.Engine.MinScoreGain == nil {
		gain := defaultMinScoreGain
		c.Engine.MinScoreGain = &gain
	}
	if c.Engine.MaxRoundsWithoutAcceptance == nil {
		rounds := defaultMaxRoundsWithoutAcceptance
		c.Engine.MaxRoundsWithoutAcceptance = &rounds
	}
	if c.Engine.EvalCaseParallelism == 0 {
		c.Engine.EvalCaseParallelism = defaultEvalCaseParallelism
	}
	if len(c.Gate.HardFailCategories) == 0 {
		c.Gate.HardFailCategories = defaultHardFailCategories()
	}
}

// Validate checks every configuration field and joins all violations into one
// error so a broken config is fixable in a single pass.
func (c *Config) Validate() error {
	var problems []error
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}
	if strings.TrimSpace(c.AppName) == "" {
		report("appName must not be empty")
	}
	if strings.TrimSpace(c.EvalSets.Train) == "" {
		report("evalsets.train must not be empty")
	}
	if strings.TrimSpace(c.EvalSets.Validation) == "" {
		report("evalsets.validation must not be empty")
	}
	if strings.TrimSpace(c.PromptSource) == "" {
		report("promptSource must not be empty")
	}
	c.validateTargetSurfaces(report)
	c.validateEngine(report)
	c.validateGate(report)
	c.validateAttribution(report)
	return errors.Join(problems...)
}

func (c *Config) validateTargetSurfaces(report func(string, ...any)) {
	if len(c.TargetSurfaces) == 0 {
		report("targetSurfaces must list at least one surface")
		return
	}
	for i, surface := range c.TargetSurfaces {
		if strings.TrimSpace(surface.Node) == "" {
			report("targetSurfaces[%d].node must not be empty", i)
		}
		if _, ok := surfaceTypeByName[surface.Type]; !ok {
			report(
				"targetSurfaces[%d].type %q is not supported, expected one of instruction, global_instruction, few_shot, tool, skill",
				i, surface.Type,
			)
		}
	}
}

func (c *Config) validateEngine(report func(string, ...any)) {
	engine := c.Engine
	if engine.MaxRounds < 0 {
		report("engine.maxRounds must be non-negative, got %d", engine.MaxRounds)
	}
	if engine.MinScoreGain != nil && *engine.MinScoreGain < 0 {
		report("engine.minScoreGain must be non-negative, got %v", *engine.MinScoreGain)
	}
	if engine.MaxRoundsWithoutAcceptance != nil && *engine.MaxRoundsWithoutAcceptance < 0 {
		report("engine.maxRoundsWithoutAcceptance must be non-negative, got %d", *engine.MaxRoundsWithoutAcceptance)
	}
	if engine.EvalCaseParallelism < 0 {
		report("engine.evalCaseParallelism must be non-negative, got %d", engine.EvalCaseParallelism)
	}
}

func (c *Config) validateGate(report func(string, ...any)) {
	gate := c.Gate
	if gate.MinValidationScoreGain < 0 {
		report("gate.minValidationScoreGain must be non-negative, got %v", gate.MinValidationScoreGain)
	}
	if gate.MaxNewHardFails < 0 {
		report("gate.maxNewHardFails must be non-negative, got %d", gate.MaxNewHardFails)
	}
	if gate.MaxRegressedCases < 0 {
		report("gate.maxRegressedCases must be non-negative, got %d", gate.MaxRegressedCases)
	}
	if gate.MaxModelCalls < 0 {
		report("gate.maxModelCalls must be non-negative, got %d", gate.MaxModelCalls)
	}
	if gate.ScoreEpsilon < 0 {
		report("gate.epsilon must be non-negative, got %v", gate.ScoreEpsilon)
	}
	for i, caseID := range gate.ProtectedCases {
		if strings.TrimSpace(caseID) == "" {
			report("gate.protectedCases[%d] must not be empty", i)
		}
	}
	for i, category := range gate.HardFailCategories {
		if !IsKnownFailureCategory(category) {
			report(
				"gate.hardFailCategories[%d] %q is not a known failure category, expected one of %s",
				i, category, knownFailureCategoryList(),
			)
		}
	}
	if _, err := gate.MaxWallClockDuration(); err != nil {
		report("%v", err)
	}
}

func (c *Config) validateAttribution(report func(string, ...any)) {
	for metricName, category := range c.Attribution.MetricCategoryHints {
		if strings.TrimSpace(metricName) == "" {
			report("attribution.metricCategoryHints contains an empty metric name")
		}
		if !IsKnownFailureCategory(category) {
			report(
				"attribution.metricCategoryHints[%q] %q is not a known failure category, expected one of %s",
				metricName, category, knownFailureCategoryList(),
			)
		}
	}
}
