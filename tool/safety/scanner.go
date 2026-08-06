//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const maxScanInputBytes = 4 << 20

// Option configures a Scanner.
type Option func(*Scanner)

// WithAuditWriter writes one JSON object and newline per completed scan to
// writer. The Scanner serializes writes but does not close writer. NewScanner
// rejects a nil writer. Once configured, an audit write failure is returned to
// the caller so execution fails closed.
func WithAuditWriter(writer io.Writer) Option {
	return func(scanner *Scanner) {
		scanner.auditConfigured = true
		scanner.auditWriter = writer
	}
}

// Scanner evaluates tool execution inputs against one immutable Policy. The
// zero value uses an empty policy and has no persistent audit writer. Scanner
// is safe for concurrent use.
type Scanner struct {
	policy          Policy
	policyRevision  string
	auditConfigured bool
	auditWriter     io.Writer
	auditMu         sync.Mutex
}

// NewScanner validates policy and options and creates an opt-in safety scanner.
// It returns an error when an explicitly configured audit writer is nil.
func NewScanner(policy Policy, opts ...Option) (*Scanner, error) {
	normalized, err := normalizePolicy(policy)
	if err != nil {
		return nil, err
	}
	revision, err := policyRevision(normalized)
	if err != nil {
		return nil, err
	}
	scanner := &Scanner{policy: normalized, policyRevision: revision}
	for _, opt := range opts {
		if opt != nil {
			opt(scanner)
		}
	}
	if scanner.auditConfigured && nilWriter(scanner.auditWriter) {
		return nil, fmt.Errorf("nil audit writer")
	}
	return scanner, nil
}

func nilWriter(writer io.Writer) bool {
	if writer == nil {
		return true
	}
	value := reflect.ValueOf(writer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Scan evaluates input, emits OpenTelemetry span attributes, and writes one
// audit event when an audit writer is configured. Findings are policy results,
// not Go errors. Context cancellation and audit failures are returned as
// errors and must prevent the pending execution. A canceled scan is incomplete
// and does not emit a report, span attributes, or an audit event. A nil context
// is rejected.
func (s *Scanner) Scan(ctx context.Context, input ScanInput) (Report, error) {
	if s == nil {
		return Report{}, fmt.Errorf("nil safety scanner")
	}
	if ctx == nil {
		return Report{}, fmt.Errorf("nil context")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	started := time.Now()
	findings, sensitive, err := s.scan(ctx, input)
	if err != nil {
		return Report{}, err
	}
	identity, err := s.policyIdentity()
	if err != nil {
		return Report{}, err
	}
	report := buildReport(input, findings, sensitive, identity)
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	setSpanAttributes(ctx, report)
	if err := s.writeAudit(report, time.Since(started)); err != nil {
		return report, fmt.Errorf("write tool safety audit: %w", err)
	}
	return report, nil
}

func (s *Scanner) scan(
	ctx context.Context,
	input ScanInput,
) ([]Finding, bool, error) {
	findings := append([]Finding(nil), input.initialFindings...)
	if strings.TrimSpace(input.ToolName) == "" {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			"tool name is empty",
			"provide the model-visible tool name before scanning",
		))
	}
	if input.Backend == "" {
		input.Backend = inferInputBackend(input)
	}
	if !validBackend(input.Backend) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			fmt.Sprintf("unknown execution backend %q", input.Backend),
			"use unknown, generic, workspace, host, or codeexecutor",
		))
	}
	if inputSize(input) > maxScanInputBytes {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleResourceAbuse,
			fmt.Sprintf("scan input exceeds %d bytes", maxScanInputBytes),
			"split the request into smaller bounded executions",
		))
		return deduplicateFindings(findings), false, nil
	}

	sensitive := false
	secretFindings, foundSecret := scanSecrets(input)
	findings = append(findings, secretFindings...)
	sensitive = sensitive || foundSecret
	findings = append(findings, s.scanEnvironment(input.Environment)...)
	findings = append(findings, s.scanLimits(input)...)

	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(input.Command) != "" && !input.sessionWrite {
		findings = append(findings, s.scanCommandInput(input)...)
	} else if len(input.Arguments) > 0 && !input.sessionWrite {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			"structured command arguments require a non-empty executable",
			"provide the executable in command and literal argv values in arguments",
		))
	}
	for i, block := range input.CodeBlocks {
		blockFindings, err := s.scanCodeBlock(ctx, i, block, input)
		if err != nil {
			return nil, false, err
		}
		findings = append(findings, blockFindings...)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(input.extraValues) > 0 && !input.sessionWrite {
		findings = append(findings, scanSensitivePaths(input.extraValues)...)
		findings = append(findings, s.scanConfiguredPaths(
			input.extraValues,
			input.WorkingDirectory,
		)...)
		findings = append(findings, s.scanOpenWorldValues(
			input.Metadata,
			input.extraValues,
		)...)
	}
	if strings.TrimSpace(input.Command) == "" && len(input.CodeBlocks) == 0 {
		findings = append(findings, scanOpaqueMetadata(input.Metadata)...)
	}
	return deduplicateFindings(findings), sensitive, nil
}

func inferInputBackend(input ScanInput) Backend {
	if len(input.CodeBlocks) > 0 {
		return BackendCodeExecutor
	}
	if input.Command != "" {
		return BackendGeneric
	}
	return BackendUnknown
}

func validBackend(backend Backend) bool {
	switch backend {
	case BackendUnknown, BackendGeneric, BackendWorkspace, BackendHost,
		BackendCodeExecutor:
		return true
	default:
		return false
	}
}

