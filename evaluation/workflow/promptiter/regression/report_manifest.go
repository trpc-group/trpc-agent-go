//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const reportArtifactFormatVersion = "promptiter-regression-manifest/v1"

const (
	baselineTrainSnapshotRef      = "baseline-train"
	baselineValidationSnapshotRef = "baseline-validation"
)

// reportManifest is the persisted projection of Report. SchemaVersion describes
// the regression domain model; ArtifactFormatVersion independently versions this
// storage layout.
type reportManifest struct {
	ArtifactFormatVersion string                                `json:"artifactFormatVersion"`
	SchemaVersion         string                                `json:"schemaVersion"`
	ReportID              string                                `json:"reportId"`
	RunID                 string                                `json:"runId"`
	GeneratedAt           time.Time                             `json:"generatedAt"`
	Status                PipelineStatus                        `json:"status"`
	StopReason            StopReason                            `json:"stopReason"`
	ResolvedConfig        ResolvedConfig                        `json:"resolvedConfig"`
	InputHashes           map[string]string                     `json:"inputHashes"`
	Runtime               RuntimeConfig                         `json:"runtime"`
	Profiles              map[string]reportManifestProfile      `json:"profiles"`
	ProfileRefs           reportManifestProfileRefs             `json:"profileRefs"`
	BaselineSnapshotRefs  reportManifestSnapshotRefs            `json:"baselineSnapshotRefs"`
	Snapshots             map[string]reportManifestSnapshot     `json:"snapshots"`
	Deltas                map[string]reportManifestDeltaSummary `json:"deltas,omitempty"`
	Candidates            []reportManifestCandidate             `json:"candidates"`
	FinalDecision         Decision                              `json:"finalDecision"`
	Resources             reportManifestResources               `json:"resources"`
	Errors                []string                              `json:"errors,omitempty"`
	Artifacts             ArtifactReferences                    `json:"artifacts"`
}

type reportManifestProfileRefs struct {
	Initial  string `json:"initial,omitempty"`
	Search   string `json:"search,omitempty"`
	Released string `json:"released,omitempty"`
}

type reportManifestProfile struct {
	StructureID     string `json:"structureId"`
	TargetSurfaceID string `json:"targetSurfaceId"`
	Prompt          string `json:"prompt"`
}

type reportManifestSnapshotRefs struct {
	Train      string `json:"train,omitempty"`
	Validation string `json:"validation,omitempty"`
}

type reportManifestSnapshot struct {
	Status       EvaluationStatus                 `json:"status"`
	ProfileRef   string                           `json:"profileRef"`
	Provenance   reportManifestSnapshotProvenance `json:"provenance"`
	OverallScore float64                          `json:"overallScore"`
	Passed       int                              `json:"passed"`
	Failed       int                              `json:"failed"`
	Cases        []reportManifestCase             `json:"cases"`
	Resources    reportManifestResourceUsage      `json:"resources"`
	LatencyMS    int64                            `json:"latencyMs"`
	Error        string                           `json:"error,omitempty"`
}

type reportManifestSnapshotProvenance struct {
	RunID               string `json:"runId"`
	EvalSetID           string `json:"evalSetId"`
	EvalSetHash         string `json:"evalSetHash"`
	MetricsHash         string `json:"metricsHash"`
	Split               string `json:"split"`
	Seed                int64  `json:"seed"`
	EvaluatorConfigHash string `json:"evaluatorConfigHash"`
	MetricPolicyHash    string `json:"metricPolicyHash"`
}

