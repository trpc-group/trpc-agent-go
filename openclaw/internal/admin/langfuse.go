//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package admin

import "strings"

const langfuseTraceIDPlaceholder = "{{trace_id}}"

// LangfuseStatus describes the current Langfuse integration state that the
// admin surface exposes.
type LangfuseStatus struct {
	Enabled          bool   `json:"enabled"`
	Ready            bool   `json:"ready"`
	Error            string `json:"error,omitempty"`
	UIBaseURL        string `json:"ui_base_url,omitempty"`
	TraceURLTemplate string `json:"trace_url_template,omitempty"`
}

func normalizeLangfuseStatus(raw LangfuseStatus) LangfuseStatus {
	raw.Error = strings.TrimSpace(raw.Error)
	raw.UIBaseURL = strings.TrimRight(
		strings.TrimSpace(raw.UIBaseURL),
		"/",
	)
	raw.TraceURLTemplate = strings.TrimSpace(raw.TraceURLTemplate)
	return raw
}

func (s *Service) langfuseTraceURL(traceID string) string {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" || s == nil {
		return ""
	}
	status := normalizeLangfuseStatus(s.cfg.Langfuse)
	if !status.Enabled || !status.Ready ||
		status.TraceURLTemplate == "" {
		return ""
	}
	if !strings.Contains(
		status.TraceURLTemplate,
		langfuseTraceIDPlaceholder,
	) {
		return ""
	}
	return strings.ReplaceAll(
		status.TraceURLTemplate,
		langfuseTraceIDPlaceholder,
		traceID,
	)
}
