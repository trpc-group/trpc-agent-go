//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

const reportFileMode = os.FileMode(0o600)

type sample struct {
	Name           string               `json:"name"`
	Expected       safety.Decision      `json:"expected_decision"`
	RequiredRuleID string               `json:"required_rule_id"`
	ToolName       string               `json:"tool_name"`
	Kind           safety.ExecutionKind `json:"kind"`
	Operation      safety.Operation     `json:"operation"`
	Command        string               `json:"command"`
	Backend        safety.Backend       `json:"backend"`
	WorkingDir     string               `json:"working_dir"`
	Timeout        string               `json:"timeout"`
	MaxOutputBytes int64                `json:"max_output_bytes"`
	PTY            bool                 `json:"pty,omitempty"`
	Interactive    bool                 `json:"interactive,omitempty"`
	Env            map[string]string    `json:"env,omitempty"`
}

type namedReport struct {
	Name   string        `json:"name"`
	Report safety.Report `json:"report"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (err error) {
	policyPath := flag.String("policy", "", "path to YAML or JSON policy")
	samplesPath := flag.String("samples", "", "path to scan-only samples")
	reportPath := flag.String("report", "", "path for JSON reports")
	auditPath := flag.String("audit", "", "path for JSONL audit events")
	flag.Parse()
	if *policyPath == "" || *samplesPath == "" ||
		*reportPath == "" || *auditPath == "" {
		return errors.New("policy, samples, report, and audit paths are required")
	}
	auditor, err := safety.NewJSONLAuditor(*auditPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, auditor.Close()) }()
	guard, err := safety.NewGuardFromFile(
		*policyPath,
		safety.WithAuditor(auditor),
	)
	if err != nil {
		return err
	}
	samples, err := loadSamples(*samplesPath)
	if err != nil {
		return err
	}
	reports, err := scanSamples(context.Background(), guard, samples)
	if err != nil {
		return err
	}
	return writeReports(*reportPath, reports)
}

func loadSamples(path string) ([]sample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read samples: %w", err)
	}
	var samples []sample
	if err := json.Unmarshal(data, &samples); err != nil {
		return nil, fmt.Errorf("decode samples: %w", err)
	}
	if len(samples) == 0 {
		return nil, errors.New("samples are empty")
	}
	return samples, nil
}

func scanSamples(
	ctx context.Context,
	guard *safety.Guard,
	samples []sample,
) ([]namedReport, error) {
	reports := make([]namedReport, 0, len(samples))
	for _, current := range samples {
		input, err := current.scanInput()
		if err != nil {
			return nil, fmt.Errorf("sample %q: %w", current.Name, err)
		}
		report, err := guard.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan sample %q: %w", current.Name, err)
		}
		if err := current.validate(report); err != nil {
			return nil, err
		}
		reports = append(reports, namedReport{Name: current.Name, Report: report})
	}
	return reports, nil
}

func (current sample) scanInput() (safety.ScanInput, error) {
	timeout, err := time.ParseDuration(current.Timeout)
	if err != nil {
		return safety.ScanInput{}, fmt.Errorf("parse timeout: %w", err)
	}
	return safety.ScanInput{
		ToolName:    current.ToolName,
		Kind:        current.Kind,
		Operation:   current.Operation,
		Command:     current.Command,
		WorkingDir:  current.WorkingDir,
		Env:         current.Env,
		Backend:     current.Backend,
		Timeout:     timeout,
		PTY:         current.PTY,
		Interactive: current.Interactive,
	}, nil
}

func (current sample) validate(report safety.Report) error {
	if report.Decision != current.Expected {
		return fmt.Errorf(
			"sample %q: decision %q, want %q",
			current.Name,
			report.Decision,
			current.Expected,
		)
	}
	if report.RuleID == current.RequiredRuleID {
		return nil
	}
	for _, finding := range report.Findings {
		if finding.RuleID == current.RequiredRuleID {
			return nil
		}
	}
	return fmt.Errorf(
		"sample %q: required rule %q not found",
		current.Name,
		current.RequiredRuleID,
	)
}

func writeReports(path string, reports []namedReport) error {
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reports: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, reportFileMode); err != nil {
		return fmt.Errorf("write reports: %w", err)
	}
	return nil
}
