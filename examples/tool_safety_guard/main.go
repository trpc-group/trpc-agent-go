//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
	"runtime"
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
	canonicalReport, err := canonicalOutputPath(reportPath)
	if err != nil {
		return fmt.Errorf("validate safety report output: %w", err)
	}
	canonicalAudit, err := canonicalOutputPath(auditPath)
	if err != nil {
		return fmt.Errorf("validate safety audit output: %w", err)
	}
	if sameOutputTarget(canonicalReport, canonicalAudit) {
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
	if err := writeSecureOutput(canonicalReport, func(writer io.Writer) error {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(reports); err != nil {
			return fmt.Errorf("encode safety report: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("write safety report: %w", err)
	}
	if err := writeSecureOutput(canonicalAudit, func(writer io.Writer) error {
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
	commandUsable := strings.TrimSpace(current.Request.Command) != ""
	argsUsable := len(current.Request.Args) > 0
	if argsUsable && strings.TrimSpace(current.Request.Args[0]) == "" {
		return errors.New("args executable is empty")
	}
	if current.Request.CodeBlocks != nil && len(current.Request.CodeBlocks) == 0 {
		return errors.New("code blocks are empty")
	}
	codeUsable := len(current.Request.CodeBlocks) > 0
	for _, block := range current.Request.CodeBlocks {
		if strings.TrimSpace(block.Code) == "" {
			return errors.New("code block is empty")
		}
		if strings.TrimSpace(block.Language) == "" {
			return errors.New("code block language is empty")
		}
	}
	rawPresent := len(bytes.TrimSpace(current.Request.RawArguments)) > 0
	rawUsable := false
	if rawPresent {
		rawUsable = rawArgumentsHaveScannableInput(current.Request.RawArguments)
		if !rawUsable {
			return errors.New("raw arguments have no scannable execution input")
		}
	}
	if !commandUsable && !argsUsable && !codeUsable && !rawUsable {
		return errors.New("request has no executable input")
	}
	return nil
}

func rawArgumentsHaveScannableInput(raw json.RawMessage) bool {
	return decodeRawForValidation(raw, "", 0)
}

func decodeRawForValidation(raw []byte, parentKey string, depth int) bool {
	if depth > 32 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false
	}
	return rawValueHasScannableInput(value, parentKey, depth)
}

func rawValueHasScannableInput(value any, parentKey string, depth int) bool {
	if depth > 32 {
		return false
	}
	switch current := value.(type) {
	case map[string]any:
		if rawCodeBlockUsable(current) {
			return true
		}
		for key, child := range current {
			if rawValueHasScannableInput(child, key, depth+1) {
				return true
			}
		}
	case []any:
		if rawArgsKey(parentKey) {
			return len(current) > 0 && rawNonblankString(current[0])
		}
		for _, child := range current {
			if rawValueHasScannableInput(child, parentKey, depth+1) {
				return true
			}
		}
	case string:
		trimmed := strings.TrimSpace(current)
		if trimmed == "" {
			return false
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return decodeRawForValidation([]byte(trimmed), parentKey, depth+1)
		}
		return parentKey == "" || rawCommandKey(parentKey) ||
			rawNetworkKey(parentKey)
	}
	return false
}

func rawCodeBlockUsable(value map[string]any) bool {
	code, codeOK := value["code"].(string)
	language, languageOK := value["language"].(string)
	if !languageOK {
		language, languageOK = value["lang"].(string)
	}
	return codeOK && languageOK && strings.TrimSpace(code) != "" &&
		strings.TrimSpace(language) != ""
}

func rawNonblankString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func rawArgsKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "args" || key == "argv"
}

func rawCommandKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "command", "commands", "cmd", "script", "scripts", "shell":
		return true
	default:
		return rawArgsKey(key)
	}
}

func rawNetworkKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "url", "uri", "endpoint", "destination":
		return true
	default:
		return false
	}
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

func canonicalOutputPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("output path is empty")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", errors.New("resolve output path")
	}
	if err := rejectSymlinks(absolute); err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", fmt.Errorf("resolve output parent: %w", err)
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func sameOutputTarget(first, second string) bool {
	if first == second {
		return true
	}
	firstParent, firstParentErr := os.Stat(filepath.Dir(first))
	secondParent, secondParentErr := os.Stat(filepath.Dir(second))
	if firstParentErr == nil && secondParentErr == nil &&
		os.SameFile(firstParent, secondParent) &&
		strings.EqualFold(filepath.Base(first), filepath.Base(second)) {
		return true
	}
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
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
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return errors.New("resolve output path")
	}
	volume := filepath.VolumeName(absolute)
	root := volume + string(os.PathSeparator)
	current := root
	parts := strings.Split(strings.TrimPrefix(absolute, root), string(os.PathSeparator))
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("inspect output path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if trustedSystemTempLink(current) {
				continue
			}
			return errors.New("output path contains a symbolic link")
		}
	}
	return nil
}

func trustedSystemTempLink(path string) bool {
	if runtime.GOOS != "darwin" || filepath.Clean(path) != "/tmp" {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && filepath.Clean(resolved) == "/private/tmp"
}
