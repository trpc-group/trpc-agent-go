//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

const (
	defaultMarkdownTextLimit = 4096
	defaultPromptTextLimit   = 16384
	defaultEvidenceLimit     = 100
	persistedNestingLimit    = 16
)

var (
	jsonCredentialPattern = regexp.MustCompile(
		`("(?:\\.|[^"\\])*"\s*:\s*)"(?:\\.|[^"\\])*"`,
	)
	assignmentCredentialPattern = regexp.MustCompile(
		`(?i)\b([a-z][a-z0-9_-]*)(\s*[:=]\s*)(?:Bearer\s+)?(?:"(?:\\.|[^"\\])*"|'[^']*'|[^\s,;}\]]+)`,
	)
)

// RenderJSON renders the complete, sanitized machine-readable report.
func RenderJSON(report *Report) ([]byte, error) {
	sanitized, err := sanitizedReport(report)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal report: %w", err)
	}
	data = append(data, '\n')
	if err := validateRenderedJSON(data, sanitized); err != nil {
		return nil, err
	}
	return data, nil
}

// RenderMarkdown renders a complete, sanitized human-readable audit report.
func RenderMarkdown(report *Report) ([]byte, error) {
	sanitized, err := sanitizedReport(report)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	fmt.Fprintln(&out, "# Prompt Optimization Report")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "- Schema: `%s`\n", markdownInline(sanitized.SchemaVersion))
	fmt.Fprintf(&out, "- Report: `%s`\n", markdownInline(sanitized.ReportID))
	fmt.Fprintf(&out, "- Run: `%s`\n", markdownInline(sanitized.RunID))
	fmt.Fprintf(&out, "- Generated: `%s`\n", sanitized.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(&out, "- Pipeline status: `%s`\n", markdownInline(string(sanitized.Status)))
	fmt.Fprintf(&out, "- Stop reason: `%s`\n", markdownInline(string(sanitized.StopReason)))
	writeDecision(&out, "Final", sanitized.FinalDecision)
	writeErrors(&out, sanitized.Errors)

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Prompt State")
	fmt.Fprintln(&out)
	fmt.Fprintln(
		&out,
		"> Profile hashes identify the exact evaluated profile before report redaction and text bounding. "+
			"Displayed prompt/profile text is a sanitized audit representation and is not hash-reconstructive.",
	)
	writeProfile(&out, "Initial", &sanitized.InitialProfile)
	writeProfile(&out, "Search", &sanitized.SearchProfile)
	writeProfile(&out, "Released", &sanitized.ReleasedProfile)

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Baseline Evaluations")
	writeSnapshot(&out, "Training", sanitized.BaselineTrain)
	writeSnapshot(&out, "Held-out validation", sanitized.BaselineValidation)

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Candidates")
	if len(sanitized.Candidates) == 0 {
		fmt.Fprintln(&out)
		fmt.Fprintln(&out, "_No candidate was evaluable._")
	}
	for i := range sanitized.Candidates {
		writeCandidate(&out, &sanitized.Candidates[i])
	}

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Final Released Prompt")
	writeCodeBlock(&out, sanitized.ReleasedProfile.Prompt, "")

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Resolved Configuration")
	writeJSONBlock(&out, sanitized.ResolvedConfig)

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "### Input hashes")
	writeStringMap(&out, sanitized.InputHashes)

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "### Runtime")
	writeJSONBlock(&out, sanitized.Runtime)

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Cumulative Resources")
	writeResourceLedger(&out, sanitized.Resources)

	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## Artifacts")
	fmt.Fprintf(&out, "- JSON: `%s`\n", markdownInline(sanitized.Artifacts.JSON))
	fmt.Fprintf(&out, "- Markdown: `%s`\n", markdownInline(sanitized.Artifacts.Markdown))

	data := []byte(out.String())
	if err := validateRenderedMarkdown(data, sanitized); err != nil {
		return nil, err
	}
	return data, nil
}

// WriteArtifacts publishes Markdown first and canonical JSON last. Each file is
// fully written and closed in a temporary file in its destination directory
// before the first rename.
func WriteArtifacts(report *Report, jsonPath, markdownPath string) error {
	if report == nil {
		return errors.New("report is nil")
	}
	if strings.TrimSpace(jsonPath) == "" {
		return errors.New("JSON output path is empty")
	}
	if strings.TrimSpace(markdownPath) == "" {
		return errors.New("Markdown output path is empty")
	}
	jsonPath = filepath.Clean(jsonPath)
	markdownPath = filepath.Clean(markdownPath)
	if jsonPath == markdownPath {
		return errors.New("JSON and Markdown output paths must be different")
	}

	reportCopy := *report
	reportCopy.Artifacts = ArtifactReferences{JSON: jsonPath, Markdown: markdownPath}
	jsonData, err := RenderJSON(&reportCopy)
	if err != nil {
		return fmt.Errorf("render JSON report: %w", err)
	}
	markdownData, err := RenderMarkdown(&reportCopy)
	if err != nil {
		return fmt.Errorf("render Markdown report: %w", err)
	}
	if err := validateArtifactPair(jsonData, markdownData, &reportCopy); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(markdownPath), 0o700); err != nil {
		return fmt.Errorf("create Markdown output directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o700); err != nil {
		return fmt.Errorf("create JSON output directory: %w", err)
	}
	markdownTemp, err := writeClosedTemp(markdownPath, markdownData)
	if err != nil {
		return fmt.Errorf("prepare Markdown artifact: %w", err)
	}
	defer os.Remove(markdownTemp)
	jsonTemp, err := writeClosedTemp(jsonPath, jsonData)
	if err != nil {
		return fmt.Errorf("prepare JSON artifact: %w", err)
	}
	defer os.Remove(jsonTemp)

	if err := os.Rename(markdownTemp, markdownPath); err != nil {
		return fmt.Errorf("publish Markdown artifact: %w", err)
	}
	if err := os.Rename(jsonTemp, jsonPath); err != nil {
		return fmt.Errorf("publish canonical JSON artifact: %w", err)
	}
	return nil
}

// Write writes the canonical artifact names below outputDir.
func Write(report *Report, outputDir string) error {
	if strings.TrimSpace(outputDir) == "" {
		return errors.New("output directory is empty")
	}
	return WriteArtifacts(
		report,
		filepath.Join(outputDir, "optimization_report.json"),
		filepath.Join(outputDir, "optimization_report.md"),
	)
}

func sanitizedReport(report *Report) (*Report, error) {
	if err := validateReport(report); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshal report for sanitization: %w", err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("decode report for sanitization: %w", err)
	}
	evidenceLimit := report.ResolvedConfig.EvidenceLimit
	if evidenceLimit <= 0 || evidenceLimit > defaultEvidenceLimit {
		evidenceLimit = defaultEvidenceLimit
	}
	value = sanitizeJSONValue("", value, evidenceLimit, 0)
	sanitizedData, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal sanitized report: %w", err)
	}
	var sanitized Report
	if err := json.Unmarshal(sanitizedData, &sanitized); err != nil {
		return nil, fmt.Errorf("decode sanitized report: %w", err)
	}
	return &sanitized, nil
}

//nolint:gocyclo // Top-level report validation preserves distinct failure semantics.
func validateReport(report *Report) error {
	if report == nil {
		return errors.New("report is nil")
	}
	switch {
	case report.SchemaVersion != SchemaVersion:
		return fmt.Errorf("unsupported report schema version %q", report.SchemaVersion)
	case strings.TrimSpace(report.ReportID) == "":
		return errors.New("report id is empty")
	case strings.TrimSpace(report.RunID) == "":
		return errors.New("run id is empty")
	case report.GeneratedAt.IsZero():
		return errors.New("generated time is zero")
	case !validPipelineStatus(report.Status):
		return fmt.Errorf("invalid pipeline status %q", report.Status)
	case !validStopReason(report.StopReason):
		return fmt.Errorf("invalid stop reason %q", report.StopReason)
	case !validDecisionStatus(report.FinalDecision.Status):
		return fmt.Errorf("invalid final decision status %q", report.FinalDecision.Status)
	}
	if report.Status == PipelineSucceeded {
		for _, item := range []struct {
			label string
			role  ProfileRole
			value *ProfileRecord
		}{
			{label: "initial", role: ProfileInitial, value: &report.InitialProfile},
			{label: "search", role: ProfileSearch, value: &report.SearchProfile},
			{label: "released", role: ProfileReleased, value: &report.ReleasedProfile},
		} {
			if err := validateProfileRecord(item.label, item.role, item.value); err != nil {
				return err
			}
		}
		if err := validateCompletedSnapshot("baseline train", report.BaselineTrain); err != nil {
			return err
		}
		if err := validateCompletedSnapshot("baseline validation", report.BaselineValidation); err != nil {
			return err
		}
	}
	candidateIDs := make(map[string]struct{}, len(report.Candidates))
	for i := range report.Candidates {
		candidate := &report.Candidates[i]
		if strings.TrimSpace(candidate.ID) == "" {
			return fmt.Errorf("candidate %d id is empty", i+1)
		}
		if _, exists := candidateIDs[candidate.ID]; exists {
			return fmt.Errorf("duplicate candidate id %q", candidate.ID)
		}
		candidateIDs[candidate.ID] = struct{}{}
		if !validEvaluationStatus(candidate.Status) {
			return fmt.Errorf("candidate %q has invalid status %q", candidate.ID, candidate.Status)
		}
		if !validDecisionStatus(candidate.SearchDecision.Status) {
			return fmt.Errorf("candidate %q has invalid search decision %q", candidate.ID, candidate.SearchDecision.Status)
		}
		if !validDecisionStatus(candidate.ReleaseDecision.Status) {
			return fmt.Errorf("candidate %q has invalid release decision %q", candidate.ID, candidate.ReleaseDecision.Status)
		}
		if candidate.Profile != nil {
			if err := validateProfileRecord(
				fmt.Sprintf("candidate %q", candidate.ID),
				ProfileCandidate,
				candidate.Profile,
			); err != nil {
				return err
			}
		}
		if candidate.Status == EvaluationCompleted {
			if candidate.Profile == nil {
				return fmt.Errorf("candidate %q completed without a profile", candidate.ID)
			}
			if err := validateCompletedSnapshot("candidate "+candidate.ID+" train", candidate.Train); err != nil {
				return err
			}
			if err := validateCompletedSnapshot(
				"candidate "+candidate.ID+" validation",
				candidate.Validation,
			); err != nil {
				return err
			}
			if candidate.Deltas == nil {
				return fmt.Errorf("candidate %q completed without held-out deltas", candidate.ID)
			}
			if candidate.SearchDecision.Status == DecisionNotEvaluable ||
				candidate.ReleaseDecision.Status == DecisionNotEvaluable {
				if err := validateNotEvaluableTransition(candidate); err != nil {
					return err
				}
			}
		}
	}
	if err := validateReportResourceLedgers(report); err != nil {
		return err
	}
	if report.Status == PipelineSucceeded {
		if err := validateSuccessfulReport(report); err != nil {
			return err
		}
	}
	return nil
}

