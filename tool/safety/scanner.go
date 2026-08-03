//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultScanner implements Scanner with the package policy rules.
type DefaultScanner struct {
	policy      Policy
	initialized bool
}

// NewDefaultScanner creates a scanner from policy.
func NewDefaultScanner(policy Policy) (*DefaultScanner, error) {
	p := policy.WithDefaults()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &DefaultScanner{policy: p, initialized: true}, nil
}

// MustDefaultScanner creates a scanner and panics on invalid policy.
func MustDefaultScanner(policy Policy) *DefaultScanner {
	scanner, err := NewDefaultScanner(policy)
	if err != nil {
		panic(err)
	}
	return scanner
}

// Scan scans one request.
func (s *DefaultScanner) Scan(ctx context.Context, req ScanRequest) (Report, error) {
	start := time.Now()
	if s == nil || !s.initialized {
		s = MustDefaultScanner(Policy{})
	}
	sizeResult := s.scanSizeResult(req)
	if req.Backend == "" {
		req.Backend = BackendUnknown
		findings := []Finding{{
			RuleID:         "backend.missing",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       "backend is required for safety scanning",
			Recommendation: "set an explicit supported safety backend before tool execution",
		}}
		reportReq := req
		if sizeResult.commandTooLarge {
			reportReq.Command = ""
		}
		report := buildReport(reportReq, findings, time.Since(start))
		return normalizeReportText(report, s.policy.DeniedPaths), nil
	}
	var findings []Finding
	if !req.Backend.Valid() {
		unsupported := req.Backend
		req.Backend = BackendUnknown
		findings = []Finding{{
			RuleID:         "backend.unsupported",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       fmt.Sprintf("unsupported backend %q", unsupported),
			Recommendation: "use a supported safety backend before tool execution",
		}}
	} else {
		findings = s.scanRequest(ctx, req, sizeResult)
	}
	reportReq := req
	if sizeResult.commandTooLarge {
		reportReq.Command = ""
	}
	report := buildReport(reportReq, findings, time.Since(start))
	return normalizeReportText(report, s.policy.DeniedPaths), nil
}

func (s *DefaultScanner) scanRequest(
	ctx context.Context,
	req ScanRequest,
	sizeResult scanSizeResult,
) []Finding {
	select {
	case <-ctx.Done():
		return []Finding{{
			RuleID:         "scanner.context_cancelled",
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "context cancelled before scan completed",
			Recommendation: "retry the scan with an active context",
		}}
	default:
	}
	findings := append([]Finding(nil), sizeResult.findings...)
	findings = append(findings, s.scanEnv(req.Env)...)
	findings = append(findings, s.scanCwd(req)...)
	findings = append(findings, s.scanCollectionPaths(req)...)
	findings = append(findings, s.scanInputPaths(req)...)
	if !sizeResult.editorTextTooLarge {
		findings = append(findings, s.scanEditorText(req)...)
	}
	findings = append(findings, s.scanOutputLimit(req)...)
	switch {
	case req.Command != "":
		if !sizeResult.commandTooLarge {
			findings = append(findings, s.scanCommand(req)...)
		}
	case len(req.Args) > 0:
		if !sizeResult.argsTooLarge {
			findings = append(findings, s.scanArgvRequest(req)...)
		}
	case req.Code != "":
		if !sizeResult.codeTooLarge {
			findings = append(findings, s.scanCode(req)...)
		}
	case len(req.RawArguments) > 0:
		if !sizeResult.rawArgumentsTooLarge {
			findings = append(findings, s.scanUnknownArguments(req)...)
		}
	}
	if req.Command != "" && sizeResult.commandTooLarge && !sizeResult.stdinTooLarge {
		findings = append(findings, s.scanCommandStdin(req)...)
	}
	if req.Stdin != "" && req.Command == "" {
		findings = append(findings, Finding{
			RuleID:         "stdin.session_fragment",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       "non-empty stdin without a complete submitted command",
			Recommendation: "scan complete submitted session lines or require review",
		})
	}
	if req.TimeoutSec > 0 && s.policy.MaxTimeoutSec > 0 &&
		req.TimeoutSec > s.policy.MaxTimeoutSec {
		decision := DecisionAsk
		if req.Backend == BackendHost {
			decision = DecisionDeny
		}
		findings = append(findings, Finding{
			RuleID:         "resource.long_running",
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       fmt.Sprintf("timeout_sec=%d exceeds max_timeout_sec=%d", req.TimeoutSec, s.policy.MaxTimeoutSec),
			Recommendation: "lower the timeout or require human approval",
		})
	}
	if req.Backend == BackendHost && req.TTY {
		findings = append(findings, Finding{
			RuleID:         "host.pty_session",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       "host command requested a PTY session",
			Recommendation: "review interactive host sessions before execution",
		})
	}
	if req.Backend == BackendHost && req.Background {
		findings = append(findings, Finding{
			RuleID:         "host.background_process",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       "host command requested background execution",
			Recommendation: "ensure the host process has cleanup and bounded lifetime",
		})
	}
	return findings
}