func inputSize(input ScanInput) int {
	size := len(input.ToolName) + len(input.Command) + len(input.WorkingDirectory)
	for _, arg := range input.Arguments {
		size += len(arg)
	}
	for key, value := range input.Environment {
		size += len(key) + len(value)
	}
	for _, block := range input.CodeBlocks {
		size += len(block.Language) + len(block.Code)
	}
	for _, value := range input.extraValues {
		size += len(value)
	}
	return size
}

type reportIdentity struct {
	schemaVersion  string
	policyID       string
	policyRevision string
}

func (s *Scanner) policyIdentity() (reportIdentity, error) {
	policy := s.policy
	if policy.SchemaVersion == "" || policy.PolicyID == "" {
		var err error
		policy, err = normalizePolicy(policy)
		if err != nil {
			return reportIdentity{}, err
		}
	}
	revision := s.policyRevision
	if revision == "" {
		var err error
		revision, err = policyRevision(policy)
		if err != nil {
			return reportIdentity{}, err
		}
	}
	return reportIdentity{
		schemaVersion:  policy.SchemaVersion,
		policyID:       policy.PolicyID,
		policyRevision: revision,
	}, nil
}

func buildReport(
	input ScanInput,
	findings []Finding,
	sensitive bool,
	identity reportIdentity,
) Report {
	backend := input.Backend
	if backend == "" {
		backend = inferInputBackend(input)
	}
	if !validBackend(backend) {
		backend = BackendUnknown
	}
	toolName, toolNameChanged := sanitizeReportText(
		strings.TrimSpace(input.ToolName),
	)
	sensitive = sensitive || toolNameChanged
	raw := reportCommand(input)
	command, commandChanged := sanitizeReportText(raw)
	hash := sha256.Sum256([]byte(raw))

	if len(findings) == 0 {
		findings = []Finding{finding(
			DecisionAllow,
			RiskLevelLow,
			RuleAllow,
			"no tool safety policy violation detected",
			"execute with the configured runtime limits and isolation",
		)}
	}
	for i := range findings {
		var changed bool
		findings[i].Evidence, changed = sanitizeReportText(findings[i].Evidence)
		sensitive = sensitive || changed
		findings[i].Recommendation, changed = sanitizeReportText(
			findings[i].Recommendation,
		)
		sensitive = sensitive || changed
	}
	primary := selectPrimaryFinding(findings)
	return Report{
		SchemaVersion:  identity.schemaVersion,
		PolicyID:       identity.policyID,
		PolicyRevision: identity.policyRevision,
		Decision:       primary.Decision,
		RiskLevel:      highestRiskLevel(findings),
		RuleID:         primary.RuleID,
		Evidence:       primary.Evidence,
		Recommendation: primary.Recommendation,
		ToolName:       toolName,
		Command:        command,
		CommandSHA256:  hex.EncodeToString(hash[:]),
		Backend:        backend,
		Intercepted:    primary.Decision != DecisionAllow,
		Redacted:       sensitive || commandChanged,
		Findings:       findings,
	}
}

func highestRiskLevel(findings []Finding) RiskLevel {
	highest := RiskLevelLow
	for _, item := range findings {
		if riskRank(item.RiskLevel) > riskRank(highest) {
			highest = item.RiskLevel
		}
	}
	return highest
}

func reportCommand(input ScanInput) string {
	if strings.TrimSpace(input.Command) != "" {
		if len(input.Arguments) == 0 {
			return input.Command
		}
		return strings.TrimSpace(input.Command + " " + strings.Join(input.Arguments, " "))
	}
	var builder strings.Builder
	for i, block := range input.CodeBlocks {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString("[")
		builder.WriteString(block.Language)
		builder.WriteString("]\n")
		builder.WriteString(block.Code)
	}
	return builder.String()
}

func finding(
	decision Decision,
	risk RiskLevel,
	ruleID RuleID,
	evidence string,
	recommendation string,
) Finding {
	return Finding{
		Decision:       decision,
		RiskLevel:      risk,
		RuleID:         ruleID,
		Evidence:       evidence,
		Recommendation: recommendation,
	}
}

func deduplicateFindings(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]Finding, 0, len(findings))
	for _, item := range findings {
		key := string(item.RuleID) + "\x00" + item.Evidence
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func selectPrimaryFinding(findings []Finding) Finding {
	indexed := make([]struct {
		finding Finding
		index   int
	}, len(findings))
	for i, item := range findings {
		indexed[i].finding = item
		indexed[i].index = i
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		left, right := indexed[i], indexed[j]
		if decisionRank(left.finding.Decision) != decisionRank(right.finding.Decision) {
			return decisionRank(left.finding.Decision) > decisionRank(right.finding.Decision)
		}
		if riskRank(left.finding.RiskLevel) != riskRank(right.finding.RiskLevel) {
			return riskRank(left.finding.RiskLevel) > riskRank(right.finding.RiskLevel)
		}
		return left.index < right.index
	})
	return indexed[0].finding
}

func decisionRank(decision Decision) int {
	switch decision {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 0
	}
}

func riskRank(risk RiskLevel) int {
	switch risk {
	case RiskLevelCritical:
		return 4
	case RiskLevelHigh:
		return 3
	case RiskLevelMedium:
		return 2
	case RiskLevelLow:
		return 1
	default:
		return 0
	}
}

func setSpanAttributes(ctx context.Context, report Report) {
	if ctx == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("tool.safety.schema_version", report.SchemaVersion),
		attribute.String("tool.safety.policy_id", report.PolicyID),
		attribute.String("tool.safety.policy_revision", report.PolicyRevision),
		attribute.String("tool.safety.decision", string(report.Decision)),
		attribute.String("tool.safety.risk_level", string(report.RiskLevel)),
		attribute.String("tool.safety.rule_id", string(report.RuleID)),
		attribute.String("tool.safety.backend", string(report.Backend)),
	)
}