type reportProvenanceBinding struct {
	metricPolicyHash    string
	evaluatorConfigHash string
}

type reportValidationState struct {
	searchHash        string
	releasedHash      string
	initialValidation *EvaluationSnapshot
	searchValidation  *EvaluationSnapshot
	releaseValidation *EvaluationSnapshot
}

//nolint:gocyclo // Successful-report invariants are explicit audit-contract checks.
func validateSuccessfulReport(report *Report) error {
	binding, err := validateSuccessfulReportConfig(report)
	if err != nil {
		return err
	}
	if err := validateDecisionReasons("final", report.FinalDecision); err != nil {
		return err
	}
	if _, offset := report.GeneratedAt.Zone(); offset != 0 {
		return errors.New("generated time must use UTC")
	}
	targetSurfaceID := report.ResolvedConfig.PromptIter.TargetSurfaceIDs[0]
	structureID := report.InitialProfile.StructureID
	for _, item := range []struct {
		label   string
		profile *ProfileRecord
	}{
		{label: "initial", profile: &report.InitialProfile},
		{label: "search", profile: &report.SearchProfile},
		{label: "released", profile: &report.ReleasedProfile},
	} {
		if err := validateProfileConfigBinding(
			item.label,
			item.profile,
			structureID,
			targetSurfaceID,
		); err != nil {
			return err
		}
	}
	if err := validateSnapshotBinding(
		"baseline train",
		report.BaselineTrain,
		report.InitialProfile.Hash,
		report.ResolvedConfig.Train,
		"train",
		report.RunID+"/baseline_train",
		report.ResolvedConfig.Seed,
		binding,
		report.ResolvedConfig.Gate,
	); err != nil {
		return err
	}
	if err := validateSnapshotBinding(
		"baseline validation",
		report.BaselineValidation,
		report.InitialProfile.Hash,
		report.ResolvedConfig.Validation,
		"heldout_validation",
		report.RunID+"/baseline_validation",
		report.ResolvedConfig.Seed,
		binding,
		report.ResolvedConfig.Gate,
	); err != nil {
		return err
	}
	if report.InitialProfile.EvaluationRunID !=
		report.BaselineValidation.Provenance.RunID {
		return errors.New(
			"initial profile evaluation run id does not match baseline validation",
		)
	}

	state := reportValidationState{
		searchHash:        report.InitialProfile.Hash,
		releasedHash:      report.InitialProfile.Hash,
		initialValidation: report.BaselineValidation,
		searchValidation:  report.BaselineValidation,
		releaseValidation: report.BaselineValidation,
	}
	for i := range report.Candidates {
		candidate := &report.Candidates[i]
		if err := validateSuccessfulCandidate(
			report,
			candidate,
			i,
			&state,
			structureID,
			targetSurfaceID,
			binding,
		); err != nil {
			return err
		}
	}
	if report.SearchProfile.Hash != state.searchHash {
		return fmt.Errorf(
			"final search profile hash %q does not match transition lineage %q",
			report.SearchProfile.Hash,
			state.searchHash,
		)
	}
	if report.ReleasedProfile.Hash != state.releasedHash {
		return fmt.Errorf(
			"final released profile hash %q does not match transition lineage %q",
			report.ReleasedProfile.Hash,
			state.releasedHash,
		)
	}
	if state.searchValidation == nil ||
		report.SearchProfile.EvaluationRunID != state.searchValidation.Provenance.RunID {
		return errors.New(
			"final search profile evaluation run id does not match transition lineage",
		)
	}
	if state.releaseValidation == nil ||
		report.ReleasedProfile.EvaluationRunID != state.releaseValidation.Provenance.RunID {
		return errors.New(
			"final released profile evaluation run id does not match transition lineage",
		)
	}
	releasedCandidate := state.releasedHash != report.InitialProfile.Hash
	if releasedCandidate && report.FinalDecision.Status != DecisionAccepted {
		return errors.New(
			"final decision is not accepted despite a released candidate profile",
		)
	}
	if !releasedCandidate && report.FinalDecision.Status == DecisionAccepted {
		return errors.New(
			"final decision is accepted without a released candidate profile",
		)
	}
	return nil
}

