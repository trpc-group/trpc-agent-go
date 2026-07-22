//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package report builds, validates, and writes review reports.
package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	storemodel "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

// SchemaVersion is the public report contract version.
const (
	SchemaVersion = "1.0.0"
	maxJSONBytes  = 4 << 20
	maxMarkdown   = 4 << 20
)

// FindingSummary counts findings by review bucket.
type FindingSummary struct {
	Findings    int `json:"findings"`
	Warnings    int `json:"warnings"`
	HumanReview int `json:"needs_human_review"`
}

// GovernanceSummary keeps decisions and blocked execution counts together.
type GovernanceSummary struct {
	Blocked   int                   `json:"blocked"`
	Decisions []storemodel.Decision `json:"decisions"`
}

// Snapshot is the single source for JSON, Markdown, and database reports.
type Snapshot struct {
	SchemaVersion    string                  `json:"schema_version"`
	Task             storemodel.Task         `json:"task"`
	Input            storemodel.InputSummary `json:"input_summary"`
	Conclusion       string                  `json:"conclusion"`
	Summary          FindingSummary          `json:"findings_summary"`
	Findings         []reviewmodel.Finding   `json:"findings"`
	Warnings         []reviewmodel.Finding   `json:"warnings"`
	NeedsHumanReview []reviewmodel.Finding   `json:"needs_human_review"`
	SeverityCounts   map[string]int          `json:"severity_counts"`
	Governance       GovernanceSummary       `json:"governance"`
	SandboxRuns      []storemodel.SandboxRun `json:"sandbox_runs"`
	Metrics          storemodel.Metrics      `json:"metrics"`
	Artifacts        []storemodel.Artifact   `json:"artifacts"`
}

// Documents contains the two validated in-memory renderings.
type Documents struct {
	JSON     []byte
	Markdown []byte
}

// Build separates buckets and derives deterministic report summaries.
func Build(review storemodel.Review) Snapshot {
	result := Snapshot{SchemaVersion: SchemaVersion, Task: review.Task, Input: review.Input, Conclusion: review.Task.Conclusion, SeverityCounts: map[string]int{}, Governance: GovernanceSummary{Decisions: append([]storemodel.Decision{}, review.Decisions...)}, SandboxRuns: append([]storemodel.SandboxRun{}, review.Runs...), Metrics: review.Metrics, Artifacts: append([]storemodel.Artifact{}, review.Artifacts...), Findings: []reviewmodel.Finding{}, Warnings: []reviewmodel.Finding{}, NeedsHumanReview: []reviewmodel.Finding{}}
	result.Input.Packages = append([]string{}, review.Input.Packages...)
	if result.Metrics.SeverityCounts == nil {
		result.Metrics.SeverityCounts = map[string]int{}
	}
	if result.Metrics.ErrorTypeCounts == nil {
		result.Metrics.ErrorTypeCounts = map[string]int{}
	}
	for _, finding := range review.Findings {
		result.SeverityCounts[finding.Severity]++
		appendFinding(&result, finding)
	}
	for _, decision := range review.Decisions {
		if decision.Action != "allow" {
			result.Governance.Blocked++
		}
	}
	return result
}
func appendFinding(snapshot *Snapshot, finding reviewmodel.Finding) {
	switch finding.Bucket {
	case reviewmodel.BucketFindings:
		snapshot.Findings = append(snapshot.Findings, finding)
		snapshot.Summary.Findings++
	case reviewmodel.BucketWarnings:
		snapshot.Warnings = append(snapshot.Warnings, finding)
		snapshot.Summary.Warnings++
	default:
		snapshot.NeedsHumanReview = append(snapshot.NeedsHumanReview, finding)
		snapshot.Summary.HumanReview++
	}
}

// Render creates both bounded, redacted report documents.
func Render(snapshot Snapshot) (Documents, error) {
	var err error
	snapshot, err = sanitizeSnapshot(snapshot)
	if err != nil {
		return Documents{}, err
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return Documents{}, fmt.Errorf("render JSON report: %w", err)
	}
	markdown, err := renderMarkdown(snapshot)
	if err != nil {
		return Documents{}, err
	}
	if len(encoded) > maxJSONBytes || len(markdown) > maxMarkdown {
		return Documents{}, errors.New("rendered report exceeds size limit")
	}
	return Documents{JSON: encoded, Markdown: []byte(markdown)}, nil
}