func (s *DefaultScanner) scanSize(req ScanRequest) []Finding {
	return s.scanSizeResult(req).findings
}

type scanSizeResult struct {
	findings             []Finding
	commandTooLarge      bool
	argsTooLarge         bool
	stdinTooLarge        bool
	rawArgumentsTooLarge bool
	codeTooLarge         bool
	editorTextTooLarge   bool
}

func (s *DefaultScanner) scanSizeResult(req ScanRequest) scanSizeResult {
	result := scanSizeResult{}
	var findings []Finding
	if s.policy.MaxCommandBytes > 0 && len(req.Command) > s.policy.MaxCommandBytes {
		result.commandTooLarge = true
		findings = append(findings, Finding{
			RuleID:         "command.too_large",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("command has %d bytes", len(req.Command)),
			Recommendation: "review long generated commands manually",
		})
	}
	if s.policy.MaxCommandBytes > 0 && len(req.Args) > 0 {
		argumentBytes, tooLarge := argvByteLength(
			req.Args, s.policy.MaxCommandBytes)
		if tooLarge {
			result.argsTooLarge = true
			findings = append(findings, Finding{
				RuleID:         "command.too_large",
				RiskLevel:      RiskHigh,
				Decision:       DecisionNeedsHumanReview,
				Evidence:       fmt.Sprintf("arguments have %d bytes, exceeds max_command_bytes=%d", argumentBytes, s.policy.MaxCommandBytes),
				Recommendation: "review long generated arguments manually",
			})
		}
	}
	if s.policy.MaxCommandBytes > 0 && len(req.Stdin) > s.policy.MaxCommandBytes {
		result.stdinTooLarge = true
		findings = append(findings, Finding{
			RuleID:         "command.too_large",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("stdin has %d bytes, exceeds max_command_bytes=%d", len(req.Stdin), s.policy.MaxCommandBytes),
			Recommendation: "review large stdin payloads manually",
		})
	}
	if s.policy.MaxCommandBytes > 0 && len(req.RawArguments) > s.policy.MaxCommandBytes {
		result.rawArgumentsTooLarge = true
		findings = append(findings, Finding{
			RuleID:         "unknown.bounded_scan",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("raw arguments have %d bytes, exceeds max_command_bytes=%d", len(req.RawArguments), s.policy.MaxCommandBytes),
			Recommendation: "review large unknown tool arguments manually before execution",
		})
	}
	if s.policy.MaxScriptBytes > 0 && len(req.Code) > s.policy.MaxScriptBytes {
		result.codeTooLarge = true
		findings = append(findings, Finding{
			RuleID:         "script.too_large",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("script has %d bytes", len(req.Code)),
			Recommendation: "review large scripts manually",
		})
	}
	if s.policy.MaxScriptBytes > 0 && len(req.EditorText) > s.policy.MaxScriptBytes {
		result.editorTextTooLarge = true
		findings = append(findings, Finding{
			RuleID:         "script.too_large",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("editor text has %d bytes, exceeds max_script_bytes=%d", len(req.EditorText), s.policy.MaxScriptBytes),
			Recommendation: "review large editor payloads manually",
		})
	}
	result.findings = findings
	return result
}