//nolint:gocyclo // Resolved configuration is checked field-by-field against provenance.
func validateSuccessfulReportConfig(
	report *Report,
) (reportProvenanceBinding, error) {
	config := report.ResolvedConfig
	if err := validatePromptIterConfig(PromptIterConfig{
		SchemaVersion: SchemaVersion,
		Seed:          config.Seed,
		Policy:        config.PromptIter,
	}); err != nil {
		return reportProvenanceBinding{}, fmt.Errorf(
			"resolved PromptIter config: %w",
			err,
		)
	}
	if err := validateGatePolicy(config.Gate); err != nil {
		return reportProvenanceBinding{}, fmt.Errorf("resolved gate config: %w", err)
	}
	if err := validateOutputConfig(config.Output); err != nil {
		return reportProvenanceBinding{}, fmt.Errorf("resolved output config: %w", err)
	}
	switch {
	case config.EvidenceLimit <= 0:
		return reportProvenanceBinding{}, errors.New(
			"resolved evidence limit must be greater than zero",
		)
	case config.EvidenceLimit > defaultEvidenceLimit:
		return reportProvenanceBinding{}, fmt.Errorf(
			"resolved evidence limit must not exceed %d",
			defaultEvidenceLimit,
		)
	}
	for _, item := range []struct {
		label   string
		dataset DatasetSpec
	}{
		{label: "train", dataset: config.Train},
		{label: "validation", dataset: config.Validation},
	} {
		if err := validateResolvedDataset(item.label, item.dataset); err != nil {
			return reportProvenanceBinding{}, err
		}
	}
	if err := validateHeldoutExclusion(config.Train, config.Validation); err != nil {
		return reportProvenanceBinding{}, fmt.Errorf(
			"resolved train/validation split: %w",
			err,
		)
	}
	if err := verifyInventory(
		"resolved train/validation metric",
		config.Train.MetricNames,
		config.Validation.MetricNames,
	); err != nil {
		return reportProvenanceBinding{}, err
	}
	if config.Train.MetricsHash != config.Validation.MetricsHash {
		return reportProvenanceBinding{}, errors.New(
			"resolved train and validation metrics hashes differ",
		)
	}
	metricNames := make(map[string]struct{}, len(config.Train.MetricNames))
	for _, name := range config.Train.MetricNames {
		metricNames[name] = struct{}{}
	}
	if _, ok := metricNames[config.Gate.PrimaryMetric]; !ok {
		return reportProvenanceBinding{}, fmt.Errorf(
			"resolved primary metric %q is absent from the metric inventory",
			config.Gate.PrimaryMetric,
		)
	}
	if len(config.Gate.MetricDirections) != len(metricNames) {
		return reportProvenanceBinding{}, fmt.Errorf(
			"resolved metric direction inventory has %d entries, want %d",
			len(config.Gate.MetricDirections),
			len(metricNames),
		)
	}
	for name := range config.Gate.MetricDirections {
		if _, ok := metricNames[name]; !ok {
			return reportProvenanceBinding{}, fmt.Errorf(
				"resolved metric direction has unexpected metric %q",
				name,
			)
		}
	}
	if err := validateInternalValidation(
		config.PromptIter,
		config.Train,
		config.Validation,
	); err != nil {
		return reportProvenanceBinding{}, fmt.Errorf(
			"resolved PromptIter validation split: %w",
			err,
		)
	}
	validationCases := make(map[string]struct{}, len(config.Validation.CaseIDs))
	for _, caseID := range config.Validation.CaseIDs {
		validationCases[caseID] = struct{}{}
	}
	for _, item := range []struct {
		label   string
		caseIDs []string
	}{
		{label: "critical case", caseIDs: config.CriticalCaseIDs},
		{label: "hard failure case", caseIDs: config.HardFailureCaseIDs},
	} {
		if err := validateUniqueNonempty(item.label, item.caseIDs); err != nil {
			return reportProvenanceBinding{}, err
		}
		for _, caseID := range item.caseIDs {
			if _, ok := validationCases[caseID]; !ok {
				return reportProvenanceBinding{}, fmt.Errorf(
					"resolved %s %q is absent from held-out validation",
					item.label,
					caseID,
				)
			}
		}
	}

	requiredInputHashes := []string{
		"trainEvalSet",
		"validationEvalSet",
		"metrics",
		"baselinePrompt",
		"promptIterConfig",
		"regressionConfig",
	}
	if len(report.InputHashes) != len(requiredInputHashes) {
		return reportProvenanceBinding{}, fmt.Errorf(
			"input hash inventory has %d entries, want %d",
			len(report.InputHashes),
			len(requiredInputHashes),
		)
	}
	for _, name := range requiredInputHashes {
		if strings.TrimSpace(report.InputHashes[name]) == "" {
			return reportProvenanceBinding{}, fmt.Errorf(
				"input hash %q is empty",
				name,
			)
		}
	}
	switch {
	case report.InputHashes["trainEvalSet"] != config.Train.EvalSetHash:
		return reportProvenanceBinding{}, errors.New(
			"train eval-set input hash does not match resolved train dataset",
		)
	case report.InputHashes["validationEvalSet"] != config.Validation.EvalSetHash:
		return reportProvenanceBinding{}, errors.New(
			"validation eval-set input hash does not match resolved validation dataset",
		)
	case report.InputHashes["metrics"] != config.Train.MetricsHash:
		return reportProvenanceBinding{}, errors.New(
			"metrics input hash does not match resolved dataset metrics",
		)
	}
	if strings.TrimSpace(report.Runtime.Engine) == "" {
		return reportProvenanceBinding{}, errors.New("runtime engine is empty")
	}
	if report.Runtime.Seed != config.Seed {
		return reportProvenanceBinding{}, fmt.Errorf(
			"runtime seed %d does not match resolved seed %d",
			report.Runtime.Seed,
			config.Seed,
		)
	}
	gateJSON, err := json.Marshal(config.Gate)
	if err != nil {
		return reportProvenanceBinding{}, fmt.Errorf(
			"marshal resolved metric gate policy: %w",
			err,
		)
	}
	metricPolicyHash := hashStrings(
		"native-metric-policy-v1",
		report.InputHashes["metrics"],
		string(gateJSON),
	)
	runtimeHash, err := RuntimeConfigFingerprint(report.Runtime)
	if err != nil {
		return reportProvenanceBinding{}, err
	}
	evaluatorConfigHash := hashStrings(
		"runtime-bound-evaluator-v1",
		config.Train.EvalSetHash,
		config.Validation.EvalSetHash,
		metricPolicyHash,
		runtimeHash,
	)
	return reportProvenanceBinding{
		metricPolicyHash:    metricPolicyHash,
		evaluatorConfigHash: evaluatorConfigHash,
	}, nil
}

func validateResolvedDataset(label string, dataset DatasetSpec) error {
	switch {
	case strings.TrimSpace(dataset.EvalSetID) == "":
		return fmt.Errorf("resolved %s eval set id is empty", label)
	case strings.TrimSpace(dataset.EvalSetHash) == "":
		return fmt.Errorf("resolved %s eval set hash is empty", label)
	case strings.TrimSpace(dataset.MetricsHash) == "":
		return fmt.Errorf("resolved %s metrics hash is empty", label)
	case len(dataset.CaseIDs) == 0:
		return fmt.Errorf("resolved %s case inventory is empty", label)
	case len(dataset.MetricNames) == 0:
		return fmt.Errorf("resolved %s metric inventory is empty", label)
	}
	if err := validateUniqueNonempty(label+" case", dataset.CaseIDs); err != nil {
		return err
	}
	if err := validateUniqueNonempty(label+" metric", dataset.MetricNames); err != nil {
		return err
	}
	if len(dataset.NormalizedInputHashes) != len(dataset.CaseIDs) {
		return fmt.Errorf(
			"resolved %s normalized input hash inventory has %d entries, want %d",
			label,
			len(dataset.NormalizedInputHashes),
			len(dataset.CaseIDs),
		)
	}
	hashOwners := make(map[string]string, len(dataset.CaseIDs))
	for _, caseID := range dataset.CaseIDs {
		inputHash := strings.TrimSpace(dataset.NormalizedInputHashes[caseID])
		if inputHash == "" {
			return fmt.Errorf(
				"resolved %s normalized input hash for case %q is empty",
				label,
				caseID,
			)
		}
		if previous, exists := hashOwners[inputHash]; exists {
			return fmt.Errorf(
				"resolved %s cases %q and %q have duplicate normalized input hashes",
				label,
				previous,
				caseID,
			)
		}
		hashOwners[inputHash] = caseID
	}
	return nil
}

//nolint:gocyclo // Candidate lineage, deltas, decisions, and transition checks are independent.
func validateSuccessfulCandidate(
	report *Report,
	candidate *CandidateReport,
	index int,
	state *reportValidationState,
	structureID string,
	targetSurfaceID string,
	binding reportProvenanceBinding,
) error {
	label := fmt.Sprintf("candidate %q", candidate.ID)
	if candidate.Round != index+1 {
		return fmt.Errorf(
			"%s round is %d, want %d",
			label,
			candidate.Round,
			index+1,
		)
	}
	if candidate.SearchParentHash != state.searchHash {
		return fmt.Errorf(
			"%s search parent %q does not match lineage %q",
			label,
			candidate.SearchParentHash,
			state.searchHash,
		)
	}
	if candidate.ReleasedParentHash != state.releasedHash {
		return fmt.Errorf(
			"%s released parent %q does not match lineage %q",
			label,
			candidate.ReleasedParentHash,
			state.releasedHash,
		)
	}
	expectedPromptIterRunID := fmt.Sprintf(
		"%s/promptiter/%d",
		report.RunID,
		candidate.Round,
	)
	if candidate.PromptIterRunID != expectedPromptIterRunID {
		return fmt.Errorf(
			"%s PromptIter run id %q does not match invocation %q",
			label,
			candidate.PromptIterRunID,
			expectedPromptIterRunID,
		)
	}
	if candidate.PromptIterStatus != "succeeded" {
		return fmt.Errorf(
			"%s PromptIter status is %q, want %q",
			label,
			candidate.PromptIterStatus,
			"succeeded",
		)
	}
	if err := validateDecisionReasons(
		label+" search",
		candidate.SearchDecision,
	); err != nil {
		return err
	}
	if err := validateDecisionReasons(
		label+" release",
		candidate.ReleaseDecision,
	); err != nil {
		return err
	}
	if candidate.Status == EvaluationCompleted && len(candidate.Patches) == 0 {
		return fmt.Errorf("%s completed without a PromptIter patch", label)
	}
	if candidate.Profile != nil {
		if err := validateProfileConfigBinding(
			label,
			candidate.Profile,
			structureID,
			targetSurfaceID,
		); err != nil {
			return err
		}
		if err := validateCandidatePatches(
			label,
			candidate.Patches,
			report.ResolvedConfig.PromptIter.TargetSurfaceIDs,
		); err != nil {
			return err
		}
		if err := validateCandidatePatchBinding(
			label,
			candidate,
		); err != nil {
			return err
		}
	} else if len(candidate.Patches) > 0 {
		return fmt.Errorf("%s has PromptIter patches without a profile", label)
	}

	switch candidate.Status {
	case EvaluationRunFailed:
		return fmt.Errorf("%s has run_failed status in a successful report", label)
	case EvaluationCompleted:
		if err := validateSnapshotBinding(
			label+" train",
			candidate.Train,
			candidate.Profile.Hash,
			report.ResolvedConfig.Train,
			"train",
			fmt.Sprintf("%s/candidate_train/%d", report.RunID, candidate.Round),
			report.ResolvedConfig.Seed,
			binding,
			report.ResolvedConfig.Gate,
		); err != nil {
			return err
		}
		if err := validateSnapshotBinding(
			label+" validation",
			candidate.Validation,
			candidate.Profile.Hash,
			report.ResolvedConfig.Validation,
			"heldout_validation",
			fmt.Sprintf("%s/candidate_validation/%d", report.RunID, candidate.Round),
			report.ResolvedConfig.Seed,
			binding,
			report.ResolvedConfig.Gate,
		); err != nil {
			return err
		}
		if candidate.Profile.EvaluationRunID !=
			candidate.Validation.Provenance.RunID {
			return fmt.Errorf(
				"%s profile evaluation run id does not match validation snapshot",
				label,
			)
		}
		if candidate.Deltas == nil {
			return fmt.Errorf("%s completed without held-out deltas", label)
		}
		if err := validateCandidateDeltas(
			label,
			candidate,
			state,
			report.ResolvedConfig.Gate,
		); err != nil {
			return err
		}
		if candidate.ReleaseDecision.Status != DecisionNotEvaluable &&
			(candidate.ReleaseDecision.ScoreDelta == nil ||
				*candidate.ReleaseDecision.ScoreDelta != candidate.Deltas.VsReleased.ScoreDelta) {
			return fmt.Errorf(
				"%s release decision score delta does not match vs_released",
				label,
			)
		}
	case EvaluationNotEvaluable:
		if candidate.SearchDecision.Status != DecisionNotEvaluable ||
			candidate.ReleaseDecision.Status != DecisionNotEvaluable {
			return fmt.Errorf(
				"%s is incomplete but has an evaluable decision",
				label,
			)
		}
	}

	searchUpdated, releaseUpdated, err := validateCandidateTransition(
		label,
		candidate,
	)
	if err != nil {
		return err
	}
	if searchUpdated {
		state.searchHash = candidate.Profile.Hash
		state.searchValidation = candidate.Validation
	}
	if releaseUpdated {
		state.releasedHash = candidate.Profile.Hash
		state.releaseValidation = candidate.Validation
	}
	return nil
}

