// Package storagewriter implements the StorageWriter GraphAgent node.
// Persists all review data to the database.
package storagewriter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/sanitize"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/storage"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Run is the StorageWriter GraphAgent node.
// Reads all task data from state and persists to the configured storage backend.
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() {
		gs[state.StateKeyNodeStorageWriterMs] = time.Since(start).Milliseconds()
	}()

	store := GetStorage(ctx)
	if store == nil {
		// Return state unchanged — no storage backend configured.
		return gs, nil
	}

	taskID, _ := gs[state.StateKeyTaskID].(string)
	findings, _ := gs[state.StateKeyFindings].([]types.Finding)
	warnings, _ := gs[state.StateKeyWarnings].([]types.Finding)
	permDecisions, _ := gs[state.StateKeyPermissionDecisions].([]types.PermissionDecision)
	sandboxResults, _ := gs[state.StateKeySandboxResults].([]types.SandboxResult)
	jsonPath, _ := gs[state.StateKeyJSONReportPath].(string)
	mdPath, _ := gs[state.StateKeyMDReportPath].(string)

	// 1. Update task as completed
	now := time.Now().UTC().Format(time.RFC3339)
	if err := store.UpdateTask(ctx, taskID, map[string]any{
		"status":       "completed",
		"completed_at": now,
	}); err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}

	// 2. Insert findings (both actual findings and warnings)
	// Sanitize sensitive data before persisting to DB (mandatory interception point)
	redactor := sanitize.NewRedactor(nil, "***REDACTED***")
	allFindings := append(findings, warnings...)
	findingRows := make([]storage.FindingRow, 0, len(allFindings))
	for _, f := range allFindings {
		f.Evidence = redactor.RedactFinding(f.Evidence)
		f.Recommendation = redactor.RedactFinding(f.Recommendation)
		findingRows = append(findingRows, toFindingRow(f))
	}
	if err := store.InsertFindings(ctx, findingRows); err != nil {
		return nil, fmt.Errorf("insert findings: %w", err)
	}

	// 3. Insert sandbox runs (sanitized)
	for _, r := range sandboxResults {
		r.Stdout = redactor.RedactSandboxOutput(r.Stdout)
		r.Stderr = redactor.RedactSandboxOutput(r.Stderr)
		if err := store.InsertSandboxRun(ctx, toSandboxRow(taskID, r)); err != nil {
			return nil, fmt.Errorf("insert sandbox run: %w", err)
		}
	}

	// 4. Insert permission decisions
	permRows := make([]storage.PermissionDecisionRow, 0, len(permDecisions))
	for _, d := range permDecisions {
		permRows = append(permRows, toPermRow(taskID, d))
	}
	if err := store.InsertPermissionDecisions(ctx, permRows); err != nil {
		return nil, fmt.Errorf("insert permission decisions: %w", err)
	}

	// 5. Insert artifacts (with size limit — Issue #2004 security boundary)
	maxArtifactBytes := int64(10 * 1024 * 1024) // default 10 MB
	if cfg, ok := gs[state.StateKeyExecutorConfig].(types.ExecutorConfig); ok && cfg.MaxArtifactMB > 0 {
		maxArtifactBytes = int64(cfg.MaxArtifactMB) * 1024 * 1024
	}
	if jsonPath != "" {
		if info, err := os.Stat(jsonPath); err == nil && info.Size() <= maxArtifactBytes {
			store.InsertArtifact(ctx, storage.ArtifactRow{
				ID: uuid.New().String(), TaskID: taskID, ArtifactType: "json_report",
				FilePath: jsonPath, SizeBytes: info.Size(), ContentHash: fileHash(jsonPath), CreatedAt: now,
			})
		}
	}
	if mdPath != "" {
		if info, err := os.Stat(mdPath); err == nil && info.Size() <= maxArtifactBytes {
			store.InsertArtifact(ctx, storage.ArtifactRow{
				ID: uuid.New().String(), TaskID: taskID, ArtifactType: "md_report",
				FilePath: mdPath, SizeBytes: info.Size(), ContentHash: fileHash(mdPath), CreatedAt: now,
			})
		}
	}

	// 6. Insert report summary
	sevDist := countSeverity(allFindings)
	catDist := countCategory(allFindings)
	sevJSON, _ := json.Marshal(sevDist)
	catJSON, _ := json.Marshal(catDist)
	if err := store.InsertReport(ctx, storage.ReportRow{
		ID:                    uuid.New().String(),
		TaskID:                taskID,
		FindingsCount:         len(findings),
		WarningsCount:         len(warnings),
		SeverityDistribution:  string(sevJSON),
		CategoryDistribution:  string(catJSON),
		JSONReportPath:        jsonPath,
		MDReportPath:          mdPath,
		Summary:               fmt.Sprintf("Reviewed %d findings (%d warnings).", len(findings), len(warnings)),
		CreatedAt:             now,
	}); err != nil {
		return nil, fmt.Errorf("insert report: %w", err)
	}

	// 7. Insert metrics
	metric := storage.MetricRow{
		ID:      uuid.New().String(),
		TaskID:  taskID,
		CreatedAt: now,
	}
	if v, ok := gs[state.StateKeyNodeDiffParserMs].(int64); ok {
		metric.DiffParseMs = v
		metric.TotalDurationMs += v
	}
	if v, ok := gs[state.StateKeyNodePermissionFilterMs].(int64); ok {
		metric.PermissionFilterMs = v
		metric.TotalDurationMs += v
	}
	if v, ok := gs[state.StateKeyNodeSandboxRunnerMs].(int64); ok {
		metric.SandboxTotalMs = v
		metric.TotalDurationMs += v
	}
	if v, ok := gs[state.StateKeyNodeRuleEngineMs].(int64); ok {
		metric.RuleEngineMs = v
		metric.TotalDurationMs += v
	}
	if v, ok := gs[state.StateKeyNodeLLMAnalyzerMs].(int64); ok {
		metric.LLMAnalyzerMs = v
		metric.TotalDurationMs += v
	}
	if v, ok := gs[state.StateKeyNodeDedupEngineMs].(int64); ok {
		metric.DedupMs = v
		metric.TotalDurationMs += v
	}
	if v, ok := gs[state.StateKeyNodeReportGeneratorMs].(int64); ok {
		metric.ReportGenMs = v
		metric.TotalDurationMs += v
	}
	if v, ok := gs[state.StateKeyNodeStorageWriterMs].(int64); ok {
		metric.StorageMs = v
		metric.TotalDurationMs += v
	}
	metric.ToolCallsCount = len(sandboxResults)
	metric.PermissionBlocksCount = countDenied(permDecisions)
	metric.FindingsCritical = sevDist["critical"]
	metric.FindingsHigh = sevDist["high"]
	metric.FindingsMedium = sevDist["medium"]
	metric.FindingsLow = sevDist["low"]
	metric.FindingsWarning = sevDist["warning"]

	if err := store.InsertMetric(ctx, metric); err != nil {
		return nil, fmt.Errorf("insert metric: %w", err)
	}

	// 8. Insert exceptions from sandbox failures
	var exceptions []storage.ExceptionRow
	excTypeSet := make(map[string]int)
	for _, r := range sandboxResults {
		if r.ErrorType != "" {
			excTypeSet[r.ErrorType]++
		}
	}
	for etype, count := range excTypeSet {
		exceptions = append(exceptions, storage.ExceptionRow{
			ID:         uuid.New().String(),
			TaskID:     taskID,
			ErrorType:  etype,
			ErrorCount: count,
			CreatedAt:  now,
		})
	}
	if len(exceptions) > 0 {
		if err := store.InsertExceptions(ctx, exceptions); err != nil {
			return nil, fmt.Errorf("insert exceptions: %w", err)
		}
	}

	gs[state.StateKeyStorageDone] = true
	return gs, nil
}

