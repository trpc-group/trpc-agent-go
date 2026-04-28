//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llm defines criteria for LLM-based judging.
package llm

import (
	"encoding/json"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// LLMCriterion configures an LLM judge for evaluation.
type LLMCriterion struct {
	Rubrics            []*Rubric             `json:"rubrics,omitempty"`
	JudgeModel         *JudgeModelOptions    `json:"judgeModel,omitempty"` // JudgeModel holds configuration for the judge model.
	JudgeRunnerOptions *JudgeRunnerOptions   `json:"-"`                    // JudgeRunnerOptions holds runtime judge runner configuration.
	Template           *JudgeTemplateOptions `json:"template,omitempty"`   // Template holds template evaluator configuration.
}

// Rubric defines a single judging rubric item for LLM-based evaluation.
type Rubric struct {
	ID          string         `json:"id,omitempty"`
	Content     *RubricContent `json:"content,omitempty"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type,omitempty"`
}

// RubricContent provides the judge-readable content for a rubric item.
type RubricContent struct {
	Text string `json:"text,omitempty"`
}

// JudgeRunnerOptions configures how judge responses are obtained via a runner at runtime.
type JudgeRunnerOptions struct {
	Runner runner.Runner // Runner is the runner to use for judge responses.
}

// JudgeModelOptions captures model and generation configuration for the judge.
type JudgeModelOptions struct {
	// ProviderName is the LLM provider name.
	ProviderName string `json:"providerName,omitempty"`
	// ModelName identifies the judge model.
	ModelName string `json:"modelName,omitempty"`
	// Variant selects the OpenAI-compatible variant when ProviderName is "openai".
	Variant string `json:"variant,omitempty"`
	// BaseURL is an optional custom endpoint.
	BaseURL string `json:"baseURL,omitempty"`
	// APIKey is used for the judge provider.
	APIKey string `json:"apiKey,omitempty"`
	// ExtraFields carries extra fields.
	ExtraFields map[string]any `json:"extraFields,omitempty"`
	// NumSamples sets how many judge samples to collect.
	NumSamples *int `json:"numSamples,omitempty"`
	// Generation holds generation parameters for the judge.
	Generation *model.GenerationConfig `json:"generationConfig,omitempty"`
}

// JudgeTemplateOptions configures the template-based LLM judge evaluator.
type JudgeTemplateOptions struct {
	Prompt                   string                     `json:"prompt,omitempty"`
	ResponseScorerName       string                     `json:"responseScorerName,omitempty"`
	VariableBindings         []*TemplateVariableBinding `json:"variableBindings,omitempty"`
	SampleAggregatorName     string                     `json:"sampleAggregatorName,omitempty"`
	InvocationAggregatorName string                     `json:"invocationAggregatorName,omitempty"`
}

// TemplateVariableBinding binds one template variable to one evaluation source.
type TemplateVariableBinding struct {
	TemplateVariable string                  `json:"templateVariable,omitempty"`
	Source           *TemplateVariableSource `json:"source,omitempty"`
}

// TemplateVariableSource identifies which evaluation artifact feeds a template variable.
type TemplateVariableSource struct {
	Scope TemplateVariableScope `json:"scope,omitempty"`
	Field TemplateVariableField `json:"field,omitempty"`
}

// TemplateVariableScope identifies the source object visible to template rendering.
type TemplateVariableScope string

const (
	// TemplateVariableScopeActual binds against the current actual invocation.
	TemplateVariableScopeActual TemplateVariableScope = "actual"
	// TemplateVariableScopeExpected binds against the current expected invocation.
	TemplateVariableScopeExpected TemplateVariableScope = "expected"
)

// TemplateVariableField identifies which field is extracted from the source object.
type TemplateVariableField string

const (
	// TemplateVariableFieldUserContent extracts the current user content text.
	TemplateVariableFieldUserContent TemplateVariableField = "userContent"
	// TemplateVariableFieldFinalResponse extracts the current final response text.
	TemplateVariableFieldFinalResponse TemplateVariableField = "finalResponse"
)

// MarshalJSON omits APIKey from JSON output while still allowing JSON input to populate it.
func (j JudgeModelOptions) MarshalJSON() ([]byte, error) {
	type judgeModelOptionsAlias JudgeModelOptions
	alias := judgeModelOptionsAlias(j)
	alias.APIKey = ""
	return json.Marshal(alias)
}

// UnmarshalJSON expands environment variables for ProviderName, ModelName, Variant, BaseURL and APIKey.
func (j *JudgeModelOptions) UnmarshalJSON(data []byte) error {
	type judgeModelOptionsAlias JudgeModelOptions
	var alias judgeModelOptionsAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	alias.ProviderName = os.ExpandEnv(alias.ProviderName)
	alias.ModelName = os.ExpandEnv(alias.ModelName)
	alias.Variant = os.ExpandEnv(alias.Variant)
	alias.BaseURL = os.ExpandEnv(alias.BaseURL)
	alias.APIKey = os.ExpandEnv(alias.APIKey)
	*j = JudgeModelOptions(alias)
	return nil
}

// New builds an LlmCriterion with judge model settings.
func New(providerName, modelName string, opt ...Option) *LLMCriterion {
	opts := newOptions(opt...)
	numSamples := opts.numSamples
	return &LLMCriterion{
		Rubrics: opts.rubrics,
		JudgeModel: &JudgeModelOptions{
			ProviderName: providerName,
			ModelName:    modelName,
			Variant:      opts.variant,
			BaseURL:      opts.baseURL,
			APIKey:       opts.apiKey,
			ExtraFields:  opts.extraFields,
			NumSamples:   &numSamples,
			Generation:   opts.generation,
		},
	}
}
