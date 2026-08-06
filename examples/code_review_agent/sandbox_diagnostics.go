//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxSandboxDiagnosticsPerRun = 50

	ruleSandboxGoVetDiagnostic       = "sandbox.go_vet_diagnostic"
	ruleSandboxStaticcheckDiagnostic = "sandbox.staticcheck_diagnostic"
)

var goDiagnosticPattern = regexp.MustCompile(`^(.*\.go):([0-9]+)(?::([0-9]+))?:\s*(.+)$`)

const invalidSandboxDiagnosticModule = "\x00"

type sandboxDiagnosticResult struct {
	Matches           []ruleMatch
	Parsed            int
	Mapped            int
	Overflow          bool
	ProtocolInvalid   bool
	UnaccountedOutput bool
}

type parsedSandboxDiagnostic struct {
	Path    string
	Line    int
	Column  int
	Message string
}

type sandboxDiagnosticMetadata struct {
	RuleID         string
	Severity       string
	Category       string
	Title          string
	Recommendation string
}

func parseSandboxDiagnostics(
	spec commandSpec,
	run sandboxRun,
	parsed parsedDiff,
) sandboxDiagnosticResult {
	// This compatibility entry point is intentionally retained for parser-only
	// unit tests. Governance uses parseSandboxDiagnosticsWithSnapshot so that
	// physical source locations are authenticated before mapping.
	return parseSandboxDiagnosticsInternal(spec, run, parsed, true, sandboxSourceTrust{
		MappingAllowed: true,
	})
}

func parseSandboxDiagnosticsWithSnapshot(
	spec commandSpec,
	run sandboxRun,
	parsed parsedDiff,
	snapshotRoot string,
) sandboxDiagnosticResult {
	trust := sandboxSourceTrust{MappingAllowed: true}
	if spec.Kind == commandCheckGoVet || spec.Kind == commandCheckStaticcheck {
		trust = inspectSandboxSourceTrust(snapshotRoot, spec.DiagnosticModules)
	}
	return parseSandboxDiagnosticsInternal(spec, run, parsed, false, trust)
}

func parseSandboxDiagnosticsInternal(
	spec commandSpec,
	run sandboxRun,
	parsed parsedDiff,
	legacyMapping bool,
	trust sandboxSourceTrust,
) sandboxDiagnosticResult {
	metadata, ok := diagnosticMetadataForCommand(spec.Kind)
	if !ok {
		return sandboxDiagnosticResult{}
	}
	result := sandboxDiagnosticResult{}
	seen := map[string]bool{}
	moduleAuthenticationRequired := len(spec.DiagnosticModules) > 0
	for _, output := range []string{run.Stdout, run.Stderr} {
		activeModule := ""
		for _, line := range strings.Split(output, "\n") {
			trimmedLine := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if trimmedLine == "" {
				continue
			}
			if module, ok := parseSandboxModuleBanner(spec, line); ok {
				activeModule = module
				if module == invalidSandboxDiagnosticModule {
					result.ProtocolInvalid = true
				}
				continue
			}
			diagnostic, ok := parseSandboxDiagnosticLine(line)
			if !ok {
				if sandboxPackageHeaderAllowed(
					line,
					activeModule,
					moduleAuthenticationRequired,
					trust.ModuleImports,
				) || (legacyMapping && isLegacySandboxPackageHeader(line)) {
					// The legacy branch is reachable only from parser-only tests;
					// governance always supplies snapshot-backed module metadata.
					continue
				}
				// Keep only the completeness bit; untrusted text is never emitted
				// as a structured diagnostic finding or finding evidence.
				result.UnaccountedOutput = true
				continue
			}
			key := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s",
				activeModule, diagnostic.Path, diagnostic.Line, diagnostic.Column, diagnostic.Message)
			if seen[key] {
				continue
			}
			seen[key] = true
			result.Parsed++
			if result.Parsed > maxSandboxDiagnosticsPerRun {
				result.Overflow = true
				continue
			}
			if moduleAuthenticationRequired &&
				(activeModule == "" || activeModule == invalidSandboxDiagnosticModule) {
				continue
			}
			if !legacyMapping && !trust.MappingAllowed {
				continue
			}
			repositoryPath, exactOnly, pathOK := repositoryDiagnosticPath(
				diagnostic.Path,
				activeModule,
			)
			if !pathOK {
				continue
			}
			file, ok := mapSandboxDiagnosticFile(repositoryPath, parsed, exactOnly)
			if !ok || !lineInNewHunk(file, diagnostic.Line) {
				continue
			}
			result.Mapped++
			evidence := diagnostic.Message
			if diagnostic.Column > 0 {
				evidence = fmt.Sprintf("column %d: %s", diagnostic.Column, diagnostic.Message)
			}
			result.Matches = append(result.Matches, ruleMatch{
				Severity:       metadata.Severity,
				Category:       metadata.Category,
				File:           file.NewPath,
				Line:           diagnostic.Line,
				Title:          metadata.Title,
				Evidence:       evidence,
				Recommendation: metadata.Recommendation,
				Confidence:     0.95,
				Source:         "sandbox",
				RuleID:         metadata.RuleID,
			})
		}
	}
	return result
}

func isLegacySandboxPackageHeader(line string) bool {
	_, _, ok := parseSandboxPackageHeader(line)
	return ok
}

