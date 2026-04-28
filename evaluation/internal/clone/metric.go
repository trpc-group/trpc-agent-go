//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clone

import (
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	criterionrouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	criteriontext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
)

// CloneEvalMetric clones a metric configuration.
func CloneEvalMetric(src *metric.EvalMetric) (*metric.EvalMetric, error) {
	if src == nil {
		return nil, errNilInput("eval metric")
	}
	copied := *src
	clonedCriterion, err := cloneCriterion(src.Criterion)
	if err != nil {
		return nil, err
	}
	copied.Criterion = clonedCriterion
	return &copied, nil
}

func cloneCriterion(src *criterion.Criterion) (*criterion.Criterion, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	toolTrajectory, err := cloneToolTrajectoryCriterion(src.ToolTrajectory)
	if err != nil {
		return nil, err
	}
	copied.ToolTrajectory = toolTrajectory
	finalResponse, err := cloneFinalResponseCriterion(src.FinalResponse)
	if err != nil {
		return nil, err
	}
	copied.FinalResponse = finalResponse
	llmJudge, err := cloneLLMCriterion(src.LLMJudge)
	if err != nil {
		return nil, err
	}
	copied.LLMJudge = llmJudge
	return &copied, nil
}

func cloneToolTrajectoryCriterion(src *tooltrajectory.ToolTrajectoryCriterion) (*tooltrajectory.ToolTrajectoryCriterion, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	defaultStrategy, err := cloneToolTrajectoryStrategy(src.DefaultStrategy)
	if err != nil {
		return nil, err
	}
	copied.DefaultStrategy = defaultStrategy
	if src.ToolStrategy != nil {
		copied.ToolStrategy = make(map[string]*tooltrajectory.ToolTrajectoryStrategy, len(src.ToolStrategy))
		for name, strategy := range src.ToolStrategy {
			clonedStrategy, err := cloneToolTrajectoryStrategy(strategy)
			if err != nil {
				return nil, err
			}
			copied.ToolStrategy[name] = clonedStrategy
		}
	}
	return &copied, nil
}

func cloneToolTrajectoryStrategy(src *tooltrajectory.ToolTrajectoryStrategy) (*tooltrajectory.ToolTrajectoryStrategy, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	copied.Name = cloneTextCriterion(src.Name)
	arguments, err := cloneJSONCriterion(src.Arguments)
	if err != nil {
		return nil, err
	}
	copied.Arguments = arguments
	result, err := cloneJSONCriterion(src.Result)
	if err != nil {
		return nil, err
	}
	copied.Result = result
	return &copied, nil
}

func cloneFinalResponseCriterion(src *finalresponse.FinalResponseCriterion) (*finalresponse.FinalResponseCriterion, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	copied.Text = cloneTextCriterion(src.Text)
	jsonCriterion, err := cloneJSONCriterion(src.JSON)
	if err != nil {
		return nil, err
	}
	copied.JSON = jsonCriterion
	copied.Rouge = cloneRougeCriterion(src.Rouge)
	return &copied, nil
}

func cloneTextCriterion(src *criteriontext.TextCriterion) *criteriontext.TextCriterion {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}

func cloneJSONCriterion(src *criterionjson.JSONCriterion) (*criterionjson.JSONCriterion, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	if src.IgnoreTree != nil {
		ignoreTree, err := cloneAny(src.IgnoreTree)
		if err != nil {
			return nil, err
		}
		copied.IgnoreTree = ignoreTree.(map[string]any)
	}
	if src.OnlyTree != nil {
		onlyTree, err := cloneAny(src.OnlyTree)
		if err != nil {
			return nil, err
		}
		copied.OnlyTree = onlyTree.(map[string]any)
	}
	copied.NumberTolerance = cloneFloat64Ptr(src.NumberTolerance)
	return &copied, nil
}

func cloneRougeCriterion(src *criterionrouge.RougeCriterion) *criterionrouge.RougeCriterion {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}

func cloneLLMCriterion(src *criterionllm.LLMCriterion) (*criterionllm.LLMCriterion, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	copied.Rubrics = cloneRubrics(src.Rubrics)
	judgeModel, err := cloneJudgeModelOptions(src.JudgeModel)
	if err != nil {
		return nil, err
	}
	copied.JudgeModel = judgeModel
	copied.JudgeRunnerOptions = nil
	copied.Template = cloneJudgeTemplateOptions(src.Template)
	return &copied, nil
}

func cloneJudgeTemplateOptions(src *criterionllm.JudgeTemplateOptions) *criterionllm.JudgeTemplateOptions {
	if src == nil {
		return nil
	}
	copied := *src
	copied.VariableBindings = cloneTemplateVariableBindings(src.VariableBindings)
	return &copied
}

func cloneTemplateVariableBindings(src []*criterionllm.TemplateVariableBinding) []*criterionllm.TemplateVariableBinding {
	if src == nil {
		return nil
	}
	copied := make([]*criterionllm.TemplateVariableBinding, len(src))
	for i := range src {
		copied[i] = cloneTemplateVariableBinding(src[i])
	}
	return copied
}

func cloneTemplateVariableBinding(src *criterionllm.TemplateVariableBinding) *criterionllm.TemplateVariableBinding {
	if src == nil {
		return nil
	}
	copied := *src
	if src.Source != nil {
		source := *src.Source
		copied.Source = &source
	}
	return &copied
}

func cloneRubrics(src []*criterionllm.Rubric) []*criterionllm.Rubric {
	if src == nil {
		return nil
	}
	copied := make([]*criterionllm.Rubric, len(src))
	for i := range src {
		copied[i] = cloneRubric(src[i])
	}
	return copied
}

func cloneRubric(src *criterionllm.Rubric) *criterionllm.Rubric {
	if src == nil {
		return nil
	}
	copied := *src
	if src.Content != nil {
		content := *src.Content
		copied.Content = &content
	}
	return &copied
}

func cloneJudgeModelOptions(src *criterionllm.JudgeModelOptions) (*criterionllm.JudgeModelOptions, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	if src.ExtraFields != nil {
		extraFields, err := cloneAny(src.ExtraFields)
		if err != nil {
			return nil, err
		}
		copied.ExtraFields = extraFields.(map[string]any)
	}
	copied.NumSamples = cloneIntPtr(src.NumSamples)
	copied.Generation = cloneGenerationConfig(src.Generation)
	return &copied, nil
}