// sanitizeSnapshot recursively redacts all JSON string values and object keys,
// so new report fields cannot silently bypass the persistence boundary.
func sanitizeSnapshot(snapshot Snapshot) (Snapshot, error) {
	return rewriteSnapshot(snapshot, "redaction", redact.String)
}

func rewriteSnapshot(snapshot Snapshot, operation string, transform func(string) string) (Snapshot, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal report for %s: %w", operation, err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return Snapshot{}, fmt.Errorf("decode report for %s: %w", operation, err)
	}
	encoded, err = json.Marshal(transformJSON(value, transform))
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal report after %s: %w", operation, err)
	}
	var result Snapshot
	if err := json.Unmarshal(encoded, &result); err != nil {
		return Snapshot{}, fmt.Errorf("decode report after %s: %w", operation, err)
	}
	return result, nil
}

func transformJSON(value any, transform func(string) string) any {
	switch typed := value.(type) {
	case string:
		return transform(typed)
	case []any:
		for index := range typed {
			typed[index] = transformJSON(typed[index], transform)
		}
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[transform(key)] = transformJSON(item, transform)
		}
		return result
	}
	return value
}

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, "`", `\`+"`", "*", `\*`, "_", `\_`, "~", `\~`, "{", `\{`, "}", `\}`,
	"[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "#", `\#`, "+", `\+`,
	"-", `\-`, "!", `\!`, "|", `\|`, ">", `\>`,
)
var reportRedactionTag = regexp.MustCompile(`\[REDACTED:[a-z_]+:[0-9a-f]{8}\]`)