func toFindingRow(f types.Finding) storage.FindingRow {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	return storage.FindingRow{
		ID:             f.ID,
		TaskID:         f.TaskID,
		Severity:       f.Severity,
		Category:       f.Category,
		File:           f.File,
		Line:           f.Line,
		Title:          f.Title,
		Evidence:       f.Evidence,
		Recommendation: f.Recommendation,
		Confidence:     f.Confidence,
		Source:         f.Source,
		DecisionKind:   f.DecisionKind,
		RuleID:         f.RuleID,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
}

func toSandboxRow(taskID string, r types.SandboxResult) storage.SandboxRunRow {
	return storage.SandboxRunRow{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		ExecutorType: "local",
		CommandName:  r.Command,
		Command:      r.Command,
		ExitCode:     r.ExitCode,
		Stdout:       r.Stdout,
		Stderr:       r.Stderr,
		DurationMs:   r.DurationMs,
		TimedOut:     r.TimedOut,
		OutputTruncated: false,
		ErrorType:    r.ErrorType,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

func toPermRow(taskID string, d types.PermissionDecision) storage.PermissionDecisionRow {
	return storage.PermissionDecisionRow{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		Command:   d.Command,
		RiskLevel: d.RiskLevel,
		Decision:  d.Decision,
		Reason:    d.Reason,
		DecidedAt: d.DecidedAt.Format(time.RFC3339),
	}
}

func fileHash(path string) string {
	// Lazy: return empty for remote artifacts
	return ""
}

func countSeverity(findings []types.Finding) map[string]int {
	m := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "warning": 0}
	for _, f := range findings {
		m[f.Severity]++
	}
	return m
}

func countCategory(findings []types.Finding) map[string]int {
	m := make(map[string]int)
	for _, f := range findings {
		m[f.Category]++
	}
	return m
}

func countDenied(decisions []types.PermissionDecision) int {
	n := 0
	for _, d := range decisions {
		if d.Decision == "deny" {
			n++
		}
	}
	return n
}