func parseSandboxModuleBanner(spec commandSpec, line string) (string, bool) {
	mode := ""
	switch spec.Kind {
	case commandCheckGoVet:
		mode = "vet"
	case commandCheckStaticcheck:
		mode = "staticcheck"
	default:
		return "", false
	}
	line = strings.TrimSuffix(line, "\r")
	if !strings.HasPrefix(line, "==>") {
		return "", false
	}
	prefix := sandboxModuleBanner(mode, "")
	if !strings.HasPrefix(line, prefix) {
		return invalidSandboxDiagnosticModule, true
	}
	token := strings.TrimPrefix(line, prefix)
	if !isValidSandboxModuleToken(token) {
		return invalidSandboxDiagnosticModule, true
	}
	module, ok := spec.DiagnosticModules[token]
	if !ok {
		return invalidSandboxDiagnosticModule, true
	}
	if !isSafeSandboxModulePath(module) {
		return invalidSandboxDiagnosticModule, true
	}
	return module, true
}

func repositoryDiagnosticPath(
	diagnosticPath string,
	module string,
) (string, bool, bool) {
	diagnosticPath = normalizeDiagnosticPath(diagnosticPath)
	if module == invalidSandboxDiagnosticModule {
		return "", false, false
	}
	if strings.ContainsRune(diagnosticPath, '\x00') {
		return "", false, false
	}
	if isAbsoluteDiagnosticPath(diagnosticPath) {
		return diagnosticPath, false, true
	}
	if diagnosticPath == "." || diagnosticPath == ".." ||
		strings.HasPrefix(diagnosticPath, "../") {
		return "", false, false
	}
	if module == "" {
		return diagnosticPath, false, true
	}
	if module == "." {
		return diagnosticPath, true, true
	}
	return path.Join(module, diagnosticPath), true, true
}

func isAbsoluteDiagnosticPath(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		hasWindowsDrive(value)
}

func parseSandboxDiagnosticLine(line string) (parsedSandboxDiagnostic, bool) {
	matches := goDiagnosticPattern.FindStringSubmatch(strings.TrimSpace(line))
	if matches == nil {
		return parsedSandboxDiagnostic{}, false
	}
	lineNumber, err := strconv.Atoi(matches[2])
	if err != nil || lineNumber < 1 {
		return parsedSandboxDiagnostic{}, false
	}
	column := 0
	if matches[3] != "" {
		column, err = strconv.Atoi(matches[3])
		if err != nil || column < 1 {
			return parsedSandboxDiagnostic{}, false
		}
	}
	message := strings.TrimSpace(matches[4])
	if message == "" {
		return parsedSandboxDiagnostic{}, false
	}
	return parsedSandboxDiagnostic{
		Path:    normalizeDiagnosticPath(matches[1]),
		Line:    lineNumber,
		Column:  column,
		Message: message,
	}, true
}

func mapSandboxDiagnosticFile(
	diagnosticPath string,
	parsed parsedDiff,
	exactOnly bool,
) (changedFile, bool) {
	diagnosticPath = normalizeDiagnosticPath(diagnosticPath)
	var exact changedFile
	exactMatches := 0
	for _, file := range parsed.Files {
		if file.IsDeleted || strings.TrimSpace(file.NewPath) == "" {
			continue
		}
		if normalizeDiagnosticPath(file.NewPath) == diagnosticPath {
			exact = file
			exactMatches++
		}
	}
	if exactMatches > 0 {
		return exact, exactMatches == 1
	}
	if exactOnly {
		return changedFile{}, false
	}

	var match changedFile
	matches := 0
	for _, file := range parsed.Files {
		if file.IsDeleted || strings.TrimSpace(file.NewPath) == "" {
			continue
		}
		changedPath := normalizeDiagnosticPath(file.NewPath)
		if !componentSuffixMatch(diagnosticPath, changedPath) {
			continue
		}
		match = file
		matches++
	}
	return match, matches == 1
}

func normalizeDiagnosticPath(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	return value
}

func componentSuffixMatch(left string, right string) bool {
	if left == right {
		return true
	}
	return strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left)
}

func lineInNewHunk(file changedFile, line int) bool {
	for _, hunk := range file.Hunks {
		if hunk.NewCount > 0 && line >= hunk.NewStart && line < hunk.NewStart+hunk.NewCount {
			return true
		}
	}
	return false
}

func diagnosticMetadataForCommand(kind commandKind) (sandboxDiagnosticMetadata, bool) {
	switch kind {
	case commandCheckGoVet:
		return sandboxDiagnosticMetadata{
			RuleID:         ruleSandboxGoVetDiagnostic,
			Severity:       "medium",
			Category:       "correctness",
			Title:          "Go vet reported a changed-file diagnostic",
			Recommendation: "Address the go vet diagnostic and rerun go vet.",
		}, true
	case commandCheckStaticcheck:
		return sandboxDiagnosticMetadata{
			RuleID:         ruleSandboxStaticcheckDiagnostic,
			Severity:       "medium",
			Category:       "quality",
			Title:          "Staticcheck reported a changed-file diagnostic",
			Recommendation: "Address the staticcheck diagnostic and rerun staticcheck.",
		}, true
	default:
		return sandboxDiagnosticMetadata{}, false
	}
}

func sandboxDiagnosticsNeedGenericWarning(
	spec commandSpec,
	run sandboxRun,
	diagnostics sandboxDiagnosticResult,
) bool {
	if spec.Kind == commandCheckGoTest {
		return true
	}
	if run.Skipped || run.TimedOut || strings.TrimSpace(run.Error) != "" ||
		len(run.Warnings) > 0 || diagnostics.Overflow || diagnostics.ProtocolInvalid {
		return true
	}
	return diagnostics.UnaccountedOutput || diagnostics.Parsed == 0 ||
		diagnostics.Mapped != diagnostics.Parsed
}
