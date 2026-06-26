//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import "context"

const (
	checkNameAlways           = "always"
	checkNameCustom           = "custom"
	checkNameEventThreshold   = "event_threshold"
	checkNameTimeThreshold    = "time_threshold"
	checkNameTokenThreshold   = "token_threshold"
	checkNameContextThreshold = "context_threshold"

	metricCustom   = "custom"
	metricDuration = "duration"
	metricEvents   = "events"
	metricTokens   = "tokens"

	unitEvents       = "events"
	unitMilliseconds = "milliseconds"
	unitTokens       = "tokens"

	callModeStandalone    = "standalone"
	callModeCacheSafeFork = "cache_safe_fork"
)

// Report describes why summary generation was triggered and how the summary
// model call was accounted.
type Report struct {
	Trigger Trigger
	Call    Call
	Error   error
}

// Trigger describes the check result that caused summary generation.
type Trigger struct {
	Fired     bool
	Name      string
	Metric    string
	Value     int
	Threshold int
	Unit      string

	ContextWindow  int
	ThresholdRatio float64
	FilterKey      string
	Checks         []Check
}

// Check describes one summary trigger check.
type Check struct {
	Name      string
	Passed    bool
	Metric    string
	Value     int
	Threshold int
	Unit      string

	ContextWindow  int
	ThresholdRatio float64
}

// Call describes the summary model request and returned usage.
type Call struct {
	Mode                  string
	EstimatedPromptTokens int
	PromptTokens          int
	CachedTokens          int
}

// ReportHook observes summary trigger and model-call accounting.
type ReportHook func(context.Context, Report)

type reportContextKey struct{}

// ContextWithReport attaches a mutable summary report to ctx. Framework code
// uses this to carry trigger information from the check phase to the summary
// model call.
func ContextWithReport(ctx context.Context, report *Report) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, reportContextKey{}, report)
}

func reportFromContext(ctx context.Context) (*Report, bool) {
	if ctx == nil {
		return nil, false
	}
	report, ok := ctx.Value(reportContextKey{}).(*Report)
	return report, ok && report != nil
}

func cloneReport(report Report) Report {
	if len(report.Trigger.Checks) > 0 {
		report.Trigger.Checks = append([]Check(nil), report.Trigger.Checks...)
	}
	return report
}