func validateProfileConfigBinding(
	label string,
	profile *ProfileRecord,
	structureID string,
	targetSurfaceID string,
) error {
	switch {
	case profile.StructureID != structureID:
		return fmt.Errorf(
			"%s profile structure id %q does not match initial structure %q",
			label,
			profile.StructureID,
			structureID,
		)
	case profile.TargetSurfaceID != targetSurfaceID:
		return fmt.Errorf(
			"%s profile target surface %q does not match resolved target %q",
			label,
			profile.TargetSurfaceID,
			targetSurfaceID,
		)
	case profile.Profile == nil:
		return fmt.Errorf("%s profile payload is nil", label)
	case profile.Profile.StructureID != "" &&
		profile.Profile.StructureID != profile.StructureID:
		return fmt.Errorf(
			"%s profile payload structure %q does not match record %q",
			label,
			profile.Profile.StructureID,
			profile.StructureID,
		)
	}
	targetValue, ok, err := profileOverrideValue(profile.Profile, targetSurfaceID)
	if err != nil {
		return fmt.Errorf("read %s profile target surface: %w", label, err)
	}
	if !ok {
		return fmt.Errorf(
			"%s profile target surface %q is absent from its payload",
			label,
			targetSurfaceID,
		)
	}
	if profile.Prompt != targetValue {
		return fmt.Errorf(
			"%s profile prompt does not match target surface %q",
			label,
			targetSurfaceID,
		)
	}
	return nil
}

func validateSnapshotBinding(
	label string,
	snapshot *EvaluationSnapshot,
	profileHash string,
	dataset DatasetSpec,
	split string,
	runID string,
	seed int64,
	binding reportProvenanceBinding,
	gate GatePolicy,
) error {
	if err := validateCompletedSnapshot(label, snapshot); err != nil {
		return err
	}
	provenance := snapshot.Provenance
	switch {
	case provenance.RunID != runID:
		return fmt.Errorf(
			"%s snapshot run id %q does not match invocation %q",
			label,
			provenance.RunID,
			runID,
		)
	case provenance.ProfileHash != profileHash:
		return fmt.Errorf(
			"%s snapshot profile hash %q does not match profile %q",
			label,
			provenance.ProfileHash,
			profileHash,
		)
	case provenance.EvalSetID != dataset.EvalSetID:
		return fmt.Errorf(
			"%s snapshot eval set id %q does not match resolved %q",
			label,
			provenance.EvalSetID,
			dataset.EvalSetID,
		)
	case provenance.EvalSetHash != dataset.EvalSetHash:
		return fmt.Errorf(
			"%s snapshot eval set hash %q does not match resolved %q",
			label,
			provenance.EvalSetHash,
			dataset.EvalSetHash,
		)
	case provenance.MetricsHash != dataset.MetricsHash:
		return fmt.Errorf(
			"%s snapshot metrics hash %q does not match resolved %q",
			label,
			provenance.MetricsHash,
			dataset.MetricsHash,
		)
	case provenance.Split != split:
		return fmt.Errorf(
			"%s snapshot split %q does not match %q",
			label,
			provenance.Split,
			split,
		)
	case provenance.Seed != seed:
		return fmt.Errorf(
			"%s snapshot seed %d does not match resolved seed %d",
			label,
			provenance.Seed,
			seed,
		)
	case provenance.EvaluatorConfigHash != binding.evaluatorConfigHash:
		return fmt.Errorf(
			"%s snapshot evaluator config hash %q does not match runtime binding %q",
			label,
			provenance.EvaluatorConfigHash,
			binding.evaluatorConfigHash,
		)
	case provenance.MetricPolicyHash != binding.metricPolicyHash:
		return fmt.Errorf(
			"%s snapshot metric policy hash %q does not match resolved policy %q",
			label,
			provenance.MetricPolicyHash,
			binding.metricPolicyHash,
		)
	case !reflect.DeepEqual(snapshot.Inventory.CaseIDs, dataset.CaseIDs):
		return fmt.Errorf("%s snapshot case inventory does not match resolved dataset", label)
	case !reflect.DeepEqual(snapshot.Inventory.MetricNames, dataset.MetricNames):
		return fmt.Errorf("%s snapshot metric inventory does not match resolved dataset", label)
	}
	if _, err := validateComparisonSnapshot(label, snapshot, gate); err != nil {
		return fmt.Errorf("%s snapshot is incomplete: %w", label, err)
	}
	return nil
}

func validateCandidatePatches(
	label string,
	patches []PatchRecord,
	targetSurfaceIDs []string,
) error {
	targets := make(map[string]struct{}, len(targetSurfaceIDs))
	for _, target := range targetSurfaceIDs {
		targets[target] = struct{}{}
	}
	seen := make(map[string]struct{}, len(patches))
	for i, patch := range patches {
		switch {
		case strings.TrimSpace(patch.SurfaceID) == "":
			return fmt.Errorf("%s PromptIter patch %d surface id is empty", label, i+1)
		case strings.TrimSpace(patch.Value) == "":
			return fmt.Errorf("%s PromptIter patch %d value is empty", label, i+1)
		case strings.TrimSpace(patch.Reason) == "":
			return fmt.Errorf("%s PromptIter patch %d reason is empty", label, i+1)
		}
		if _, ok := targets[patch.SurfaceID]; !ok {
			return fmt.Errorf(
				"%s PromptIter patch %d targets unconfigured surface %q",
				label,
				i+1,
				patch.SurfaceID,
			)
		}
		if _, exists := seen[patch.SurfaceID]; exists {
			return fmt.Errorf(
				"%s has duplicate PromptIter patch for surface %q",
				label,
				patch.SurfaceID,
			)
		}
		seen[patch.SurfaceID] = struct{}{}
	}
	return nil
}

func validateCandidatePatchBinding(
	label string,
	candidate *CandidateReport,
) error {
	reasons := make([]string, 0, len(candidate.Patches))
	for _, patch := range candidate.Patches {
		value, ok, err := profileOverrideValue(
			candidate.Profile.Profile,
			patch.SurfaceID,
		)
		if err != nil {
			return fmt.Errorf(
				"read %s profile patch surface %q: %w",
				label,
				patch.SurfaceID,
				err,
			)
		}
		if !ok {
			return fmt.Errorf(
				"%s PromptIter patch surface %q is absent from the candidate profile",
				label,
				patch.SurfaceID,
			)
		}
		if patch.Value != value {
			return fmt.Errorf(
				"%s PromptIter patch value for surface %q does not match the candidate profile",
				label,
				patch.SurfaceID,
			)
		}
		reasons = append(reasons, strings.TrimSpace(patch.Reason))
	}
	targetValue, ok, err := profileOverrideValue(
		candidate.Profile.Profile,
		candidate.Profile.TargetSurfaceID,
	)
	if err != nil {
		return fmt.Errorf(
			"read %s profile target surface %q: %w",
			label,
			candidate.Profile.TargetSurfaceID,
			err,
		)
	}
	if !ok {
		return fmt.Errorf(
			"%s target surface %q is absent from the candidate profile",
			label,
			candidate.Profile.TargetSurfaceID,
		)
	}
	if candidate.Profile.Prompt != targetValue {
		return fmt.Errorf(
			"%s displayed prompt does not match the candidate profile target surface",
			label,
		)
	}
	expectedReason := strings.Join(reasons, "; ")
	if candidate.OptimizationReason != expectedReason {
		return fmt.Errorf(
			"%s optimization reason does not match PromptIter patch reasons",
			label,
		)
	}
	return nil
}

func profileOverrideValue(
	profile *promptiter.Profile,
	surfaceID string,
) (string, bool, error) {
	if profile == nil {
		return "", false, errors.New("profile payload is nil")
	}
	found := false
	value := ""
	for _, override := range profile.Overrides {
		if override.SurfaceID != surfaceID {
			continue
		}
		if found {
			return "", false, fmt.Errorf(
				"duplicate override for surface %q",
				surfaceID,
			)
		}
		found = true
		if override.Value.Text != nil {
			value = *override.Value.Text
			continue
		}
		data, err := json.Marshal(override.Value)
		if err != nil {
			return "", false, err
		}
		value = string(data)
	}
	return value, found, nil
}