// reportManifestCase keeps passing cases deliberately small. Failure-only
// fields are populated only when the source CaseResult did not pass.
type reportManifestCase struct {
	CaseID           string                      `json:"caseId"`
	Status           string                      `json:"status"`
	HardFailure      bool                        `json:"hardFailure,omitempty"`
	Critical         bool                        `json:"critical,omitempty"`
	Metrics          []reportManifestMetric      `json:"metrics"`
	FinalResponse    string                      `json:"finalResponse,omitempty"`
	ExpectedResponse string                      `json:"expectedResponse,omitempty"`
	StructuredOutput string                      `json:"structuredOutput,omitempty"`
	ExpectStructured bool                        `json:"expectStructured,omitempty"`
	ToolTrajectory   []ToolCall                  `json:"toolTrajectory,omitempty"`
	ExpectedTools    []ToolCall                  `json:"expectedTools,omitempty"`
	ExpectNoTools    bool                        `json:"expectNoTools,omitempty"`
	Route            string                      `json:"route,omitempty"`
	ExpectedRoute    string                      `json:"expectedRoute,omitempty"`
	ExpectedFacts    []string                    `json:"expectedFacts,omitempty"`
	Error            string                      `json:"error,omitempty"`
	Attributions     []reportManifestAttribution `json:"attributions,omitempty"`
	TraceRef         string                      `json:"traceRef,omitempty"`
	TraceHash        string                      `json:"traceHash,omitempty"`
}

type reportManifestAttribution struct {
	MetricName          string              `json:"metricName"`
	PrimaryCategory     FailureCategory     `json:"primaryCategory"`
	SecondaryCategories []FailureCategory   `json:"secondaryCategories,omitempty"`
	Reason              string              `json:"reason"`
	Evidence            []EvidenceReference `json:"evidence"`
	Severity            FailureSeverity     `json:"severity"`
	Confidence          float64             `json:"confidence"`
	EvidenceSufficiency EvidenceSufficiency `json:"evidenceSufficiency"`
}

