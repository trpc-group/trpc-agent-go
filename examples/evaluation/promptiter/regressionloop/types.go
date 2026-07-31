//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "time"

type evalSet struct {
	Name  string     `json:"name"`
	Cases []evalCase `json:"cases"`
}

type evalCase struct {
	ID              string   `json:"id"`
	Input           string   `json:"input"`
	Expected        string   `json:"expected"`
	Required        []string `json:"required"`
	Forbidden       []string `json:"forbidden,omitempty"`
	FailureCategory string   `json:"failureCategory"`
	Hard            bool     `json:"hard,omitempty"`
}

type metricConfig struct {
	PassScore float64 `json:"passScore"`
	FailScore float64 `json:"failScore"`
}

type optimizationConfig struct {
	Seed       int64             `json:"seed"`
	Model      modelConfig       `json:"model"`
	Gate       gateConfig        `json:"gate"`
	Candidates []candidateConfig `json:"candidates"`
}

type modelConfig struct {
	Name        string `json:"name"`
	Temperature int    `json:"temperature"`
}

type gateConfig struct {
	MinValidationGain  float64 `json:"minValidationGain"`
	ForbidNewFailures  bool    `json:"forbidNewFailures"`
	NoHardRegression   bool    `json:"noHardRegression"`
	MaxCalls           int     `json:"maxCalls"`
	MaxEstimatedTokens int     `json:"maxEstimatedTokens"`
}

type candidateConfig struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Reason string `json:"reason"`
}

type evaluationSummary struct {
	SetName      string               `json:"setName"`
	Score        float64              `json:"score"`
	Passed       int                  `json:"passed"`
	Failed       int                  `json:"failed"`
	Cases        []caseResult         `json:"cases"`
	Attributions []failureAttribution `json:"attributions,omitempty"`
	Cost         costSummary          `json:"cost"`
}

type caseResult struct {
	ID             string   `json:"id"`
	Score          float64  `json:"score"`
	Passed         bool     `json:"passed"`
	Hard           bool     `json:"hard,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	Category       string   `json:"category,omitempty"`
	Trace          []string `json:"trace"`
	ToolTrajectory []string `json:"toolTrajectory,omitempty"`
}

type failureAttribution struct {
	CaseID   string `json:"caseId"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
	Signal   string `json:"signal"`
}

type costSummary struct {
	Calls           int   `json:"calls"`
	EstimatedTokens int   `json:"estimatedTokens"`
	LatencyMillis   int64 `json:"latencyMillis"`
}

type caseDelta struct {
	CaseID         string  `json:"caseId"`
	BaselineScore  float64 `json:"baselineScore"`
	CandidateScore float64 `json:"candidateScore"`
	Delta          float64 `json:"delta"`
	Status         string  `json:"status"`
	Hard           bool    `json:"hard,omitempty"`
}

type gateDecision struct {
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons"`
}

type roundReport struct {
	Round              int               `json:"round"`
	CandidateID        string            `json:"candidateId"`
	Prompt             string            `json:"prompt"`
	PatchReason        string            `json:"patchReason"`
	Train              evaluationSummary `json:"train"`
	Validation         evaluationSummary `json:"validation"`
	ValidationDelta    []caseDelta       `json:"validationDelta"`
	AttributionSummary map[string]int    `json:"attributionSummary"`
	Cost               costSummary       `json:"cost"`
	Gate               gateDecision      `json:"gate"`
}

type baselineReport struct {
	Prompt     string            `json:"prompt"`
	Train      evaluationSummary `json:"train"`
	Validation evaluationSummary `json:"validation"`
}

type runMetadata struct {
	Seed       int64       `json:"seed"`
	Model      modelConfig `json:"model"`
	StartedAt  time.Time   `json:"startedAt"`
	DurationMS int64       `json:"durationMillis"`
	Mode       string      `json:"mode"`
	Promptiter string      `json:"promptiterIntegration"`
}

type optimizationReport struct {
	Metadata            runMetadata    `json:"metadata"`
	Baseline            baselineReport `json:"baseline"`
	Rounds              []roundReport  `json:"rounds"`
	AcceptedCandidateID string         `json:"acceptedCandidateId,omitempty"`
	AcceptedPrompt      string         `json:"acceptedPrompt"`
	Decision            string         `json:"decision"`
}
