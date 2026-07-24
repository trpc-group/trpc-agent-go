// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// The tool_safety_guard command scans execution requests without executing
// them and writes a structured report and a secret-minimizing audit stream.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type sample struct {
	Name             string          `json:"name"`
	Description      string          `json:"description,omitempty"`
	Request          safety.Request  `json:"request"`
	ExpectedDecision safety.Decision `json:"expected_decision"`
}

func main() {
	policyPath := flag.String("policy", "tool_safety_policy.yaml", "input YAML or JSON safety policy")
	samplesPath := flag.String("samples", "samples.json", "input JSON execution requests")
	reportPath := flag.String("report", "tool_safety_report.json", "output JSON report")
	auditPath := flag.String("audit", "tool_safety_audit.jsonl", "output JSONL audit events")
	flag.Parse()

	if err := generate(*policyPath, *samplesPath, *reportPath, *auditPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(policyPath, samplesPath, reportPath, auditPath string) error {
	if filepath.Clean(reportPath) == filepath.Clean(auditPath) {
		return errors.New("report and audit output paths must differ")
	}
	policy, err := safety.LoadPolicy(policyPath)
	if err != nil {
		return fmt.Errorf("load safety policy: %w", errors.New("policy input is invalid"))
	}
	samples, err := loadSamples(samplesPath)
	if err != nil {
		return fmt.Errorf("load safety samples: %w", err)
	}

	var audit bytes.Buffer
	reports, err := scanSamples(
		samples, policy, safety.NewJSONLAuditSink(&audit), time.Now,
	)
	if err != nil {
		return err
	}
	if err := writeSecureOutput(reportPath, func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(reports); err != nil {
			return fmt.Errorf("encode safety report: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("write safety report: %w", err)
	}
	if err := writeSecureOutput(auditPath, func(writer io.Writer) error {
		if _, err := io.Copy(writer, &audit); err != nil {
			return fmt.Errorf("encode safety audit: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("write safety audit: %w", err)
	}
	return nil
}

func loadSamples(path string) ([]sample, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sample input: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var samples []sample
	if err := decoder.Decode(&samples); err != nil {
		return nil, errors.New("sample input is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("sample input has trailing data")
	}
	if len(samples) == 0 {
		return nil, errors.New("sample input is empty")
	}
	for index := range samples {
		if err := validateSample(samples[index]); err != nil {
			return nil, fmt.Errorf("sample %d is invalid: %w", index+1, err)
		}
	}
	return samples, nil
}

func validateSample(current sample) error {
	if strings.TrimSpace(current.Name) == "" {
		return errors.New("name is empty")
	}
	switch current.ExpectedDecision {
	case safety.DecisionAllow, safety.DecisionDeny, safety.DecisionAsk,
		safety.DecisionNeedsHumanReview:
	default:
		return errors.New("expected decision is unsupported")
	}
	switch current.Request.Backend {
	case safety.BackendWorkspaceExec, safety.BackendHostExec,
		safety.BackendCodeExec, safety.BackendUnknown:
	default:
		return errors.New("backend is unsupported")
	}
	if strings.TrimSpace(current.Request.ToolName) == "" {
		return errors.New("tool name is empty")
	}
	if strings.TrimSpace(current.Request.Command) == "" &&
		len(current.Request.Args) == 0 && len(current.Request.CodeBlocks) == 0 &&
		len(bytes.TrimSpace(current.Request.RawArguments)) == 0 {
		return errors.New("request has no executable input")
	}
	return nil
}

func scanSamples(
	samples []sample,
	policy safety.Policy,
	sink safety.AuditSink,
	now func() time.Time,
) ([]safety.Report, error) {
	guard, err := safety.NewGuard(policy)
	if err != nil {
		return nil, errors.New("create safety guard: policy is invalid")
	}
	if now == nil {
		now = time.Now
	}
	reports := make([]safety.Report, 0, len(samples))
	for index, current := range samples {
		report := guard.Scan(current.Request)
		if report.Decision != current.ExpectedDecision {
			return nil, fmt.Errorf(
				"sample decision does not match expected decision at index %d", index+1,
			)
		}
		if sink != nil {
			event := safety.AuditEvent{
				SchemaVersion:  report.SchemaVersion,
				Timestamp:      now().UTC(),
				ScanID:         report.ScanID,
				Stage:          "preflight",
				ToolName:       report.ToolName,
				Backend:        report.Backend,
				Decision:       report.Decision,
				RiskLevel:      report.RiskLevel,
				RuleID:         report.RuleID,
				DurationMillis: report.DurationMillis,
				Redacted:       report.Redacted,
				Intercepted:    report.Decision != safety.DecisionAllow,
			}
			if err := sink.Record(context.Background(), event); err != nil {
				return nil, fmt.Errorf("record safety audit: %w", err)
			}
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func writeSecureOutput(path string, encode func(io.Writer) error) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("output path is empty")
	}
	if err := rejectSymlinks(path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("output parent is not a directory")
	}
	file, err := os.CreateTemp(parent, ".tool-safety-*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	temporary := file.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set temporary output permissions: %w", err)
	}
	if err := writeBufferedAndClose(file, encode); err != nil {
		return err
	}
	if err := rejectSymlinks(path); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace output: %w", err)
	}
	committed = true
	return nil
}

func writeBufferedAndClose(
	writer io.WriteCloser,
	encode func(io.Writer) error,
) error {
	buffered := bufio.NewWriter(writer)
	encodeErr := encode(buffered)
	flushErr := buffered.Flush()
	closeErr := writer.Close()
	if err := errors.Join(encodeErr, flushErr, closeErr); err != nil {
		return fmt.Errorf("write buffered output: %w", err)
	}
	return nil
}

func rejectSymlinks(path string) error {
	for _, current := range []string{filepath.Dir(path), path} {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect output path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if current == filepath.Dir(path) && trustedSystemTempLink(current) {
				continue
			}
			return errors.New("output path contains a symbolic link")
		}
	}
	return nil
}

func trustedSystemTempLink(path string) bool {
	if filepath.Clean(path) != "/tmp" {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && filepath.Clean(resolved) == "/private/tmp"
}
