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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	promptissue "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
)

type judgeOutput struct {
	Issues []promptissue.Issue `json:"issues,omitempty"`
}

func newIssueExtractor(jsonSchemaMetricName string, llmCriticMetricName string) promptissue.IssueExtractor {
	jsonSchemaMetricName = strings.TrimSpace(jsonSchemaMetricName)
	llmCriticMetricName = strings.TrimSpace(llmCriticMetricName)
	return func(evalSetID string, caseResult *evalresult.EvalCaseResult) []promptissue.IssueRecord {
		return extractIssuesFromCaseResult(evalSetID, caseResult, jsonSchemaMetricName, llmCriticMetricName)
	}
}

func extractIssuesFromCaseResult(evalSetID string, caseResult *evalresult.EvalCaseResult, jsonSchemaMetricName string, llmCriticMetricName string) []promptissue.IssueRecord {
	if caseResult == nil {
		return nil
	}
	out := make([]promptissue.IssueRecord, 0)
	if strings.TrimSpace(caseResult.ErrorMessage) != "" {
		out = append(out, promptissue.IssueRecord{
			Issue: promptissue.Issue{
				Severity: promptissue.SeverityP0,
				Key:      "case_failed",
				Summary:  strings.TrimSpace(caseResult.ErrorMessage),
				Action:   "该 case 在评测阶段报错（非内容质量问题）。优先检查 llm_critic judge 的提示词/输出 schema/模型调用是否返回可解析的 JSON，再检查推理链路与网络配置。",
			},
			EvalSetID:  evalSetID,
			EvalCaseID: caseResult.EvalID,
		})
	}
	for _, perInv := range caseResult.EvalMetricResultPerInvocation {
		if perInv == nil {
			continue
		}
		for _, metricResult := range perInv.EvalMetricResults {
			if metricResult == nil || metricResult.Details == nil {
				continue
			}
			switch strings.TrimSpace(metricResult.MetricName) {
			case jsonSchemaMetricName:
				if metricResult.Score >= metricResult.Threshold {
					continue
				}
				reason := strings.TrimSpace(metricResult.Details.Reason)
				if reason == "" {
					reason = "JSON schema validation failed."
				}
				out = append(out, promptissue.IssueRecord{
					Issue: promptissue.Issue{
						Severity: promptissue.SeverityP0,
						Key:      "json_schema_invalid",
						Summary:  reason,
						Action:   "在 output_contract 中强化“仅输出 JSON、仅包含 content、不得额外字段”，并明确 content 允许的格式与边界。",
					},
					EvalSetID:  evalSetID,
					EvalCaseID: caseResult.EvalID,
					MetricName: metricResult.MetricName,
				})
			case llmCriticMetricName:
				judgeJSON := strings.TrimSpace(metricResult.Details.Reason)
				if judgeJSON == "" {
					out = append(out, promptissue.IssueRecord{
						Issue: promptissue.Issue{
							Severity: promptissue.SeverityP0,
							Key:      "judge_empty_reason",
							Summary:  "Judge returned empty reason.",
							Action:   "检查 judge_critic 提示词，确保输出严格 JSON，并包含 issues[]。",
						},
						EvalSetID:  evalSetID,
						EvalCaseID: caseResult.EvalID,
						MetricName: metricResult.MetricName,
					})
					continue
				}
				var parsed judgeOutput
				if err := json.Unmarshal([]byte(judgeJSON), &parsed); err != nil {
					out = append(out, promptissue.IssueRecord{
						Issue: promptissue.Issue{
							Severity: promptissue.SeverityP0,
							Key:      "judge_output_invalid_json",
							Summary:  fmt.Sprintf("Failed to parse judge output JSON: %v", err),
							Action:   "在 judge_critic 中强调“只输出 JSON”，并减少歧义；必要时降低输出长度与增加示例。",
						},
						EvalSetID:  evalSetID,
						EvalCaseID: caseResult.EvalID,
						MetricName: metricResult.MetricName,
					})
					continue
				}
				for _, iss := range parsed.Issues {
					out = append(out, promptissue.IssueRecord{
						Issue:      normalizeIssue(iss),
						EvalSetID:  evalSetID,
						EvalCaseID: caseResult.EvalID,
						MetricName: metricResult.MetricName,
					})
				}
			}
		}
	}
	return out
}

func normalizeIssue(in promptissue.Issue) promptissue.Issue {
	if in.Severity != promptissue.SeverityP0 && in.Severity != promptissue.SeverityP1 {
		in.Severity = promptissue.SeverityP1
	}
	in.Key = strings.TrimSpace(in.Key)
	in.Summary = strings.TrimSpace(in.Summary)
	in.Action = strings.TrimSpace(in.Action)
	if in.Key == "" {
		in.Key = "unspecified"
	}
	if in.Summary == "" {
		in.Summary = "No summary."
	}
	if in.Action == "" {
		in.Action = "Update the prompt to address this issue."
	}
	return in
}