func validateCandidateDeltas(
	label string,
	candidate *CandidateReport,
	state *reportValidationState,
	gate GatePolicy,
) error {
	comparisons := []struct {
		name   string
		delta  DeltaSummary
		before *EvaluationSnapshot
	}{
		{
			name:   "vs_initial",
			delta:  candidate.Deltas.VsInitial,
			before: state.initialValidation,
		},
		{
			name:   "vs_search_parent",
			delta:  candidate.Deltas.VsSearchParent,
			before: state.searchValidation,
		},
		{
			name:   "vs_released",
			delta:  candidate.Deltas.VsReleased,
			before: state.releaseValidation,
		},
	}
	for _, comparison := range comparisons {
		delta := comparison.delta
		if delta.Comparison != comparison.name {
			return fmt.Errorf(
				"%s delta comparison is %q, want %q",
				label,
				delta.Comparison,
				comparison.name,
			)
		}
		if comparison.before == nil {
			return fmt.Errorf(
				"%s delta %s has no parent snapshot",
				label,
				comparison.name,
			)
		}
		if delta.BeforeProfileHash != comparison.before.Provenance.ProfileHash {
			return fmt.Errorf(
				"%s delta %s before profile hash %q does not match parent %q",
				label,
				comparison.name,
				delta.BeforeProfileHash,
				comparison.before.Provenance.ProfileHash,
			)
		}
		if delta.AfterProfileHash != candidate.Profile.Hash {
			return fmt.Errorf(
				"%s delta %s after profile hash %q does not match candidate %q",
				label,
				comparison.name,
				delta.AfterProfileHash,
				candidate.Profile.Hash,
			)
		}
		if err := validateDeltaSummary(delta, gate, gate.Epsilon); err != nil {
			return fmt.Errorf(
				"%s delta %s is invalid: %w",
				label,
				comparison.name,
				err,
			)
		}
		expected, err := CalculateDelta(
			comparison.name,
			comparison.before,
			candidate.Validation,
			gate,
		)
		if err != nil {
			return fmt.Errorf(
				"%s delta %s snapshots are incompatible: %w",
				label,
				comparison.name,
				err,
			)
		}
		if !reflect.DeepEqual(delta, expected) {
			return fmt.Errorf(
				"%s delta %s does not match its bound snapshots",
				label,
				comparison.name,
			)
		}
	}
	return nil
}

func validateCandidateTransition(
	label string,
	candidate *CandidateReport,
) (bool, bool, error) {
	transition := candidate.Transition
	if strings.TrimSpace(transition.Explanation) == "" {
		return false, false, fmt.Errorf("%s transition explanation is empty", label)
	}
	if transition.SearchBefore != candidate.SearchParentHash {
		return false, false, fmt.Errorf(
			"%s search transition starts at %q, want parent %q",
			label,
			transition.SearchBefore,
			candidate.SearchParentHash,
		)
	}
	if transition.ReleasedBefore != candidate.ReleasedParentHash {
		return false, false, fmt.Errorf(
			"%s release transition starts at %q, want parent %q",
			label,
			transition.ReleasedBefore,
			candidate.ReleasedParentHash,
		)
	}
	searchUpdated := false
	releaseUpdated := false
	searchAfter := candidate.SearchParentHash
	releaseAfter := candidate.ReleasedParentHash
	if candidate.SearchDecision.Status != DecisionNotEvaluable &&
		candidate.ReleaseDecision.Status != DecisionNotEvaluable {
		if candidate.SearchDecision.Status == DecisionAccepted {
			searchUpdated = true
			searchAfter = candidate.Profile.Hash
		}
		if candidate.ReleaseDecision.Status == DecisionAccepted {
			releaseUpdated = true
			releaseAfter = candidate.Profile.Hash
		}
	}
	if transition.SearchUpdated != searchUpdated ||
		transition.SearchAfter != searchAfter {
		return false, false, fmt.Errorf(
			"%s search transition is inconsistent with its independent decision",
			label,
		)
	}
	if transition.ReleaseUpdated != releaseUpdated ||
		transition.ReleasedAfter != releaseAfter {
		return false, false, fmt.Errorf(
			"%s release transition is inconsistent with its independent decision",
			label,
		)
	}
	return searchUpdated, releaseUpdated, nil
}

func validateDecisionReasons(label string, decision Decision) error {
	if len(decision.Reasons) == 0 {
		return fmt.Errorf("%s decision reasons are empty", label)
	}
	for _, reason := range decision.Reasons {
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("%s decision reasons contain an empty reason", label)
		}
	}
	return nil
}

func validateReportResourceLedgers(report *Report) error {
	if err := validateResourceLedger("global", report.Resources); err != nil {
		return err
	}
	remaining := make(map[ResourceEntry]int, len(report.Resources.Entries))
	for _, entry := range report.Resources.Entries {
		remaining[entry]++
	}
	for i := range report.Candidates {
		candidate := &report.Candidates[i]
		label := fmt.Sprintf("candidate %q", candidate.ID)
		if err := validateResourceLedger(label, candidate.Resources); err != nil {
			return err
		}
		for _, entry := range candidate.Resources.Entries {
			if remaining[entry] == 0 {
				return fmt.Errorf(
					"%s resource entry for stage %q is absent from the global ledger",
					label,
					entry.Stage,
				)
			}
			remaining[entry]--
		}
	}
	return nil
}

func validateResourceLedger(label string, ledger ResourceLedger) error {
	expected := ResourceUsage{}
	for i, entry := range ledger.Entries {
		if strings.TrimSpace(entry.Stage) == "" {
			return fmt.Errorf("%s resource entry %d stage is empty", label, i+1)
		}
		if entry.Round < 0 {
			return fmt.Errorf(
				"%s resource entry %d round is negative",
				label,
				i+1,
			)
		}
		if err := validateReportResourceUsage(
			fmt.Sprintf("%s resource entry %d", label, i+1),
			entry.Usage,
		); err != nil {
			return err
		}
		if i == 0 {
			expected = entry.Usage
		} else {
			expected = addResourceUsage(expected, entry.Usage)
		}
	}
	if err := validateReportResourceUsage(
		label+" cumulative resources",
		ledger.Cumulative,
	); err != nil {
		return err
	}
	if !reflect.DeepEqual(ledger.Cumulative, expected) {
		return fmt.Errorf(
			"%s cumulative resources do not equal the sum of its entries",
			label,
		)
	}
	return nil
}

func validateReportResourceUsage(label string, usage ResourceUsage) error {
	if reasons := validateResourceUsage(usage); len(reasons) > 0 {
		return fmt.Errorf("%s: %s", label, strings.Join(reasons, "; "))
	}
	if !usage.MonetaryCost.Available &&
		strings.TrimSpace(usage.MonetaryCost.Unit) != "" {
		return fmt.Errorf(
			"%s: unavailable monetary cost has a unit",
			label,
		)
	}
	return nil
}

func validateNotEvaluableTransition(candidate *CandidateReport) error {
	transition := candidate.Transition
	if transition.SearchUpdated || transition.ReleaseUpdated ||
		transition.SearchBefore != transition.SearchAfter ||
		transition.ReleasedBefore != transition.ReleasedAfter {
		return fmt.Errorf(
			"candidate %q has a not-evaluable decision that updates a profile pointer",
			candidate.ID,
		)
	}
	return nil
}

func validateProfileRecord(label string, expectedRole ProfileRole, profile *ProfileRecord) error {
	if profile == nil {
		return fmt.Errorf("%s profile is nil", label)
	}
	switch {
	case profile.Role != expectedRole:
		return fmt.Errorf("%s profile has role %q, want %q", label, profile.Role, expectedRole)
	case strings.TrimSpace(profile.Hash) == "":
		return fmt.Errorf("%s profile hash is empty", label)
	case strings.TrimSpace(profile.StructureID) == "":
		return fmt.Errorf("%s profile structure id is empty", label)
	case strings.TrimSpace(profile.TargetSurfaceID) == "":
		return fmt.Errorf("%s profile target surface id is empty", label)
	case strings.TrimSpace(profile.Prompt) == "":
		return fmt.Errorf("%s profile prompt is empty", label)
	case profile.Profile == nil:
		return fmt.Errorf("%s profile payload is nil", label)
	}
	hash, err := ProfileFingerprint(profile.Profile)
	if err != nil {
		return fmt.Errorf("fingerprint %s profile: %w", label, err)
	}
	if hash != profile.Hash {
		return fmt.Errorf("%s profile hash %q does not match payload hash %q", label, profile.Hash, hash)
	}
	return nil
}