type reportManifestMetric struct {
	MetricName   string         `json:"metricName"`
	Score        float64        `json:"score"`
	Status       string         `json:"status"`
	Threshold    *float64       `json:"threshold,omitempty"`
	Direction    ScoreDirection `json:"direction,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	RubricScores []RubricScore  `json:"rubricScores,omitempty"`
}

type reportManifestCandidate struct {
	Round              int                         `json:"round"`
	ID                 string                      `json:"id"`
	Status             EvaluationStatus            `json:"status"`
	SearchParentHash   string                      `json:"searchParentHash"`
	ReleasedParentHash string                      `json:"releasedParentHash"`
	ProfileRef         string                      `json:"profileRef,omitempty"`
	Patches            []reportManifestPatch       `json:"patches,omitempty"`
	OptimizationReason string                      `json:"optimizationReason,omitempty"`
	PromptIterRunID    string                      `json:"promptIterRunId,omitempty"`
	PromptIterStatus   string                      `json:"promptIterStatus,omitempty"`
	SnapshotRefs       reportManifestSnapshotRefs  `json:"snapshotRefs"`
	DeltaRefs          *reportManifestDeltaRefs    `json:"deltaRefs,omitempty"`
	SearchDecision     Decision                    `json:"searchDecision"`
	ReleaseDecision    Decision                    `json:"releaseDecision"`
	Transition         StateTransition             `json:"transition"`
	ResourceDelta      reportManifestResourceUsage `json:"resourceDelta"`
	Errors             []string                    `json:"errors,omitempty"`
}

// reportManifestPatch omits Value because the exact candidate prompt is stored
// once in Profiles and addressed by ProfileRef.
type reportManifestPatch struct {
	SurfaceID string `json:"surfaceId"`
	Reason    string `json:"reason"`
}

type reportManifestDeltaRefs struct {
	VsInitial      string `json:"vsInitial"`
	VsSearchParent string `json:"vsSearchParent"`
	VsReleased     string `json:"vsReleased"`
}

type reportManifestDeltaSummary struct {
	BeforeSnapshotRef  string                    `json:"beforeSnapshotRef"`
	AfterSnapshotRef   string                    `json:"afterSnapshotRef"`
	PrimaryMetric      string                    `json:"primaryMetric"`
	BeforeOverallScore float64                   `json:"beforeOverallScore"`
	AfterOverallScore  float64                   `json:"afterOverallScore"`
	ScoreDelta         float64                   `json:"scoreDelta"`
	NewlyPassing       int                       `json:"newlyPassing"`
	NewlyFailing       int                       `json:"newlyFailing"`
	Improved           int                       `json:"improved"`
	Regressed          int                       `json:"regressed"`
	Unchanged          int                       `json:"unchanged"`
	Cases              []reportManifestCaseDelta `json:"cases"`
}

type reportManifestCaseDelta struct {
	CaseID     string     `json:"caseId"`
	ScoreDelta float64    `json:"scoreDelta"`
	Change     ChangeKind `json:"change"`
}

type reportManifestResources struct {
	Cumulative reportManifestResourceUsage `json:"cumulative"`
}

// reportManifestResourceUsage uses absence for an unavailable measurement and
// a scalar for an available count, including known zero.
type reportManifestResourceUsage struct {
	ModelCalls   *int64                `json:"modelCalls,omitempty"`
	InputTokens  *int64                `json:"inputTokens,omitempty"`
	OutputTokens *int64                `json:"outputTokens,omitempty"`
	LatencyMS    *int64                `json:"latencyMs,omitempty"`
	MonetaryCost *reportManifestAmount `json:"monetaryCost,omitempty"`
}

type reportManifestAmount struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

type reportManifestTraceSidecar struct {
	ArtifactFormatVersion string      `json:"artifactFormatVersion"`
	Trace                 []TraceStep `json:"trace,omitempty"`
	PassingCaseEvidence   *CaseResult `json:"passingCaseEvidence,omitempty"`
}

func projectResourceUsage(source ResourceUsage) reportManifestResourceUsage {
	projected := reportManifestResourceUsage{
		ModelCalls:   projectCount(source.ModelCalls),
		InputTokens:  projectCount(source.InputTokens),
		OutputTokens: projectCount(source.OutputTokens),
		LatencyMS:    projectCount(source.LatencyMS),
	}
	if source.MonetaryCost.Available {
		projected.MonetaryCost = &reportManifestAmount{
			Value: source.MonetaryCost.Value,
			Unit:  source.MonetaryCost.Unit,
		}
	}
	return projected
}

func projectCount(source Count) *int64 {
	if !source.Available {
		return nil
	}
	value := source.Value
	return &value
}

// traceArtifact is one content-addressed trace sidecar ready to be persisted.
// Ref is always a manifest-relative forward-slash path derived only from SHA256.
type traceArtifact struct {
	Ref    string
	SHA256 string
	Data   []byte
}

// buildReportManifest projects an already-sanitized report into the persisted
// manifest and content-addressed trace sidecars. It intentionally does not call
// validateReport because sanitized profile payloads retain their original,
// pre-sanitization hashes.
func buildReportManifest(report *Report) (*reportManifest, []traceArtifact, error) {
	if report == nil {
		return nil, nil, errors.New("report is nil")
	}

	builder := reportManifestBuilder{
		manifest: &reportManifest{
			ArtifactFormatVersion: reportArtifactFormatVersion,
			SchemaVersion:         report.SchemaVersion,
			ReportID:              report.ReportID,
			RunID:                 report.RunID,
			GeneratedAt:           report.GeneratedAt,
			Status:                report.Status,
			StopReason:            report.StopReason,
			ResolvedConfig:        report.ResolvedConfig,
			InputHashes:           report.InputHashes,
			Runtime:               report.Runtime,
			Profiles:              make(map[string]reportManifestProfile),
			Snapshots:             make(map[string]reportManifestSnapshot),
			Deltas:                make(map[string]reportManifestDeltaSummary),
			Candidates:            make([]reportManifestCandidate, 0, len(report.Candidates)),
			FinalDecision:         report.FinalDecision,
			Resources: reportManifestResources{
				Cumulative: projectResourceUsage(report.Resources.Cumulative),
			},
			Errors:    report.Errors,
			Artifacts: report.Artifacts,
		},
		traceByHash: make(map[string]int),
	}

	var err error
	builder.manifest.ProfileRefs.Initial, err = builder.addProfile(&report.InitialProfile)
	if err != nil {
		return nil, nil, fmt.Errorf("add initial profile: %w", err)
	}
	builder.manifest.ProfileRefs.Search, err = builder.addProfile(&report.SearchProfile)
	if err != nil {
		return nil, nil, fmt.Errorf("add search profile: %w", err)
	}
	builder.manifest.ProfileRefs.Released, err = builder.addProfile(&report.ReleasedProfile)
	if err != nil {
		return nil, nil, fmt.Errorf("add released profile: %w", err)
	}

	builder.manifest.BaselineSnapshotRefs.Train, err = builder.addSnapshot(
		baselineTrainSnapshotRef,
		report.BaselineTrain,
	)
	if err != nil {
		return nil, nil, err
	}
	builder.manifest.BaselineSnapshotRefs.Validation, err = builder.addSnapshot(
		baselineValidationSnapshotRef,
		report.BaselineValidation,
	)
	if err != nil {
		return nil, nil, err
	}

	searchSnapshotRef := builder.manifest.BaselineSnapshotRefs.Validation
	releasedSnapshotRef := builder.manifest.BaselineSnapshotRefs.Validation
	for i := range report.Candidates {
		candidate, candidateValidationRef, err := builder.buildCandidate(
			&report.Candidates[i],
			searchSnapshotRef,
			releasedSnapshotRef,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("build candidate %d: %w", i+1, err)
		}
		builder.manifest.Candidates = append(builder.manifest.Candidates, candidate)
		if report.Candidates[i].Transition.SearchUpdated {
			if candidateValidationRef == "" {
				return nil, nil, fmt.Errorf(
					"candidate %d updates search without a validation snapshot",
					i+1,
				)
			}
			searchSnapshotRef = candidateValidationRef
		}
		if report.Candidates[i].Transition.ReleaseUpdated {
			if candidateValidationRef == "" {
				return nil, nil, fmt.Errorf(
					"candidate %d updates release without a validation snapshot",
					i+1,
				)
			}
			releasedSnapshotRef = candidateValidationRef
		}
	}

	return builder.manifest, builder.traceArtifacts, nil
}

// marshalReportManifest returns deterministic, one-space-indented JSON with a
// trailing newline.
func marshalReportManifest(manifest *reportManifest) ([]byte, error) {
	if manifest == nil {
		return nil, errors.New("report manifest is nil")
	}
	data, err := marshalManifestJSON(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal report manifest: %w", err)
	}
	return data, nil
}

func marshalManifestJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

type reportManifestBuilder struct {
	manifest       *reportManifest
	traceArtifacts []traceArtifact
	traceByHash    map[string]int
}

func (b *reportManifestBuilder) addProfile(record *ProfileRecord) (string, error) {
	if record == nil || strings.TrimSpace(record.Hash) == "" {
		return "", nil
	}
	projected := reportManifestProfile{
		StructureID:     record.StructureID,
		TargetSurfaceID: record.TargetSurfaceID,
		Prompt:          record.Prompt,
	}
	if existing, ok := b.manifest.Profiles[record.Hash]; ok {
		if !reflect.DeepEqual(existing, projected) {
			return "", fmt.Errorf(
				"profile hash %q identifies different sanitized payloads",
				record.Hash,
			)
		}
		return record.Hash, nil
	}
	b.manifest.Profiles[record.Hash] = projected
	return record.Hash, nil
}

func (b *reportManifestBuilder) addSnapshot(
	ref string,
	snapshot *EvaluationSnapshot,
) (string, error) {
	if snapshot == nil {
		return "", nil
	}
	if _, exists := b.manifest.Snapshots[ref]; exists {
		return "", fmt.Errorf("duplicate logical snapshot ref %q", ref)
	}

	cases := make([]reportManifestCase, 0, len(snapshot.Cases))
	for i := range snapshot.Cases {
		projected, err := b.buildCase(&snapshot.Cases[i], snapshot.Attributions)
		if err != nil {
			return "", fmt.Errorf(
				"build snapshot %q case %q: %w",
				ref,
				snapshot.Cases[i].CaseID,
				err,
			)
		}
		cases = append(cases, projected)
	}
	b.manifest.Snapshots[ref] = reportManifestSnapshot{
		Status:     snapshot.Status,
		ProfileRef: snapshot.Provenance.ProfileHash,
		Provenance: reportManifestSnapshotProvenance{
			RunID:               snapshot.Provenance.RunID,
			EvalSetID:           snapshot.Provenance.EvalSetID,
			EvalSetHash:         snapshot.Provenance.EvalSetHash,
			MetricsHash:         snapshot.Provenance.MetricsHash,
			Split:               snapshot.Provenance.Split,
			Seed:                snapshot.Provenance.Seed,
			EvaluatorConfigHash: snapshot.Provenance.EvaluatorConfigHash,
			MetricPolicyHash:    snapshot.Provenance.MetricPolicyHash,
		},
		OverallScore: snapshot.OverallScore,
		Passed:       snapshot.Passed,
		Failed:       snapshot.Failed,
		Cases:        cases,
		Resources:    projectResourceUsage(snapshot.Resources),
		LatencyMS:    snapshot.LatencyMS,
		Error:        snapshot.Error,
	}
	return ref, nil
}

func (b *reportManifestBuilder) buildCase(
	result *CaseResult,
	attributions []FailureAttribution,
) (reportManifestCase, error) {
	projected := reportManifestCase{
		CaseID:        result.CaseID,
		Status:        result.Status,
		HardFailure:   result.HardFailure,
		Critical:      result.Critical,
		Metrics:       make([]reportManifestMetric, 0, len(result.Metrics)),
		ExpectNoTools: result.ExpectNoTools,
	}
	for _, metric := range result.Metrics {
		item := reportManifestMetric{
			MetricName: metric.MetricName,
			Score:      metric.Score,
			Status:     metric.Status,
		}
		if !result.Passed {
			threshold := metric.Threshold
			item.Threshold = &threshold
			item.Direction = metric.Direction
			item.Reason = metric.Reason
			item.RubricScores = metric.RubricScores
		}
		projected.Metrics = append(projected.Metrics, item)
	}
	if len(result.Trace) > 0 || result.Passed {
		ref, hash, err := b.addTrace(result)
		if err != nil {
			return reportManifestCase{}, err
		}
		projected.TraceRef = ref
		projected.TraceHash = hash
	}
	if result.Passed {
		return projected, nil
	}

	projected.FinalResponse = result.FinalResponse
	projected.ExpectedResponse = result.ExpectedResponse
	projected.StructuredOutput = result.StructuredOutput
	projected.ExpectStructured = result.ExpectStructured
	projected.ToolTrajectory = result.ToolTrajectory
	projected.ExpectedTools = result.ExpectedTools
	projected.Route = result.Route
	projected.ExpectedRoute = result.ExpectedRoute
	projected.ExpectedFacts = result.ExpectedFacts
	projected.Error = result.Error
	for _, attribution := range attributions {
		if attribution.EvalCaseID == result.CaseID {
			projected.Attributions = append(
				projected.Attributions,
				reportManifestAttribution{
					MetricName:          attribution.MetricName,
					PrimaryCategory:     attribution.PrimaryCategory,
					SecondaryCategories: attribution.SecondaryCategories,
					Reason:              attribution.Reason,
					Evidence:            attribution.Evidence,
					Severity:            attribution.Severity,
					Confidence:          attribution.Confidence,
					EvidenceSufficiency: attribution.EvidenceSufficiency,
				},
			)
		}
	}
	return projected, nil
}

func (b *reportManifestBuilder) addTrace(result *CaseResult) (string, string, error) {
	sidecar := reportManifestTraceSidecar{
		ArtifactFormatVersion: reportArtifactFormatVersion,
		Trace:                 result.Trace,
	}
	if result.Passed {
		evidence := *result
		evidence.Trace = nil
		sidecar.PassingCaseEvidence = &evidence
	}
	data, err := json.Marshal(sidecar)
	if err != nil {
		return "", "", fmt.Errorf("marshal trace sidecar: %w", err)
	}
	data = append(data, '\n')
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	ref := "traces/" + hash + ".json"
	if index, exists := b.traceByHash[hash]; exists {
		if !bytes.Equal(b.traceArtifacts[index].Data, data) {
			return "", "", fmt.Errorf("trace SHA-256 collision for %q", hash)
		}
		return ref, hash, nil
	}
	b.traceByHash[hash] = len(b.traceArtifacts)
	b.traceArtifacts = append(b.traceArtifacts, traceArtifact{
		Ref:    ref,
		SHA256: hash,
		Data:   data,
	})
	return ref, hash, nil
}

func (b *reportManifestBuilder) buildCandidate(
	source *CandidateReport,
	searchSnapshotRef string,
	releasedSnapshotRef string,
) (reportManifestCandidate, string, error) {
	profileRef, err := b.addProfile(source.Profile)
	if err != nil {
		return reportManifestCandidate{}, "", err
	}
	trainRef := fmt.Sprintf("candidate-%02d-train", source.Round)
	trainRef, err = b.addSnapshot(trainRef, source.Train)
	if err != nil {
		return reportManifestCandidate{}, "", err
	}
	validationRef := fmt.Sprintf("candidate-%02d-validation", source.Round)
	validationRef, err = b.addSnapshot(validationRef, source.Validation)
	if err != nil {
		return reportManifestCandidate{}, "", err
	}

	patches := make([]reportManifestPatch, 0, len(source.Patches))
	for _, patch := range source.Patches {
		patches = append(patches, reportManifestPatch{
			SurfaceID: patch.SurfaceID,
			Reason:    patch.Reason,
		})
	}
	projected := reportManifestCandidate{
		Round:              source.Round,
		ID:                 source.ID,
		Status:             source.Status,
		SearchParentHash:   source.SearchParentHash,
		ReleasedParentHash: source.ReleasedParentHash,
		ProfileRef:         profileRef,
		Patches:            patches,
		OptimizationReason: source.OptimizationReason,
		PromptIterRunID:    source.PromptIterRunID,
		PromptIterStatus:   source.PromptIterStatus,
		SnapshotRefs: reportManifestSnapshotRefs{
			Train:      trainRef,
			Validation: validationRef,
		},
		SearchDecision:  source.SearchDecision,
		ReleaseDecision: source.ReleaseDecision,
		Transition:      source.Transition,
		ResourceDelta:   projectResourceUsage(source.Resources.Cumulative),
		Errors:          source.Errors,
	}
	if source.Deltas != nil {
		if validationRef == "" {
			return reportManifestCandidate{}, "", errors.New(
				"candidate deltas have no validation snapshot",
			)
		}
		projected.DeltaRefs, err = b.addDeltaRefs(
			source.Deltas,
			b.manifest.BaselineSnapshotRefs.Validation,
			searchSnapshotRef,
			releasedSnapshotRef,
			validationRef,
		)
		if err != nil {
			return reportManifestCandidate{}, "", err
		}
	}
	return projected, validationRef, nil
}

func (b *reportManifestBuilder) addDeltaRefs(
	source *DeltaSet,
	initialSnapshotRef string,
	searchSnapshotRef string,
	releasedSnapshotRef string,
	afterSnapshotRef string,
) (*reportManifestDeltaRefs, error) {
	primaryMetric := b.manifest.ResolvedConfig.Gate.PrimaryMetric
	vsInitial, err := b.addDelta(
		&source.VsInitial,
		primaryMetric,
		initialSnapshotRef,
		afterSnapshotRef,
	)
	if err != nil {
		return nil, fmt.Errorf("build vsInitial delta: %w", err)
	}
	vsSearchParent, err := b.addDelta(
		&source.VsSearchParent,
		primaryMetric,
		searchSnapshotRef,
		afterSnapshotRef,
	)
	if err != nil {
		return nil, fmt.Errorf("build vsSearchParent delta: %w", err)
	}
	vsReleased, err := b.addDelta(
		&source.VsReleased,
		primaryMetric,
		releasedSnapshotRef,
		afterSnapshotRef,
	)
	if err != nil {
		return nil, fmt.Errorf("build vsReleased delta: %w", err)
	}
	return &reportManifestDeltaRefs{
		VsInitial:      vsInitial,
		VsSearchParent: vsSearchParent,
		VsReleased:     vsReleased,
	}, nil
}

func (b *reportManifestBuilder) addDelta(
	source *DeltaSummary,
	primaryMetric string,
	beforeSnapshotRef string,
	afterSnapshotRef string,
) (string, error) {
	if beforeSnapshotRef == "" || afterSnapshotRef == "" {
		return "", errors.New(
			"delta is missing a before or after snapshot ref",
		)
	}
	projected := reportManifestDeltaSummary{
		BeforeSnapshotRef:  beforeSnapshotRef,
		AfterSnapshotRef:   afterSnapshotRef,
		PrimaryMetric:      primaryMetric,
		BeforeOverallScore: source.BeforeOverallScore,
		AfterOverallScore:  source.AfterOverallScore,
		ScoreDelta:         source.ScoreDelta,
		NewlyPassing:       source.NewlyPassing,
		NewlyFailing:       source.NewlyFailing,
		Improved:           source.Improved,
		Regressed:          source.Regressed,
		Unchanged:          source.Unchanged,
		Cases:              make([]reportManifestCaseDelta, 0, len(source.Cases)),
	}
	for _, item := range source.Cases {
		metric, err := primaryMetricDelta(item.Metrics, primaryMetric)
		if err != nil {
			return "", fmt.Errorf(
				"case %q: %w",
				item.CaseID,
				err,
			)
		}
		caseDelta := reportManifestCaseDelta{
			CaseID:     item.CaseID,
			ScoreDelta: metric.Delta,
			Change:     item.PrimaryKind,
		}
		projected.Cases = append(projected.Cases, caseDelta)
	}
	ref := beforeSnapshotRef + "--" + afterSnapshotRef
	if existing, ok := b.manifest.Deltas[ref]; ok {
		if !reflect.DeepEqual(existing, projected) {
			return "", fmt.Errorf(
				"delta ref %q identifies different snapshot comparisons",
				ref,
			)
		}
		return ref, nil
	}
	b.manifest.Deltas[ref] = projected
	return ref, nil
}

func validateTraceArtifact(artifact traceArtifact) error {
	if len(artifact.Data) == 0 {
		return errors.New("trace artifact data is empty")
	}
	sum := sha256.Sum256(artifact.Data)
	hash := hex.EncodeToString(sum[:])
	if artifact.SHA256 != hash {
		return fmt.Errorf(
			"trace artifact hash %q does not match bytes %q",
			artifact.SHA256,
			hash,
		)
	}
	expectedRef := "traces/" + hash + ".json"
	if artifact.Ref != expectedRef {
		return fmt.Errorf(
			"trace artifact ref %q does not match content address %q",
			artifact.Ref,
			expectedRef,
		)
	}
	return nil
}

func primaryMetricDelta(metrics []MetricDelta, primaryMetric string) (MetricDelta, error) {
	var found *MetricDelta
	for i := range metrics {
		if metrics[i].MetricName != primaryMetric {
			continue
		}
		if found != nil {
			return MetricDelta{}, fmt.Errorf(
				"primary metric %q appears more than once",
				primaryMetric,
			)
		}
		found = &metrics[i]
	}
	if found == nil {
		return MetricDelta{}, fmt.Errorf(
			"primary metric %q is absent",
			primaryMetric,
		)
	}
	return *found, nil
}