func escapeMarkdown(value string) string {
	var tags []string
	value = reportRedactionTag.ReplaceAllStringFunc(value, func(tag string) string {
		tags = append(tags, tag)
		return fmt.Sprintf("\x00CRMDREDACTED%d\x00", len(tags)-1)
	})
	value = strings.Join(strings.Fields(value), " ")
	escaped := markdownEscaper.Replace(html.EscapeString(value))
	for index, tag := range tags {
		placeholder := fmt.Sprintf("\x00CRMDREDACTED%d\x00", index)
		escaped = strings.ReplaceAll(escaped, placeholder, `\`+tag[:len(tag)-1]+`\]`)
	}
	return escaped
}

func renderMarkdown(snapshot Snapshot) (string, error) {
	functions := template.FuncMap{"md": func(value any) string {
		return escapeMarkdown(fmt.Sprint(value))
	}}
	tmpl, err := template.New("review").Funcs(functions).Parse(markdownTemplate)
	if err != nil {
		return "", fmt.Errorf("parse Markdown report template: %w", err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, snapshot); err != nil {
		return "", fmt.Errorf("render Markdown report: %w", err)
	}
	return output.String(), nil
}

const markdownTemplate = `# Code Review Report

- Task: {{md .Task.ID}}
- Status: {{md .Task.Status}}
- Conclusion: {{md .Conclusion}}
- Input: {{md .Task.InputKind}} ({{md .Task.InputDigest}})

## Findings summary

- Findings: {{.Summary.Findings}}
- Warnings: {{.Summary.Warnings}}
- Needs human review: {{.Summary.HumanReview}}

### Severity distribution
{{range $name, $count := .SeverityCounts}}- {{md $name}}: {{$count}}
{{else}}No severity counts.
{{end}}

## Findings
{{range .Findings}}
### [{{md .Severity}}] {{md .Title}}

- Location: {{md .File}}:{{.Line}}
- Category / rule: {{md .Category}} / {{md .RuleID}}
- Evidence: {{md .Evidence}}
- Recommendation: {{md .Recommendation}}
- Confidence / source: {{.Confidence}} / {{md .Source}}
{{else}}
No actionable findings.
{{end}}

## Warnings
{{range .Warnings}}- {{md .File}}:{{.Line}} {{md .Title}} — {{md .Recommendation}}
{{else}}No warnings.
{{end}}

## Needs human review
{{range .NeedsHumanReview}}- {{md .File}}:{{.Line}} {{md .Title}} — {{md .Recommendation}}
{{else}}No manual review items.
{{end}}

## Governance

- Blocked decisions: {{.Governance.Blocked}}
{{range .Governance.Decisions}}- {{md .Stage}} / {{md .CheckID}}: {{md .Action}} ({{md .Reason}})
{{end}}

## Sandbox runs
{{range .SandboxRuns}}- {{md .CheckID}}: {{md .Status}}, {{.DurationMS}} ms, exit={{.ExitCode}}, timeout={{.TimedOut}}, truncated={{.OutputTruncated}}
{{else}}No sandbox runs.
{{end}}

## Monitoring

- Total duration: {{.Metrics.TotalDurationMS}} ms
- Sandbox duration: {{.Metrics.SandboxDurationMS}} ms
- Tool calls: {{.Metrics.ToolCalls}}
- Permission blocks: {{.Metrics.PermissionBlocks}}
- Finding count: {{.Metrics.FindingCount}}

### Error type distribution
{{range $name, $count := .Metrics.ErrorTypeCounts}}- {{md $name}}: {{$count}}
{{else}}No error types.
{{end}}

## Artifacts
{{range .Artifacts}}- {{md .Kind}}: {{md .Path}} ({{.SizeBytes}} bytes, sha256={{md .SHA256}})
{{else}}No artifacts.
{{end}}

`
const (
	jsonFileName     = "review_report.json"
	markdownFileName = "review_report.md"
	reportDirMode    = 0o700
	reportFileMode   = 0o600
)

// Written describes the exact external report copies.
type Written struct {
	JSONPath, JSONSHA256         string
	MarkdownPath, MarkdownSHA256 string
	JSONBytes, MarkdownBytes     int64
}

// Write publishes each report atomically without replacing an existing target.
func Write(outputDir string, documents Documents) (Written, error) {
	dir, err := prepareOutputDirectory(outputDir)
	if err != nil {
		return Written{}, err
	}
	jsonPath := filepath.Join(dir, jsonFileName)
	markdownPath := filepath.Join(dir, markdownFileName)
	if err := writeAtomic(jsonPath, documents.JSON); err != nil {
		return Written{}, err
	}
	if err := writeAtomic(markdownPath, documents.Markdown); err != nil {
		return Written{}, errors.Join(err, removeIfExists(jsonPath))
	}
	return Written{JSONPath: jsonPath, JSONSHA256: digest(documents.JSON), JSONBytes: int64(len(documents.JSON)), MarkdownPath: markdownPath, MarkdownSHA256: digest(documents.Markdown), MarkdownBytes: int64(len(documents.Markdown))}, nil
}
func prepareOutputDirectory(outputDir string) (string, error) {
	if outputDir == "" {
		return "", errors.New("report output directory is empty")
	}
	abs, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("resolve report output directory: %w", err)
	}
	if err := os.MkdirAll(abs, reportDirMode); err != nil {
		return "", fmt.Errorf("create report output directory: %w", err)
	}
	return abs, nil
}
func writeAtomic(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".review-*")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	temporary := file.Name()
	defer func() {
		_ = file.Close()
		_ = removeIfExists(temporary)
	}()
	if err := file.Chmod(reportFileMode); err != nil {
		return fmt.Errorf("set report permissions: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("write temporary report: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync temporary report: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary report: %w", err)
	}
	if err := os.Link(temporary, path); err != nil {
		return fmt.Errorf("publish report: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		return errors.Join(fmt.Errorf("remove temporary report: %w", err), removeIfExists(path))
	}
	return nil
}

// Remove deletes written copies during database compensation.
func (w Written) Remove() error {
	return errors.Join(removeIfExists(w.JSONPath), removeIfExists(w.MarkdownPath))
}

// StoreReport converts external metadata and canonical blobs for Finalize.
func (w Written) StoreReport(documents Documents, conclusion string) storemodel.Report {
	return storemodel.Report{SchemaVersion: SchemaVersion, Conclusion: conclusion, JSON: string(documents.JSON), Markdown: string(documents.Markdown), JSONPath: w.JSONPath, JSONSHA256: w.JSONSHA256, MarkdownPath: w.MarkdownPath, MarkdownSHA256: w.MarkdownSHA256}
}
func removeIfExists(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("remove report file: %w", err)
}
func digest(content []byte) string {
	value := sha256.Sum256(content)
	return fmt.Sprintf("%x", value[:])
}