func validateCompletedSnapshot(label string, snapshot *EvaluationSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("%s snapshot is nil", label)
	}
	switch {
	case snapshot.Status != EvaluationCompleted:
		return fmt.Errorf("%s snapshot status is %q, want %q", label, snapshot.Status, EvaluationCompleted)
	case strings.TrimSpace(snapshot.Provenance.RunID) == "":
		return fmt.Errorf("%s snapshot run id is empty", label)
	case strings.TrimSpace(snapshot.Provenance.ProfileHash) == "":
		return fmt.Errorf("%s snapshot profile hash is empty", label)
	case strings.TrimSpace(snapshot.Provenance.EvalSetID) == "":
		return fmt.Errorf("%s snapshot eval set id is empty", label)
	case strings.TrimSpace(snapshot.Provenance.EvalSetHash) == "":
		return fmt.Errorf("%s snapshot eval set hash is empty", label)
	case strings.TrimSpace(snapshot.Provenance.MetricsHash) == "":
		return fmt.Errorf("%s snapshot metrics hash is empty", label)
	case strings.TrimSpace(snapshot.Provenance.Split) == "":
		return fmt.Errorf("%s snapshot split is empty", label)
	case strings.TrimSpace(snapshot.Provenance.EvaluatorConfigHash) == "":
		return fmt.Errorf("%s snapshot evaluator config hash is empty", label)
	case strings.TrimSpace(snapshot.Provenance.MetricPolicyHash) == "":
		return fmt.Errorf("%s snapshot metric policy hash is empty", label)
	case len(snapshot.Inventory.CaseIDs) == 0:
		return fmt.Errorf("%s snapshot case inventory is empty", label)
	case len(snapshot.Inventory.MetricNames) == 0:
		return fmt.Errorf("%s snapshot metric inventory is empty", label)
	case len(snapshot.Cases) != len(snapshot.Inventory.CaseIDs):
		return fmt.Errorf(
			"%s snapshot has %d cases for %d inventory ids",
			label,
			len(snapshot.Cases),
			len(snapshot.Inventory.CaseIDs),
		)
	}
	return nil
}

func validPipelineStatus(status PipelineStatus) bool {
	switch status {
	case PipelineSucceeded, PipelineRunFailed, PipelineBudgetStopped:
		return true
	default:
		return false
	}
}

func validStopReason(reason StopReason) bool {
	switch reason {
	case StopMaxRounds, StopBudgetExhausted, StopNoCandidate, StopNecessaryRunFailed,
		StopRepeatedFingerprint, StopTrainingFailuresFixed:
		return true
	default:
		return false
	}
}

func validDecisionStatus(status DecisionStatus) bool {
	switch status {
	case DecisionAccepted, DecisionRejected, DecisionNotEvaluable:
		return true
	default:
		return false
	}
}

func validEvaluationStatus(status EvaluationStatus) bool {
	switch status {
	case EvaluationCompleted, EvaluationNotEvaluable, EvaluationRunFailed:
		return true
	default:
		return false
	}
}

func validateRenderedJSON(data []byte, expected *Report) error {
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("validate rendered JSON: %w", err)
	}
	if decoded.SchemaVersion != expected.SchemaVersion ||
		decoded.ReportID != expected.ReportID ||
		decoded.RunID != expected.RunID ||
		len(decoded.Candidates) != len(expected.Candidates) {
		return errors.New("rendered JSON lost canonical report identity")
	}
	return nil
}

func validateRenderedMarkdown(data []byte, expected *Report) error {
	text := string(data)
	required := []string{
		"# Prompt Optimization Report",
		markdownInline(expected.SchemaVersion),
		markdownInline(expected.ReportID),
		markdownInline(expected.RunID),
		string(expected.Status),
		string(expected.StopReason),
		"## Final Released Prompt",
	}
	for _, item := range required {
		if item != "" && !strings.Contains(text, item) {
			return fmt.Errorf("rendered Markdown is missing %q", item)
		}
	}
	return nil
}

func validateArtifactPair(jsonData, markdownData []byte, expected *Report) error {
	if err := validateRenderedJSON(jsonData, expected); err != nil {
		return err
	}
	if err := validateRenderedMarkdown(markdownData, expected); err != nil {
		return err
	}
	return nil
}

func writeClosedTemp(target string, data []byte) (path string, err error) {
	file, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(path)
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err = file.Write(data); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func writeCandidate(out *strings.Builder, candidate *CandidateReport) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "### Round %d — %s\n\n", candidate.Round, markdownInline(candidate.ID))
	fmt.Fprintf(out, "- Evaluation status: `%s`\n", markdownInline(string(candidate.Status)))
	fmt.Fprintf(out, "- Search parent: `%s`\n", markdownInline(candidate.SearchParentHash))
	fmt.Fprintf(out, "- Released parent: `%s`\n", markdownInline(candidate.ReleasedParentHash))
	if candidate.PromptIterRunID != "" {
		fmt.Fprintf(out, "- PromptIter run: `%s`\n", markdownInline(candidate.PromptIterRunID))
	}
	if candidate.PromptIterStatus != "" {
		fmt.Fprintf(out, "- PromptIter status: `%s`\n", markdownInline(candidate.PromptIterStatus))
	}
	if candidate.OptimizationReason != "" {
		fmt.Fprintf(out, "- Optimization reason: %s\n",
			markdownText(candidate.OptimizationReason, defaultMarkdownTextLimit))
	}
	writeDecision(out, "Search", candidate.SearchDecision)
	writeDecision(out, "Release", candidate.ReleaseDecision)
	writeErrors(out, candidate.Errors)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "#### State transition")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "| Pointer | Before | After | Updated |")
	fmt.Fprintln(out, "|---|---|---|---|")
	fmt.Fprintf(out, "| Search | %s | %s | %t |\n",
		markdownTable(candidate.Transition.SearchBefore),
		markdownTable(candidate.Transition.SearchAfter),
		candidate.Transition.SearchUpdated)
	fmt.Fprintf(out, "| Released | %s | %s | %t |\n",
		markdownTable(candidate.Transition.ReleasedBefore),
		markdownTable(candidate.Transition.ReleasedAfter),
		candidate.Transition.ReleaseUpdated)
	if candidate.Transition.Explanation != "" {
		fmt.Fprintf(out, "\n%s\n", markdownText(candidate.Transition.Explanation, defaultMarkdownTextLimit))
	}

	if candidate.Profile != nil {
		writeProfile(out, "Candidate profile", candidate.Profile)
	}
	if len(candidate.Patches) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "#### Patches")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "| Surface | Value | Reason |")
		fmt.Fprintln(out, "|---|---|---|")
		for _, patch := range candidate.Patches {
			fmt.Fprintf(out, "| %s | %s | %s |\n",
				markdownTable(patch.SurfaceID),
				markdownTable(redactAndBoundText(patch.Value, defaultMarkdownTextLimit)),
				markdownTable(redactAndBoundText(patch.Reason, defaultMarkdownTextLimit)))
		}
	}

	writeSnapshot(out, "Candidate training", candidate.Train)
	writeSnapshot(out, "Candidate held-out validation", candidate.Validation)
	if candidate.Deltas != nil {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "#### Three held-out deltas")
		writeDelta(out, "vs_initial", candidate.Deltas.VsInitial)
		writeDelta(out, "vs_search_parent", candidate.Deltas.VsSearchParent)
		writeDelta(out, "vs_released", candidate.Deltas.VsReleased)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "#### Candidate resources")
	writeResourceLedger(out, candidate.Resources)
}

func writeProfile(out *strings.Builder, title string, profile *ProfileRecord) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "### %s\n\n", markdownInline(title))
	if profile == nil {
		fmt.Fprintln(out, "_Not available._")
		return
	}
	fmt.Fprintf(out, "- Role: `%s`\n", markdownInline(string(profile.Role)))
	fmt.Fprintf(out, "- Hash: `%s`\n", markdownInline(profile.Hash))
	fmt.Fprintf(out, "- Structure: `%s`\n", markdownInline(profile.StructureID))
	fmt.Fprintf(out, "- Target surface: `%s`\n", markdownInline(profile.TargetSurfaceID))
	if profile.EvaluationRunID != "" {
		fmt.Fprintf(out, "- Evaluation run: `%s`\n", markdownInline(profile.EvaluationRunID))
	}
	fmt.Fprintln(out)
	writeCodeBlock(out, profile.Prompt, "")
}

