//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func (s *DefaultScanner) scanUnknownArguments(req ScanRequest) []Finding {
	if s.policy.MaxCommandBytes > 0 && len(req.RawArguments) > s.policy.MaxCommandBytes {
		return []Finding{{
			RuleID:         "unknown.bounded_scan",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("raw arguments have %d bytes, exceeds max_command_bytes=%d", len(req.RawArguments), s.policy.MaxCommandBytes),
			Recommendation: "review large unknown tool arguments manually before execution",
		}}
	}
	rawFindings := s.scanTextForUnknownRisk(req, string(req.RawArguments))
	var decoded any
	if err := json.Unmarshal(req.RawArguments, &decoded); err != nil {
		return rawFindings
	}
	findings := append(rawFindings, s.scanDecodedUnknownArguments(req, decoded)...)
	return dedupeFindings(findings)
}

func (s *DefaultScanner) scanDecodedUnknownArguments(req ScanRequest, value any) []Finding {
	switch v := value.(type) {
	case string:
		return s.scanTextForUnknownRisk(req, v)
	case []any:
		var findings []Finding
		var argv []string
		flushArgv := func() {
			if len(argv) == 0 {
				return
			}
			findings = append(findings, s.scanTextForUnknownRisk(req, strings.Join(argv, " "))...)
			argv = nil
		}
		for _, item := range v {
			if text, ok := item.(string); ok {
				argv = append(argv, text)
				continue
			}
			flushArgv()
			findings = append(findings, s.scanDecodedUnknownArguments(req, item)...)
		}
		flushArgv()
		return findings
	case map[string]any:
		var findings []Finding
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			findings = append(findings, s.scanTextForUnknownRisk(req, key)...)
			item := v[key]
			findings = append(findings, s.scanDecodedUnknownArguments(req, item)...)
		}
		return findings
	default:
		return nil
	}
}

func (s *DefaultScanner) scanTextForUnknownRisk(req ScanRequest, text string) []Finding {
	findings := s.scanSecretText(text)
	lower := strings.ToLower(text)
	directTextInput := req.Command == ""
	if containsDownloaderOrURL(lower) {
		if req.Backend == BackendUnknown {
			findings = append(findings, Finding{
				RuleID:         "unknown.requires_review",
				RiskLevel:      RiskHigh,
				Decision:       DecisionNeedsHumanReview,
				Evidence:       "unknown tool contains downloader or URL-like content",
				Recommendation: "review unknown open-world tools before execution",
			})
		} else if directTextInput && (req.Backend == BackendHost || req.Backend == BackendCodeExec || req.Backend == BackendSandbox) {
			findings = append(findings, s.scanTextNetwork(text)...)
		}
	}
	if containsDangerousCommandText(lower) {
		switch req.Backend {
		case BackendUnknown:
			findings = append(findings, Finding{
				RuleID:         "unknown.dangerous_command",
				RiskLevel:      RiskCritical,
				Decision:       DecisionNeedsHumanReview,
				Evidence:       "unknown tool contains dangerous command-like content",
				Recommendation: "review unknown open-world tools before execution",
			})
		case BackendHost, BackendCodeExec, BackendSandbox:
			if !directTextInput {
				break
			}
			findings = append(findings, Finding{
				RuleID:         "command.dangerous_text",
				RiskLevel:      RiskCritical,
				Decision:       DecisionAsk,
				Evidence:       "text contains dangerous command-like content",
				Recommendation: "review generated stdin or code before execution",
			})
		}
	}
	if s.textMentionsDeniedPath(text) {
		if req.Backend == BackendUnknown {
			findings = append(findings, Finding{
				RuleID:         "unknown.sensitive_path",
				RiskLevel:      RiskCritical,
				Decision:       DecisionNeedsHumanReview,
				Evidence:       "<redacted>",
				Recommendation: "review unknown tools that reference credential or secret paths",
				Redacted:       true,
			})
		} else if directTextInput && (req.Backend == BackendHost || req.Backend == BackendCodeExec || req.Backend == BackendSandbox) {
			findings = append(findings, Finding{
				RuleID:         "path.sensitive_credentials",
				RiskLevel:      RiskCritical,
				Decision:       DecisionDeny,
				Evidence:       "<redacted>",
				Recommendation: "do not pass credential or secret paths through execution inputs",
				Redacted:       true,
			})
		}
	}
	return findings
}