func argvByteLength(args []string, limit int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	total := 0
	for i, arg := range args {
		if i > 0 {
			if total == maxInt {
				return maxInt, true
			}
			total++
		}
		if len(arg) > maxInt-total {
			return maxInt, true
		}
		total += len(arg)
		if total > limit {
			return total, true
		}
	}
	return total, false
}

func (s *DefaultScanner) scanOutputLimit(req ScanRequest) []Finding {
	if s.policy.MaxOutputBytes <= 0 {
		return nil
	}
	value, ok := metadataInt64(req.Metadata, "max_result_size", "max_output_bytes", "max_output_size")
	if req.RequestedOutputBytes > 0 && (!ok || req.RequestedOutputBytes > value) {
		value = req.RequestedOutputBytes
		ok = true
	}
	if !ok || value <= s.policy.MaxOutputBytes {
		return nil
	}
	return []Finding{{
		RuleID:         "resource.output_limit",
		RiskLevel:      RiskHigh,
		Decision:       DecisionAsk,
		Evidence:       fmt.Sprintf("requested output size %d exceeds max_output_bytes=%d", value, s.policy.MaxOutputBytes),
		Recommendation: "lower the requested output size or require approval",
	}}
}

func buildReport(req ScanRequest, findings []Finding, dur time.Duration) Report {
	report := Report{
		ToolName:       req.ToolName,
		ToolCallID:     req.ToolCallID,
		Backend:        req.Backend,
		Command:        req.Command,
		Decision:       DecisionAllow,
		RiskLevel:      RiskLow,
		RuleID:         "evaluation.none",
		Evidence:       "no findings",
		Recommendation: "no action required",
		Blocked:        false,
		Findings:       findings,
		Duration:       dur,
		DurationMS:     dur.Milliseconds(),
	}
	for _, f := range findings {
		if findingRank(f) > reportRank(report) {
			report.Decision = f.Decision
			report.RiskLevel = f.RiskLevel
			report.RuleID = f.RuleID
			report.Evidence = f.Evidence
			report.Recommendation = f.Recommendation
		}
		report.Redacted = report.Redacted || f.Redacted
	}
	report.Blocked = report.Decision == DecisionDeny ||
		report.Decision == DecisionAsk ||
		report.Decision == DecisionNeedsHumanReview
	return report
}

func findingRank(f Finding) int {
	return decisionRank(f.Decision)*10 + riskRank(f.RiskLevel)
}

func reportRank(r Report) int {
	return decisionRank(r.Decision)*10 + riskRank(r.RiskLevel)
}

func decisionRank(d Decision) int {
	switch d {
	case DecisionDeny:
		return 4
	case DecisionNeedsHumanReview:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 0
	}
}

func riskRank(r RiskLevel) int {
	switch r {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

func dedupeFindings(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		key := strings.Join([]string{
			finding.RuleID,
			string(finding.RiskLevel),
			string(finding.Decision),
			finding.Evidence,
			finding.Recommendation,
			strconv.FormatBool(finding.Redacted),
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, finding)
	}
	return out
}

func metadataInt64(metadata map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case int:
			return int64(v), true
		case int8:
			return int64(v), true
		case int16:
			return int64(v), true
		case int32:
			return int64(v), true
		case int64:
			return v, true
		case uint:
			return metadataUnsignedInt64(uint64(v))
		case uint8:
			return metadataUnsignedInt64(uint64(v))
		case uint16:
			return metadataUnsignedInt64(uint64(v))
		case uint32:
			return metadataUnsignedInt64(uint64(v))
		case uint64:
			return metadataUnsignedInt64(v)
		case float64:
			return int64(v), true
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func metadataUnsignedInt64(v uint64) (int64, bool) {
	n, err := strconv.ParseInt(strconv.FormatUint(v, 10), 10, 64)
	return n, err == nil
}