func writeSnapshot(out *strings.Builder, title string, snapshot *EvaluationSnapshot) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "### %s\n\n", markdownInline(title))
	if snapshot == nil {
		fmt.Fprintln(out, "_Not available._")
		return
	}
	fmt.Fprintf(out, "- Status: `%s`\n", markdownInline(string(snapshot.Status)))
	fmt.Fprintf(out, "- Eval set: `%s`\n", markdownInline(snapshot.Provenance.EvalSetID))
	fmt.Fprintf(out, "- Split: `%s`\n", markdownInline(snapshot.Provenance.Split))
	fmt.Fprintf(out, "- Evaluation run: `%s`\n", markdownInline(snapshot.Provenance.RunID))
	fmt.Fprintf(out, "- Profile hash: `%s`\n", markdownInline(snapshot.Provenance.ProfileHash))
	fmt.Fprintf(out, "- Score: `%.6f`; passed: `%d`; failed: `%d`; latency: `%d ms`\n",
		snapshot.OverallScore, snapshot.Passed, snapshot.Failed, snapshot.LatencyMS)
	if snapshot.Error != "" {
		fmt.Fprintf(out, "- Error: %s\n", markdownText(snapshot.Error, defaultMarkdownTextLimit))
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "| Case | Status | Passed | Hard | Critical | Primary metric | Error |")
	fmt.Fprintln(out, "|---|---|---:|---:|---:|---|---|")
	for _, result := range snapshot.Cases {
		fmt.Fprintf(out, "| %s | %s | %t | %t | %t | %s | %s |\n",
			markdownTable(result.CaseID),
			markdownTable(result.Status),
			result.Passed,
			result.HardFailure,
			result.Critical,
			markdownTable(result.PrimaryMetric),
			markdownTable(redactAndBoundText(result.Error, defaultMarkdownTextLimit)))
	}
	for i := range snapshot.Cases {
		writeCaseEvidence(out, &snapshot.Cases[i])
	}

	for _, attribution := range snapshot.Attributions {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "#### Attribution: %s / %s\n\n",
			markdownInline(attribution.EvalCaseID),
			markdownInline(attribution.MetricName))
		fmt.Fprintf(out, "- Category: `%s`; severity: `%s`; confidence: `%.3f`; evidence: `%s`\n",
			markdownInline(string(attribution.PrimaryCategory)),
			markdownInline(string(attribution.Severity)),
			attribution.Confidence,
			markdownInline(string(attribution.EvidenceSufficiency)))
		fmt.Fprintf(out, "- Reason: %s\n", markdownText(attribution.Reason, defaultMarkdownTextLimit))
		for _, evidence := range attribution.Evidence {
			fmt.Fprintf(out, "  - `%s` `%s`: %s\n",
				markdownInline(evidence.ID),
				markdownInline(evidence.Kind),
				markdownText(evidence.Summary, defaultMarkdownTextLimit))
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "#### Snapshot resources")
	writeResourceUsage(out, snapshot.Resources)
}

func writeCaseEvidence(out *strings.Builder, result *CaseResult) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "#### Case evidence: %s\n\n", markdownInline(result.CaseID))
	fmt.Fprintf(out, "- Route: `%s`; expected route: `%s`\n",
		markdownInline(result.Route),
		markdownInline(result.ExpectedRoute))
	if len(result.ExpectedFacts) > 0 {
		fmt.Fprintf(out, "- Expected facts: %s\n",
			markdownText(strings.Join(result.ExpectedFacts, "; "), defaultMarkdownTextLimit))
	}
	if result.Error != "" {
		fmt.Fprintf(out, "- Error: %s\n", markdownText(result.Error, defaultMarkdownTextLimit))
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Metrics:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "| Metric | Score | Threshold | Direction | Status | Passed | Reason | Rubrics |")
	fmt.Fprintln(out, "|---|---:|---:|---|---|---:|---|---|")
	for _, item := range result.Metrics {
		fmt.Fprintf(out, "| %s | %.6f | %.6f | %s | %s | %t | %s | %s |\n",
			markdownTable(item.MetricName),
			item.Score,
			item.Threshold,
			markdownTable(string(item.Direction)),
			markdownTable(item.Status),
			item.Passed,
			markdownTable(redactAndBoundText(item.Reason, defaultMarkdownTextLimit)),
			markdownTable(rubricSummary(item.RubricScores)))
	}

	if result.FinalResponse != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Final response:")
		writeBoundedCodeBlock(out, result.FinalResponse, "", defaultMarkdownTextLimit)
	}
	if result.ExpectedResponse != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Expected response:")
		writeBoundedCodeBlock(out, result.ExpectedResponse, "", defaultMarkdownTextLimit)
	}
	if result.ExpectStructured || result.StructuredOutput != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Structured output:")
		writeBoundedCodeBlock(out, result.StructuredOutput, "json", defaultMarkdownTextLimit)
	}
	if result.ExpectNoTools {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Expected tool trajectory: `[]` (explicit no-tool requirement)")
	}
	writeToolCalls(out, "Observed tools", result.ToolTrajectory)
	writeToolCalls(out, "Expected tools", result.ExpectedTools)
	writeTrace(out, result.Trace)
}

func rubricSummary(scores []RubricScore) string {
	parts := make([]string, 0, len(scores))
	for _, score := range scores {
		part := fmt.Sprintf("%s=%.6f", score.ID, score.Score)
		if score.Reason != "" {
			part += ": " + redactAndBoundText(score.Reason, defaultMarkdownTextLimit)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func writeToolCalls(out *strings.Builder, title string, calls []ToolCall) {
	if len(calls) == 0 {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "%s:\n\n", markdownInline(title))
	fmt.Fprintln(out, "| Sequence | Name | Arguments | Result |")
	fmt.Fprintln(out, "|---:|---|---|---|")
	for _, call := range calls {
		fmt.Fprintf(out, "| %d | %s | %s | %s |\n",
			call.Sequence,
			markdownTable(call.Name),
			markdownTable(compactJSON(call.Arguments)),
			markdownTable(compactJSON(call.Result)))
	}
}

func writeTrace(out *strings.Builder, trace []TraceStep) {
	if len(trace) == 0 {
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Trace:")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "| Step | Node | Agent | Branch | Applied surfaces | Input | Output | Error |")
	fmt.Fprintln(out, "|---|---|---|---|---|---|---|---|")
	for _, step := range trace {
		fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			markdownTable(step.StepID),
			markdownTable(step.NodeID),
			markdownTable(step.AgentName),
			markdownTable(step.Branch),
			markdownTable(strings.Join(step.AppliedSurfaceIDs, ", ")),
			markdownTable(redactAndBoundText(step.Input, defaultMarkdownTextLimit)),
			markdownTable(redactAndBoundText(step.Output, defaultMarkdownTextLimit)),
			markdownTable(redactAndBoundText(step.Error, defaultMarkdownTextLimit)))
	}
}

func compactJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return redactAndBoundText(fmt.Sprintf("<unrenderable: %v>", err), defaultMarkdownTextLimit)
	}
	return redactAndBoundText(string(data), defaultMarkdownTextLimit)
}

func writeDelta(out *strings.Builder, label string, delta DeltaSummary) {
	fmt.Fprintln(out)
	fmt.Fprintf(out, "##### %s\n\n", markdownInline(label))
	fmt.Fprintf(out, "- Comparison: `%s`\n", markdownInline(delta.Comparison))
	fmt.Fprintf(out, "- Profiles: `%s` → `%s`\n",
		markdownInline(delta.BeforeProfileHash),
		markdownInline(delta.AfterProfileHash))
	fmt.Fprintf(out, "- Score: `%.6f` → `%.6f` (`%+.6f`)\n",
		delta.BeforeOverallScore, delta.AfterOverallScore, delta.ScoreDelta)
	fmt.Fprintf(out, "- Newly passing: `%d`; newly failing: `%d`; improved: `%d`; regressed: `%d`; unchanged: `%d`\n",
		delta.NewlyPassing, delta.NewlyFailing, delta.Improved, delta.Regressed, delta.Unchanged)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "| Case | Before | After | Change | Hard | Critical | Reason |")
	fmt.Fprintln(out, "|---|---|---|---|---:|---:|---|")
	for _, item := range delta.Cases {
		fmt.Fprintf(out, "| %s | %s | %s | %s | %t | %t | %s |\n",
			markdownTable(item.CaseID),
			markdownTable(item.BeforeStatus),
			markdownTable(item.AfterStatus),
			markdownTable(string(item.PrimaryKind)),
			item.HardFailure,
			item.Critical,
			markdownTable(redactAndBoundText(item.Reason, defaultMarkdownTextLimit)))
	}
}

func writeDecision(out *strings.Builder, label string, decision Decision) {
	fmt.Fprintf(out, "- %s: %s\n", markdownInline(label), strings.ToUpper(string(decision.Status)))
	if decision.ScoreDelta != nil {
		fmt.Fprintf(out, "  - Score delta: `%+.6f`\n", *decision.ScoreDelta)
	}
	for _, reason := range decision.Reasons {
		fmt.Fprintf(out, "  - %s\n", markdownText(reason, defaultMarkdownTextLimit))
	}
}

func writeErrors(out *strings.Builder, errorsList []string) {
	if len(errorsList) == 0 {
		return
	}
	fmt.Fprintln(out, "- Errors:")
	for _, item := range errorsList {
		fmt.Fprintf(out, "  - %s\n", markdownText(item, defaultMarkdownTextLimit))
	}
}

func writeResourceLedger(out *strings.Builder, ledger ResourceLedger) {
	if len(ledger.Entries) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "| Stage | Round | Split | Profile | Calls | Input | Output | Latency | Cost | Failed |")
		fmt.Fprintln(out, "|---|---:|---|---|---:|---:|---:|---:|---:|---:|")
		for _, entry := range ledger.Entries {
			fmt.Fprintf(out, "| %s | %d | %s | %s | %s | %s | %s | %s | %s | %t |\n",
				markdownTable(entry.Stage),
				entry.Round,
				markdownTable(entry.Split),
				markdownTable(entry.ProfileHash),
				countText(entry.Usage.ModelCalls),
				countText(entry.Usage.InputTokens),
				countText(entry.Usage.OutputTokens),
				countText(entry.Usage.LatencyMS),
				amountText(entry.Usage.MonetaryCost),
				entry.Failed)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Cumulative:")
	writeResourceUsage(out, ledger.Cumulative)
}

