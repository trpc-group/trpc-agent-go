//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package langfuse provides Langfuse remote experiment integration for the evaluation server.
package langfuse

// remoteExperimentRequest is the webhook payload sent by Langfuse remote experiments.
type remoteExperimentRequest struct {
	ProjectID   string `json:"projectId"`
	DatasetID   string `json:"datasetId"`
	DatasetName string `json:"datasetName"`
	Payload     any    `json:"payload"`
}

// RemoteExperimentOptions contains the public payload accepted by remote experiment webhooks.
type RemoteExperimentOptions struct {
	RunName        string   `json:"runName"`
	RunDescription string   `json:"runDescription"`
	UserID         string   `json:"userId,omitempty"`
	TraceTags      []string `json:"traceTags,omitempty"`
}

type remoteExperimentPayload struct {
	RunName        *string   `json:"runName,omitempty"`
	RunDescription *string   `json:"runDescription,omitempty"`
	UserID         *string   `json:"userId,omitempty"`
	TraceTags      *[]string `json:"traceTags,omitempty"`
}

// remoteExperimentResponse summarizes a completed remote experiment run.
type remoteExperimentResponse struct {
	DatasetID        string              `json:"datasetId"`
	DatasetName      string              `json:"datasetName"`
	RunName          string              `json:"runName"`
	DatasetRunID     string              `json:"datasetRunId,omitempty"`
	ProcessedCases   int                 `json:"processedCases"`
	TraceCount       int                 `json:"traceCount"`
	ScoreCount       int                 `json:"scoreCount"`
	AggregateScores  map[string]float64  `json:"aggregateScores,omitempty"`
	AggregateReasons map[string]string   `json:"aggregateReasons,omitempty"`
	Cases            []remoteCaseSummary `json:"cases"`
}

// remoteCaseSummary summarizes one dataset item execution.
type remoteCaseSummary struct {
	CaseID        string             `json:"caseId"`
	DatasetItemID string             `json:"datasetItemId"`
	TraceID       string             `json:"traceId"`
	Status        string             `json:"status"`
	MetricScores  map[string]float64 `json:"metricScores"`
	MetricReasons map[string]string  `json:"metricReasons,omitempty"`
}

// dataset represents the Langfuse dataset fetched from the public API.
type dataset struct {
	ID          string         `json:"id"`
	ProjectID   string         `json:"projectId"`
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	Items       []*DatasetItem `json:"items"`
}

// DatasetItem represents one Langfuse dataset item.
type DatasetItem struct {
	ID             string `json:"id"`
	DatasetID      string `json:"datasetId"`
	Input          any    `json:"input"`
	ExpectedOutput any    `json:"expectedOutput"`
	Metadata       any    `json:"metadata"`
}