func writeResourceUsage(out *strings.Builder, usage ResourceUsage) {
	fmt.Fprintf(out, "- Model calls: `%s`; input tokens: `%s`; output tokens: `%s`; latency ms: `%s`; monetary cost: `%s`\n",
		countText(usage.ModelCalls),
		countText(usage.InputTokens),
		countText(usage.OutputTokens),
		countText(usage.LatencyMS),
		amountText(usage.MonetaryCost))
}

func countText(count Count) string {
	if !count.Available {
		return "unavailable"
	}
	return fmt.Sprintf("%d", count.Value)
}

func amountText(amount Amount) string {
	if !amount.Available {
		return "unavailable"
	}
	if amount.Unit == "" {
		return fmt.Sprintf("%.6f", amount.Value)
	}
	return fmt.Sprintf("%.6f %s", amount.Value, markdownTable(amount.Unit))
}

func writeStringMap(out *strings.Builder, values map[string]string) {
	if len(values) == 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "_None._")
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "| Input | SHA-256 |")
	fmt.Fprintln(out, "|---|---|")
	for _, key := range keys {
		fmt.Fprintf(out, "| %s | `%s` |\n", markdownTable(key), markdownInline(values[key]))
	}
}

func writeJSONBlock(out *strings.Builder, value any) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		writeCodeBlock(out, "unable to render: "+err.Error(), "")
		return
	}
	writeCodeBlock(out, string(data), "json")
}

func writeCodeBlock(out *strings.Builder, value, language string) {
	writeBoundedCodeBlock(out, value, language, defaultPromptTextLimit)
}

func writeBoundedCodeBlock(out *strings.Builder, value, language string, limit int) {
	value = redactAndBoundText(value, limit)
	fence := dynamicFence(value)
	fmt.Fprintf(out, "%s%s\n%s\n%s\n", fence, language, value, fence)
}

func dynamicFence(value string) string {
	longest := 0
	current := 0
	for _, char := range value {
		if char == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	if longest < 3 {
		longest = 2
	}
	return strings.Repeat("`", longest+1)
}

func markdownInline(value string) string {
	return markdownTable(redactAndBoundText(value, defaultMarkdownTextLimit))
}

func markdownText(value string, limit int) string {
	return markdownTable(redactAndBoundText(value, limit))
}

func markdownTable(value string) string {
	replacer := strings.NewReplacer(
		`&`, `&amp;`,
		`|`, `&#124;`,
		"`", "&#96;",
		"\r\n", "<br>",
		"\n", "<br>",
		"\r", "<br>",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(value)
}

func redactAndBoundText(value string, limit int) string {
	value = redactCredentialText(value)
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "..."
}

func redactCredentialText(value string) string {
	for index := 0; index < len(value); {
		if value[index] != '{' && value[index] != '[' {
			index++
			continue
		}
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(value[index:]))
		if err := decoder.Decode(&decoded); err != nil {
			index++
			continue
		}
		consumed := int(decoder.InputOffset())
		redacted, changed := redactCredentialJSONValue(decoded)
		if !changed {
			index += consumed
			continue
		}
		data, err := json.Marshal(redacted)
		if err != nil {
			index += consumed
			continue
		}
		value = value[:index] + string(data) + value[index+consumed:]
		index += len(data)
	}
	return redactCredentialAssignments(value)
}

func redactCredentialJSONValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		changed := false
		for key, child := range typed {
			if credentialKey(key) {
				redacted[key] = "[REDACTED]"
				changed = true
				continue
			}
			redactedChild, childChanged := redactCredentialJSONValue(child)
			redacted[key] = redactedChild
			changed = changed || childChanged
		}
		return redacted, changed
	case []any:
		redacted := make([]any, len(typed))
		changed := false
		for i := range typed {
			redactedChild, childChanged := redactCredentialJSONValue(typed[i])
			redacted[i] = redactedChild
			changed = changed || childChanged
		}
		return redacted, changed
	case string:
		redacted := redactCredentialAssignments(typed)
		return redacted, redacted != typed
	default:
		return value, false
	}
}

func redactCredentialAssignments(value string) string {
	value = jsonCredentialPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := jsonCredentialPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		prefix := parts[1]
		colon := strings.LastIndexByte(prefix, ':')
		if colon < 0 {
			return match
		}
		var key string
		if err := json.Unmarshal([]byte(strings.TrimSpace(prefix[:colon])), &key); err != nil ||
			!credentialKey(key) {
			return match
		}
		return prefix + `"[REDACTED]"`
	})
	return assignmentCredentialPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := assignmentCredentialPattern.FindStringSubmatch(match)
		if len(parts) != 3 || !credentialKey(parts[1]) {
			return match
		}
		return parts[1] + parts[2] + "[REDACTED]"
	})
}

func sanitizeJSONValue(path string, value any, evidenceLimit, depth int) any {
	key := jsonPathKey(path)
	if credentialKey(key) {
		return "[REDACTED]"
	}
	if depth >= persistedNestingLimit {
		return "[TRUNCATED: maximum nesting depth]"
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		collectionLimit := len(keys)
		if evidencePayloadMapPath(path) && collectionLimit > evidenceLimit {
			collectionLimit = evidenceLimit
		}
		sanitized := make(map[string]any, min(len(keys), collectionLimit)+1)
		for index, childKey := range keys {
			if index >= collectionLimit {
				sanitized["__report_truncated__"] = fmt.Sprintf(
					"%d map entries omitted",
					len(keys)-collectionLimit,
				)
				break
			}
			childPath := childKey
			if path != "" {
				childPath = path + "." + childKey
			}
			sanitized[childKey] = sanitizeJSONValue(
				childPath,
				typed[childKey],
				evidenceLimit,
				depth+1,
			)
		}
		return sanitized
	case []any:
		collectionLimit := len(typed)
		if evidenceCollectionPath(path) && collectionLimit > evidenceLimit {
			collectionLimit = evidenceLimit
		}
		if len(typed) > collectionLimit {
			typed = typed[:collectionLimit]
		}
		sanitized := make([]any, len(typed))
		for i := range typed {
			sanitized[i] = sanitizeJSONValue(path, typed[i], evidenceLimit, depth+1)
		}
		return sanitized
	case string:
		if identityJSONKey(key) {
			return redactCredentialText(typed)
		}
		return redactAndBoundText(typed, persistedTextLimit(path))
	default:
		return value
	}
}

func evidenceCollectionPath(path string) bool {
	for _, segment := range strings.Split(strings.ToLower(path), ".") {
		switch segment {
		case "tooltrajectory", "expectedtools":
			return true
		case "trace", "evidence", "rubricscores":
			return true
		}
	}
	return false
}

func evidencePayloadMapPath(path string) bool {
	hasEvidenceCollection := false
	hasPayload := false
	for _, segment := range strings.Split(strings.ToLower(path), ".") {
		switch segment {
		case "tooltrajectory", "expectedtools":
			hasEvidenceCollection = true
		case "arguments", "result":
			hasPayload = true
		}
	}
	return hasEvidenceCollection && hasPayload
}

func jsonPathKey(path string) string {
	if index := strings.LastIndexByte(path, '.'); index >= 0 {
		return path[index+1:]
	}
	return path
}

func persistedTextLimit(path string) int {
	key := jsonPathKey(path)
	if key == "prompt" ||
		(key == "value" && strings.Contains(path, ".patches.")) ||
		(key == "text" && strings.Contains(path, ".profile.overrides.")) {
		return defaultPromptTextLimit
	}
	return defaultMarkdownTextLimit
}

func identityJSONKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
	switch normalized {
	case "schemaversion", "reportid", "runid", "evaluationrunid", "promptiterrunid",
		"evalsetid", "evalcaseid", "caseid", "caseids", "metricname", "metricnames",
		"hash", "inputhashes", "profilehash", "evalsethash", "metricshash",
		"evaluatorconfighash", "metricpolicyhash", "beforeprofilehash", "afterprofilehash",
		"searchparenthash", "releasedparenthash", "targetsurfaceid", "targetsurfaceids",
		"surfaceid", "structureid", "stepid", "nodeid", "predecessorstepids",
		"appliedsurfaceids":
		return true
	default:
		return false
	}
}

func credentialKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
	switch normalized {
	case "apikey", "apikeys", "authorization", "authorizations",
		"accesstoken", "accesstokens", "refreshtoken", "refreshtokens",
		"password", "passwords", "passwd", "privatekey", "privatekeys",
		"clientsecret", "clientsecrets", "credential", "credentials",
		"secret", "secrets", "token", "tokens", "xapikey", "xapikeys":
		return true
	}
	for _, suffix := range []string{
		"apikey", "apikeys", "accesstoken", "accesstokens",
		"refreshtoken", "refreshtokens", "password", "passwords",
		"privatekey", "privatekeys", "clientsecret", "clientsecrets",
		"credential", "credentials", "secret", "secrets",
	} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	for _, suffix := range []string{"token", "tokens"} {
		if !strings.HasSuffix(normalized, suffix) {
			continue
		}
		prefix := strings.TrimSuffix(normalized, suffix)
		for _, counter := range []string{
			"input", "output", "max", "min", "total", "prompt",
			"completion", "reasoning", "cached", "used", "remaining",
		} {
			if strings.HasSuffix(prefix, counter) {
				return false
			}
		}
		return true
	}
	return false
}
