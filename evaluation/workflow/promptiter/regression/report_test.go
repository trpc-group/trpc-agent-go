//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestReportJSONAndMarkdownShareCanonicalIdentity(t *testing.T) {
	report := testReport(t)
	report.BaselineTrain.Cases[0].ExpectNoTools = true
	report.BaselineTrain.Attributions = []FailureAttribution{{
		EvalCaseID:      report.BaselineTrain.Cases[0].CaseID,
		MetricName:      "quality",
		PrimaryCategory: FailureResponseMismatch,
		Reason:          "final response differs from the expected answer",
		Severity:        FailureSeverityP2,
		Confidence:      0.9,
	}}
	jsonData, err := RenderJSON(report)
	require.NoError(t, err)
	decoded := decodeReportManifest(t, jsonData)
	require.Equal(t, report.SchemaVersion, decoded.SchemaVersion)
	require.Equal(t, report.ReportID, decoded.ReportID)
	require.Equal(t, report.RunID, decoded.RunID)
	require.Equal(t, report.StopReason, decoded.StopReason)
	require.Len(t, decoded.Candidates, len(report.Candidates))
	require.Contains(t, decoded.Profiles, decoded.ProfileRefs.Initial)
	require.Contains(t, decoded.Profiles, decoded.Candidates[0].ProfileRef)
	require.NotNil(t, decoded.Candidates[0].DeltaRefs)
	require.Equal(
		t,
		decoded.Candidates[0].DeltaRefs.VsInitial,
		decoded.Candidates[0].DeltaRefs.VsSearchParent,
	)
	delta, ok := decoded.Deltas[decoded.Candidates[0].DeltaRefs.VsReleased]
	require.True(t, ok)
	require.Equal(t, decoded.BaselineSnapshotRefs.Validation, delta.BeforeSnapshotRef)
	require.Equal(
		t,
		decoded.Candidates[0].SnapshotRefs.Validation,
		delta.AfterSnapshotRef,
	)
	baselineTrain := manifestSnapshot(
		t,
		decoded,
		decoded.BaselineSnapshotRefs.Train,
	)
	require.True(t, baselineTrain.Cases[0].ExpectNoTools)
	require.NotEmpty(t, baselineTrain.Cases[0].TraceRef)
	require.NotEmpty(t, baselineTrain.Cases[0].TraceHash)

	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	text := string(markdown)
	require.Contains(t, text, report.ReportID)
	require.Contains(t, text, report.RunID)
	require.Contains(t, text, string(report.StopReason))
	require.Contains(t, text, "Search: REJECTED")
	require.Contains(t, text, "Release: ACCEPTED")
	require.Contains(t, text, "vs_initial")
	require.Contains(t, text, report.ReleasedProfile.Prompt)
	require.Contains(t, text, "quality=0.800000")
	require.Contains(t, text, "Failed-case attribution")
	require.Contains(t, text, string(FailureResponseMismatch))
	require.Contains(t, text, "final response differs from the expected answer")
	require.Contains(t, text, "## Configuration and Provenance")
	require.Contains(t, text, "Model config")
	require.Contains(
		t,
		text,
		"hash-bound trace sidecars retain sanitized and bounded audit evidence",
	)
	require.NotContains(t, text, "retains complete")
	require.NotContains(t, text, "Final response:")
	require.NotContains(t, text, "Expected response:")
	require.NotContains(t, text, "Structured output:")
	require.NotContains(t, text, "Observed tools:")
	require.NotContains(t, text, "Expected tools:")
	require.NotContains(t, text, "Trace:")
}

func TestManifestResourceProjectionPreservesKnownZeroAndUnavailable(t *testing.T) {
	projected := projectResourceUsage(ResourceUsage{
		ModelCalls: Count{Available: true},
		InputTokens: Count{
			Available: true,
			Value:     12,
		},
		MonetaryCost: Amount{
			Available: true,
			Value:     1.25,
			Unit:      "USD",
		},
	})
	require.NotNil(t, projected.ModelCalls)
	require.Zero(t, *projected.ModelCalls)
	require.NotNil(t, projected.InputTokens)
	require.Equal(t, int64(12), *projected.InputTokens)
	require.Nil(t, projected.OutputTokens)
	require.Nil(t, projected.LatencyMS)
	require.Equal(
		t,
		&reportManifestAmount{Value: 1.25, Unit: "USD"},
		projected.MonetaryCost,
	)
}

func TestRenderMarkdownUsesDynamicFence(t *testing.T) {
	report := testReport(t)
	report.Status = PipelineRunFailed
	report.StopReason = StopNecessaryRunFailed
	report.InitialProfile.Prompt = "Explain this:\n```json\n{}\n```"
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	require.Contains(t, string(markdown), "````")
}

func TestRenderMarkdownIncludesRejectedIntermediateCandidatePrompt(t *testing.T) {
	report := testReport(t)
	report.Status = PipelineRunFailed
	report.StopReason = StopNecessaryRunFailed
	intermediatePrompt := "rejected-intermediate-prompt\napi_key=secret\n" +
		strings.Repeat("x", defaultPromptTextLimit+64) +
		"rejected-intermediate-tail-canary"
	intermediate := testProfileRecord(t, ProfileCandidate, intermediatePrompt)
	for _, lifecycle := range []ProfileRecord{
		report.InitialProfile,
		report.SearchProfile,
		report.ReleasedProfile,
	} {
		require.NotEqual(t, intermediate.Hash, lifecycle.Hash)
	}

	rejected := report.Candidates[0]
	rejected.Round = 2
	rejected.ID = "rejected-intermediate"
	rejected.Profile = &intermediate
	rejected.Patches = []PatchRecord{{
		SurfaceID: intermediate.TargetSurfaceID,
		Value:     intermediate.Prompt,
		Reason:    "candidate rejected by both policies",
	}}
	rejected.SearchDecision = Decision{
		Status:  DecisionRejected,
		Reasons: []string{"search rejected"},
	}
	rejected.ReleaseDecision = Decision{
		Status:  DecisionRejected,
		Reasons: []string{"release rejected"},
	}
	report.Candidates = append(report.Candidates, rejected)

	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	text := string(markdown)
	start := strings.Index(text, "### Round 2 — rejected-intermediate")
	require.NotEqual(t, -1, start)
	endOffset := strings.Index(text[start:], "\n## Configuration and Provenance")
	require.NotEqual(t, -1, endOffset)
	section := text[start : start+endOffset]
	require.Contains(t, section, "#### Candidate prompt")
	require.Contains(t, section, "rejected-intermediate-prompt")
	require.Contains(t, section, "api_key=[REDACTED]")
	require.NotContains(t, section, "secret")
	require.NotContains(t, section, "rejected-intermediate-tail-canary")
}

func TestValidateRenderedMarkdownRequiresExactOrderedUniqueHeadings(t *testing.T) {
	report := testReport(t)
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	valid := string(markdown)

	tests := []struct {
		name      string
		markdown  string
		wantError string
	}{
		{
			name: "wrong heading level",
			markdown: strings.Replace(
				valid,
				"## Prompt State",
				"### Prompt State",
				1,
			),
			wantError: `missing exact heading "## Prompt State"`,
		},
		{
			name: "heading has suffix",
			markdown: strings.Replace(
				valid,
				"## Candidates",
				"## Candidates extra",
				1,
			),
			wantError: `missing exact heading "## Candidates"`,
		},
		{
			name: "duplicate heading",
			markdown: strings.Replace(
				valid,
				"## Baseline Evaluations",
				"## Prompt State\n\n## Baseline Evaluations",
				1,
			),
			wantError: `heading "## Prompt State" appears 2 times`,
		},
		{
			name: "headings out of order",
			markdown: func() string {
				swapped := strings.Replace(
					valid,
					"## Configuration and Provenance",
					"## HEADING SWAP PLACEHOLDER",
					1,
				)
				swapped = strings.Replace(
					swapped,
					"## Cumulative Resources",
					"## Configuration and Provenance",
					1,
				)
				return strings.Replace(
					swapped,
					"## HEADING SWAP PLACEHOLDER",
					"## Cumulative Resources",
					1,
				)
			}(),
			wantError: `heading "## Cumulative Resources" is out of order`,
		},
		{
			name: "code block cannot spoof heading",
			markdown: strings.Replace(
				valid,
				"## Artifacts",
				"### Artifacts",
				1,
			) + "\n```\n## Artifacts\n```\n",
			wantError: `missing exact heading "## Artifacts"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRenderedMarkdown([]byte(test.markdown), report)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestValidateRenderedMarkdownRequiresCanonicalUniqueHeaderFields(t *testing.T) {
	report := testReport(t)
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	valid := string(markdown)
	fields := []string{
		fmt.Sprintf("- Schema: `%s`", markdownInline(report.SchemaVersion)),
		fmt.Sprintf("- Report: `%s`", markdownInline(report.ReportID)),
		fmt.Sprintf("- Run: `%s`", markdownInline(report.RunID)),
	}

	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			spoofed := strings.Replace(valid, field, field+" extra", 1) +
				"\n```\n" + field + "\n```\n"
			err := validateRenderedMarkdown([]byte(spoofed), report)
			require.ErrorContains(t, err, "missing exact header field")
		})
	}

	duplicated := strings.Replace(valid, fields[1], fields[1]+"\n"+fields[1], 1)
	err = validateRenderedMarkdown([]byte(duplicated), report)
	require.ErrorContains(t, err, "header field")
	require.ErrorContains(t, err, "appears 2 times")
}

func TestValidateMarkdownHeadingSequenceChecksFenceClosures(t *testing.T) {
	const heading = "# Required"

	t.Run("non-whitespace suffix does not close fence", func(t *testing.T) {
		data := "```\n```not-a-close\n" + heading + "\n```\n"
		err := validateMarkdownHeadingSequence([]byte(data), []string{heading})
		require.ErrorContains(t, err, "missing exact heading")
	})

	t.Run("whitespace suffix closes fence", func(t *testing.T) {
		data := "```\ncontent\n``` \t\n" + heading + "\n"
		require.NoError(t, validateMarkdownHeadingSequence([]byte(data), []string{heading}))
	})

	t.Run("unclosed fence at EOF", func(t *testing.T) {
		data := heading + "\n```\ncontent\n"
		err := validateMarkdownHeadingSequence([]byte(data), []string{heading})
		require.ErrorContains(t, err, "unclosed fenced code block")
	})
}

func TestRenderMarkdownOmitsRawCaseAuditPayloads(t *testing.T) {
	report := testReport(t)
	result := &report.BaselineTrain.Cases[0]
	result.FinalResponse = "raw-final-response-canary"
	result.ExpectedResponse = "raw-expected-response-canary"
	result.StructuredOutput = `{"canary":"raw-structured-output-canary"}`
	result.ExpectStructured = true
	result.Route = "raw-route-canary"
	result.ExpectedFacts = []string{"raw-expected-fact-canary"}
	result.ToolTrajectory[0].Arguments = map[string]any{
		"canary": "raw-tool-arguments-canary",
	}
	result.ToolTrajectory[0].Result = map[string]any{
		"canary": "raw-tool-result-canary",
	}
	result.Trace[0].Input = "raw-trace-input-canary"
	result.Trace[0].Output = "raw-trace-output-canary"
	report.BaselineTrain.Attributions = []FailureAttribution{{
		EvalCaseID:      result.CaseID,
		MetricName:      result.PrimaryMetric,
		PrimaryCategory: FailureResponseMismatch,
		Reason:          "concise attribution reason",
		Severity:        FailureSeverityP2,
		Confidence:      0.9,
		Evidence: []EvidenceReference{{
			Summary: "raw-attribution-evidence-canary",
		}},
	}}

	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	text := string(markdown)
	require.Contains(t, text, result.CaseID)
	require.Contains(t, text, "concise attribution reason")
	for _, omitted := range []string{
		"raw-final-response-canary",
		"raw-expected-response-canary",
		"raw-structured-output-canary",
		"raw-route-canary",
		"raw-expected-fact-canary",
		"raw-tool-arguments-canary",
		"raw-tool-result-canary",
		"raw-trace-input-canary",
		"raw-trace-output-canary",
		"raw-attribution-evidence-canary",
	} {
		require.NotContains(t, text, omitted)
	}
}

func TestRedactAndBoundEvidence(t *testing.T) {
	value := `{"api_key":"secret","authorization":"Bearer private","query":"` + string(make([]byte, 256)) + `"}`
	got := redactAndBoundText(value, 96)
	require.NotContains(t, got, "secret")
	require.NotContains(t, got, "private")
	require.LessOrEqual(t, len(got), 99)
	require.Contains(t, got, "[REDACTED]")
}

func TestRenderJSONRedactsAndBoundsPersistedEvidence(t *testing.T) {
	report := testReport(t)
	report.BaselineTrain.Cases[0].FinalResponse =
		strings.Repeat("x", defaultMarkdownTextLimit+128) + " api_key=secret"
	report.BaselineTrain.Cases[0].ToolTrajectory[0].Arguments = map[string]any{
		"authorization": "Bearer private",
		"payload":       strings.Repeat("p", defaultMarkdownTextLimit+128),
	}

	data, err := RenderJSON(report)
	require.NoError(t, err)
	decoded := decodeReportManifest(t, data)
	result := manifestSnapshot(
		t,
		decoded,
		decoded.BaselineSnapshotRefs.Train,
	).Cases[0]
	require.LessOrEqual(t, len(result.FinalResponse), defaultMarkdownTextLimit+3)
	arguments, ok := result.ToolTrajectory[0].Arguments.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED]", arguments["authorization"])
	require.LessOrEqual(t, len(arguments["payload"].(string)), defaultMarkdownTextLimit+3)
	require.NotContains(t, string(data), "private")
	require.NotContains(t, string(data), "secret")
	require.Equal(t, report.InitialProfile.Hash, decoded.ProfileRefs.Initial)
}

func TestRenderRedactsCredentialContainersPluralsAndSuffixes(t *testing.T) {
	report := testReport(t)
	report.Runtime.Model = map[string]any{
		"apiKeys": map[string]any{
			"primary": "api-primary-secret",
			"backup":  "api-backup-secret",
		},
		"openai_api_key": "openai-secret",
		"credentials": map[string]any{
			"username": "credential-user-secret",
			"region":   "credential-region-secret",
		},
		"nested": map[string]any{
			"secrets": []any{"nested-secret-one", "nested-secret-two"},
			"tokens":  map[string]any{"access": "nested-token-secret"},
		},
		"github_token":        "github-secret",
		"serviceClientSecret": "client-secret",
		"maxTokens":           4096,
	}
	report.BaselineTrain.Cases[0].FinalResponse =
		`{"credentials":{"username":"response-user-secret","region":"response-region-secret"},"safe":"visible"}`
	rebindTestReportProvenance(t, report)

	jsonData, err := RenderJSON(report)
	require.NoError(t, err)
	markdownData, err := RenderMarkdown(report)
	require.NoError(t, err)
	for _, rendered := range []string{string(jsonData), string(markdownData)} {
		for _, secret := range []string{
			"api-primary-secret",
			"api-backup-secret",
			"openai-secret",
			"credential-user-secret",
			"credential-region-secret",
			"nested-secret-one",
			"nested-secret-two",
			"nested-token-secret",
			"github-secret",
			"client-secret",
			"response-user-secret",
			"response-region-secret",
		} {
			require.NotContains(t, rendered, secret)
		}
		require.Contains(t, rendered, "[REDACTED]")
	}
	require.Contains(t, string(jsonData), "visible")
	require.NotContains(t, string(markdownData), "visible")

	decoded := decodeReportManifest(t, jsonData)
	require.Equal(t, "[REDACTED]", decoded.Runtime.Model["apiKeys"])
	require.Equal(t, "[REDACTED]", decoded.Runtime.Model["openai_api_key"])
	require.Equal(t, "[REDACTED]", decoded.Runtime.Model["credentials"])
	require.Equal(t, float64(4096), decoded.Runtime.Model["maxTokens"])
	baselineTrain := manifestSnapshot(
		t,
		decoded,
		decoded.BaselineSnapshotRefs.Train,
	)
	require.Contains(
		t,
		baselineTrain.Cases[0].FinalResponse,
		`"credentials":"[REDACTED]"`,
	)
}

func TestRenderJSONBoundsEvidenceCollectionsAndNestedArguments(t *testing.T) {
	report := testReport(t)
	report.ResolvedConfig.EvidenceLimit = 2
	tool := report.BaselineTrain.Cases[0].ToolTrajectory[0]
	tool.Arguments = map[string]any{
		"a_items":  []any{"one", "two", "three", "four"},
		"b_nested": deeplyNestedValue(32),
		"c":        1,
		"d":        2,
		"e":        3,
	}
	report.BaselineTrain.Cases[0].ToolTrajectory = []ToolCall{tool, tool, tool, tool}
	report.BaselineTrain.Attributions = []FailureAttribution{{
		EvalCaseID: "train-case",
		MetricName: "quality",
		Evidence: []EvidenceReference{
			{ID: "one", Kind: "final_response", Summary: "one"},
			{ID: "two", Kind: "trace", Summary: "two"},
			{ID: "three", Kind: "tool", Summary: "three"},
		},
	}}

	data, err := RenderJSON(report)
	require.NoError(t, err)
	decoded := decodeReportManifest(t, data)
	result := manifestSnapshot(
		t,
		decoded,
		decoded.BaselineSnapshotRefs.Train,
	).Cases[0]
	require.Len(t, result.ToolTrajectory, 2)
	require.Len(t, result.Attributions[0].Evidence, 2)
	arguments, ok := result.ToolTrajectory[0].Arguments.(map[string]any)
	require.True(t, ok)
	require.Len(t, arguments["a_items"], 2)
	require.Contains(t, arguments, "__report_truncated__")
	require.Contains(t, string(data), "[TRUNCATED: maximum nesting depth]")
}

func TestRenderJSONDoesNotTruncateCoreCollectionsAt1024(t *testing.T) {
	const collectionSize = 1025
	report := testReport(t)
	// A failed report can legitimately contain partial snapshots. Use one here
	// so this test isolates persistence loss from successful-run completeness.
	report.Status = PipelineRunFailed
	report.StopReason = StopNecessaryRunFailed
	report.Candidates = make([]CandidateReport, collectionSize)
	for i := range report.Candidates {
		report.Candidates[i] = CandidateReport{
			Round:  i + 1,
			ID:     fmt.Sprintf("candidate-%04d", i),
			Status: EvaluationNotEvaluable,
			SearchDecision: Decision{
				Status:  DecisionNotEvaluable,
				Reasons: []string{"not evaluated"},
			},
			ReleaseDecision: Decision{
				Status:  DecisionNotEvaluable,
				Reasons: []string{"not evaluated"},
			},
		}
	}
	report.BaselineTrain.Cases = make([]CaseResult, collectionSize)
	for i := 0; i < collectionSize; i++ {
		report.BaselineTrain.Cases[i] = CaseResult{
			EvalSetID: "train-set",
			CaseID:    fmt.Sprintf("case-%04d", i),
			Metrics: []MetricResult{{
				MetricName: "quality",
			}},
		}
	}
	report.BaselineTrain.Cases[0].Metrics = make([]MetricResult, collectionSize)
	for i := range report.BaselineTrain.Cases[0].Metrics {
		report.BaselineTrain.Cases[0].Metrics[i].MetricName = fmt.Sprintf("metric-%04d", i)
	}

	data, err := RenderJSON(report)
	require.NoError(t, err)
	decoded := decodeReportManifest(t, data)
	baselineTrain := manifestSnapshot(
		t,
		decoded,
		decoded.BaselineSnapshotRefs.Train,
	)
	require.Len(t, decoded.Candidates, collectionSize)
	require.Len(t, baselineTrain.Cases, collectionSize)
	require.Len(t, baselineTrain.Cases[0].Metrics, collectionSize)
	require.NotContains(t, string(data), "__report_truncated__")
}

func TestRenderedProfileHashRetainsPreSanitizationEvaluationIdentity(t *testing.T) {
	report := testReport(t)
	profile := testProfileRecord(t, ProfileCandidate, "Use api_key=secret for this candidate")
	replaceTestCandidateProfile(t, report, profile)

	data, err := RenderJSON(report)
	require.NoError(t, err)
	decoded := decodeReportManifest(t, data)
	profileRef := decoded.Candidates[0].ProfileRef
	rendered, ok := decoded.Profiles[profileRef]
	require.True(t, ok)
	require.Equal(t, profile.Hash, profileRef)
	require.NotContains(t, rendered.Prompt, "secret")
	prompt := rendered.Prompt
	sanitizedPayloadHash, err := ProfileFingerprint(&promptiter.Profile{
		StructureID: rendered.StructureID,
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: rendered.TargetSurfaceID,
			Value:     astructure.SurfaceValue{Text: &prompt},
		}},
	})
	require.NoError(t, err)
	require.NotEqual(t, profileRef, sanitizedPayloadHash)

	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	require.Contains(t, string(markdown), "before report redaction and text bounding")
	require.Contains(t, string(markdown), "not hash-reconstructive")
}

func TestWriteArtifactsValidatesPathsAndPublishesCanonicalJSONLast(t *testing.T) {
	report := testReport(t)
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "nested", "optimization_report.json")
	markdownPath := filepath.Join(dir, "nested", "optimization_report.md")
	require.NoError(t, WriteArtifacts(report, jsonPath, markdownPath))
	require.FileExists(t, jsonPath)
	require.FileExists(t, markdownPath)
	outputInfo, err := os.Stat(filepath.Dir(jsonPath))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), outputInfo.Mode().Perm())
	for _, path := range []string{jsonPath, markdownPath} {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	jsonData, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	decoded := decodeReportManifest(t, jsonData)
	require.Equal(t, report.ReportID, decoded.ReportID)
	require.NotContains(t, string(jsonData), `"trace":`)
	require.NotContains(t, string(jsonData), `"traceArtifacts"`)
	traceDir := filepath.Join(filepath.Dir(jsonPath), "traces")
	traceDirInfo, err := os.Stat(traceDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), traceDirInfo.Mode().Perm())
	traceHashes := make(map[string]string)
	passingEvidenceSeen := false
	for _, snapshot := range decoded.Snapshots {
		for _, result := range snapshot.Cases {
			if result.TraceRef == "" {
				continue
			}
			tracePath := filepath.Join(
				filepath.Dir(jsonPath),
				filepath.FromSlash(result.TraceRef),
			)
			traceData, readErr := os.ReadFile(tracePath)
			require.NoError(t, readErr)
			var sidecar reportManifestTraceSidecar
			require.NoError(t, json.Unmarshal(traceData, &sidecar))
			require.Equal(
				t,
				result.TraceHash,
				fmt.Sprintf("%x", sha256.Sum256(traceData)),
			)
			traceInfo, statErr := os.Stat(tracePath)
			require.NoError(t, statErr)
			require.Equal(t, os.FileMode(0o600), traceInfo.Mode().Perm())
			traceHashes[result.TraceRef] = result.TraceHash
			if result.Status == "passed" {
				require.NotNil(t, sidecar.PassingCaseEvidence)
				require.Equal(t, result.CaseID, sidecar.PassingCaseEvidence.CaseID)
				require.NotEmpty(t, sidecar.PassingCaseEvidence.FinalResponse)
				require.NotEmpty(t, sidecar.PassingCaseEvidence.ExpectedResponse)
				require.NotEmpty(t, sidecar.PassingCaseEvidence.ToolTrajectory)
				require.Empty(t, sidecar.PassingCaseEvidence.Trace)
				passingEvidenceSeen = true
			} else {
				require.Nil(t, sidecar.PassingCaseEvidence)
			}
		}
	}
	require.NotEmpty(t, traceHashes)
	require.True(t, passingEvidenceSeen)
	baselineTrain := manifestSnapshot(
		t,
		decoded,
		decoded.BaselineSnapshotRefs.Train,
	)
	require.Equal(
		t,
		traceHashes[baselineTrain.Cases[0].TraceRef],
		baselineTrain.Cases[0].TraceHash,
	)
	markdown, err := os.ReadFile(markdownPath)
	require.NoError(t, err)
	require.Contains(t, string(markdown), report.ReportID)

	err = WriteArtifacts(report, jsonPath, jsonPath)
	require.ErrorContains(t, err, "different")
	require.ErrorContains(t, WriteArtifacts(nil, jsonPath, markdownPath), "report is nil")
}

func TestWriteArtifactsRejectsTamperedContentAddressedTrace(t *testing.T) {
	report := testReport(t)
	_, traces, err := renderJSONBundle(report)
	require.NoError(t, err)
	require.NotEmpty(t, traces)

	dir := t.TempDir()
	traceDir := filepath.Join(dir, "traces")
	require.NoError(t, os.Mkdir(traceDir, 0o700))
	tracePath := filepath.Join(dir, filepath.FromSlash(traces[0].Ref))
	require.NoError(t, os.WriteFile(tracePath, []byte("tampered"), 0o600))

	jsonPath := filepath.Join(dir, "optimization_report.json")
	markdownPath := filepath.Join(dir, "optimization_report.md")
	err = WriteArtifacts(report, jsonPath, markdownPath)
	require.ErrorContains(t, err, "different bytes")
	require.NoFileExists(t, jsonPath)
	require.NoFileExists(t, markdownPath)
	require.Equal(t, []byte("tampered"), mustReadFile(t, tracePath))
}

func TestWriteArtifactsSanitizesPassingCaseSidecarEvidence(t *testing.T) {
	report := testReport(t)
	passing := &report.Candidates[0].Validation.Cases[0]
	passing.FinalResponse = `{"api_key":"passing-secret","answer":"visible"}`
	passing.ToolTrajectory[0].Arguments = map[string]any{
		"authorization": "Bearer private-token",
	}

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "optimization_report.json")
	require.NoError(
		t,
		WriteArtifacts(
			report,
			jsonPath,
			filepath.Join(dir, "optimization_report.md"),
		),
	)
	manifest := decodeReportManifest(t, mustReadFile(t, jsonPath))
	candidateSnapshot := manifestSnapshot(
		t,
		manifest,
		manifest.Candidates[0].SnapshotRefs.Validation,
	)
	tracePath := filepath.Join(
		dir,
		filepath.FromSlash(candidateSnapshot.Cases[0].TraceRef),
	)
	sidecar := mustReadFile(t, tracePath)
	require.NotContains(t, string(sidecar), "passing-secret")
	require.NotContains(t, string(sidecar), "private-token")
	require.Contains(t, string(sidecar), "[REDACTED]")
	require.Contains(t, string(sidecar), "visible")
}

func TestWriteArtifactsRejectsSymlinkTraceDirectory(t *testing.T) {
	report := testReport(t)
	root := t.TempDir()
	outputDir := filepath.Join(root, "output")
	realTraceDir := filepath.Join(root, "real-traces")
	require.NoError(t, os.Mkdir(outputDir, 0o700))
	require.NoError(t, os.Mkdir(realTraceDir, 0o700))
	if err := os.Symlink(realTraceDir, filepath.Join(outputDir, "traces")); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}

	jsonPath := filepath.Join(outputDir, "optimization_report.json")
	markdownPath := filepath.Join(outputDir, "optimization_report.md")
	err := WriteArtifacts(report, jsonPath, markdownPath)
	require.ErrorContains(t, err, "trace output directory must be a real directory")
	require.NoFileExists(t, jsonPath)
	require.NoFileExists(t, markdownPath)
	entries, readErr := os.ReadDir(realTraceDir)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func TestWriteArtifactsRejectsTraceDirectoryOutputCollision(t *testing.T) {
	tests := []struct {
		name         string
		jsonName     string
		markdownName string
	}{
		{
			name:         "JSON output",
			jsonName:     "traces",
			markdownName: "optimization_report.md",
		},
		{
			name:         "Markdown output",
			jsonName:     "optimization_report.json",
			markdownName: "traces",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			jsonPath := filepath.Join(dir, test.jsonName)
			markdownPath := filepath.Join(dir, test.markdownName)

			err := WriteArtifacts(testReport(t), jsonPath, markdownPath)
			require.ErrorContains(t, err, "trace output directory collides")
			require.NoFileExists(t, jsonPath)
			require.NoFileExists(t, markdownPath)
			require.NoDirExists(t, filepath.Join(dir, "traces"))
		})
	}
}

func TestWriteArtifactsRejectsRelativeAbsoluteDestinationAlias(t *testing.T) {
	report := testReport(t)
	path := filepath.Join(t.TempDir(), "report")
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	relativePath, err := filepath.Rel(workingDirectory, path)
	require.NoError(t, err)

	err = WriteArtifacts(report, relativePath, path)
	require.ErrorContains(t, err, "different")
	require.NoFileExists(t, path)
}

func TestWriteArtifactsRejectsSymlinkParentDestinationAlias(t *testing.T) {
	report := testReport(t)
	root := t.TempDir()
	realParent := filepath.Join(root, "real")
	aliasParent := filepath.Join(root, "alias")
	require.NoError(t, os.Mkdir(realParent, 0o700))
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	path := filepath.Join(realParent, "report")

	err := WriteArtifacts(report, path, filepath.Join(aliasParent, "report"))
	require.ErrorContains(t, err, "different")
	require.NoFileExists(t, path)
}

func TestWriteArtifactsUsesDestinationFilesystemCaseBehavior(t *testing.T) {
	report := testReport(t)
	dir := t.TempDir()
	caseInsensitive := testDirectoryCaseInsensitive(t, dir)
	lowerPath := filepath.Join(dir, "case-report")
	upperPath := filepath.Join(dir, "CASE-REPORT")

	err := WriteArtifacts(report, lowerPath, upperPath)
	probes, globErr := filepath.Glob(filepath.Join(dir, ".artifact-case-probe-*"))
	require.NoError(t, globErr)
	require.Empty(t, probes)
	if caseInsensitive {
		require.ErrorContains(t, err, "different")
		require.NoFileExists(t, lowerPath)
		require.NoFileExists(t, upperPath)
		return
	}
	require.NoError(t, err)
	require.FileExists(t, lowerPath)
	require.FileExists(t, upperPath)
}

func TestWriteArtifactsRejectsExactDestination(t *testing.T) {
	report := testReport(t)
	path := filepath.Join(t.TempDir(), "report")

	err := WriteArtifacts(report, path, path)
	require.ErrorContains(t, err, "different")
	require.NoFileExists(t, path)
}

func TestWriteUsesResolvedOutputNames(t *testing.T) {
	report := testReport(t)
	report.ResolvedConfig.Output = OutputConfig{
		JSON:     "custom-result.json",
		Markdown: "custom-result.md",
	}
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, report.ResolvedConfig.Output.JSON)
	markdownPath := filepath.Join(dir, report.ResolvedConfig.Output.Markdown)

	require.NoError(t, Write(report, dir))
	require.FileExists(t, jsonPath)
	require.FileExists(t, markdownPath)
	require.NoFileExists(t, filepath.Join(dir, "optimization_report.json"))
	require.NoFileExists(t, filepath.Join(dir, "optimization_report.md"))

	jsonData, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	decoded := decodeReportManifest(t, jsonData)
	expected := ArtifactReferences{JSON: jsonPath, Markdown: markdownPath}
	require.Equal(t, expected, decoded.Artifacts)
	require.Equal(t, report.ResolvedConfig.Output, decoded.ResolvedConfig.Output)

	markdownData, err := os.ReadFile(markdownPath)
	require.NoError(t, err)
	require.Contains(t, string(markdownData), jsonPath)
	require.Contains(t, string(markdownData), markdownPath)
}

func TestWriteRejectsInvalidResolvedOutputNamesWithoutEscaping(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Report, string)
	}{
		{
			name: "JSON traversal",
			configure: func(report *Report, _ string) {
				report.ResolvedConfig.Output.JSON = "../escaped.json"
			},
		},
		{
			name: "Markdown absolute path",
			configure: func(report *Report, root string) {
				report.ResolvedConfig.Output.Markdown = filepath.Join(root, "escaped.md")
			},
		},
		{
			name: "JSON current directory",
			configure: func(report *Report, _ string) {
				report.ResolvedConfig.Output.JSON = "."
			},
		},
		{
			name: "JSON parent directory",
			configure: func(report *Report, _ string) {
				report.ResolvedConfig.Output.JSON = ".."
			},
		},
		{
			name: "Markdown current directory",
			configure: func(report *Report, _ string) {
				report.ResolvedConfig.Output.Markdown = "."
			},
		},
		{
			name: "Markdown parent directory",
			configure: func(report *Report, _ string) {
				report.ResolvedConfig.Output.Markdown = ".."
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := testReport(t)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			root := t.TempDir()
			outputDir := filepath.Join(root, "output")
			test.configure(report, root)

			err := Write(report, outputDir)
			require.ErrorContains(t, err, "resolved output config")
			entries, readErr := os.ReadDir(root)
			require.NoError(t, readErr)
			require.Empty(t, entries)
		})
	}
}

func TestWriteRejectsNilReport(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "output")

	require.ErrorContains(t, Write(nil, outputDir), "report is nil")
	_, err := os.Stat(outputDir)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, Write(nil, ""), "output directory is empty")
}

func TestWriteArtifactsRestrictsOverwrittenFilesWithoutChangingExistingDirectory(t *testing.T) {
	report := testReport(t)
	dir := filepath.Join(t.TempDir(), "existing")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.Chmod(dir, 0o755))
	jsonPath := filepath.Join(dir, "optimization_report.json")
	markdownPath := filepath.Join(dir, "optimization_report.md")
	for _, path := range []string{jsonPath, markdownPath} {
		require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))
		require.NoError(t, os.Chmod(path, 0o644))
	}

	require.NoError(t, WriteArtifacts(report, jsonPath, markdownPath))

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), dirInfo.Mode().Perm())
	for _, path := range []string{jsonPath, markdownPath} {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func testDirectoryCaseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe, err := os.CreateTemp(dir, ".case-probe-*")
	require.NoError(t, err)
	probePath := probe.Name()
	require.NoError(t, probe.Close())
	t.Cleanup(func() {
		require.NoError(t, os.Remove(probePath))
	})

	probeInfo, err := os.Stat(probePath)
	require.NoError(t, err)
	foldedPath := filepath.Join(dir, strings.ToUpper(filepath.Base(probePath)))
	foldedInfo, err := os.Stat(foldedPath)
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(t, err)
	return os.SameFile(probeInfo, foldedInfo)
}

func TestWriteArtifactsReturnsPublishFailure(t *testing.T) {
	report := testReport(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("file"), 0o600))
	err := WriteArtifacts(
		report,
		filepath.Join(dir, "optimization_report.json"),
		filepath.Join(blocker, "optimization_report.md"),
	)
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(dir, "optimization_report.json"))
}

func TestRenderSuccessfulReportRequiresCanonicalBaselines(t *testing.T) {
	report := testReport(t)
	report.BaselineValidation = nil
	_, err := RenderJSON(report)
	require.ErrorContains(t, err, "baseline validation snapshot is nil")
}

func TestRenderSuccessfulReportRejectsIncompleteOrForgedAuditBindings(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Report)
		wantError string
	}{
		{
			name: "six input hashes required",
			mutate: func(report *Report) {
				delete(report.InputHashes, "baselinePrompt")
			},
			wantError: "input hash inventory",
		},
		{
			name: "runtime engine required",
			mutate: func(report *Report) {
				report.Runtime.Engine = ""
			},
			wantError: "runtime engine is empty",
		},
		{
			name: "runtime seed bound",
			mutate: func(report *Report) {
				report.Runtime.Seed++
			},
			wantError: "runtime seed",
		},
		{
			name: "resolved config valid",
			mutate: func(report *Report) {
				report.ResolvedConfig.EvidenceLimit = 0
			},
			wantError: "evidence limit",
		},
		{
			name: "baseline profile binding",
			mutate: func(report *Report) {
				report.BaselineValidation.Provenance.ProfileHash = "forged"
			},
			wantError: "profile hash",
		},
		{
			name: "displayed profile prompt binding",
			mutate: func(report *Report) {
				report.InitialProfile.Prompt = "forged"
			},
			wantError: "profile prompt",
		},
		{
			name: "snapshot run binding",
			mutate: func(report *Report) {
				report.BaselineTrain.Provenance.RunID = "forged"
			},
			wantError: "run id",
		},
		{
			name: "snapshot split binding",
			mutate: func(report *Report) {
				report.Candidates[0].Train.Provenance.Split = "heldout_validation"
			},
			wantError: "split",
		},
		{
			name: "snapshot eval set binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.EvalSetHash = "forged"
			},
			wantError: "eval set hash",
		},
		{
			name: "snapshot metrics binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.MetricsHash = "forged"
			},
			wantError: "metrics hash",
		},
		{
			name: "snapshot seed binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.Seed++
			},
			wantError: "seed",
		},
		{
			name: "snapshot evaluator binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.EvaluatorConfigHash = "forged"
			},
			wantError: "evaluator config hash",
		},
		{
			name: "snapshot metric policy binding",
			mutate: func(report *Report) {
				report.Candidates[0].Validation.Provenance.MetricPolicyHash = "forged"
			},
			wantError: "metric policy hash",
		},
		{
			name: "conflicting no-tool expectation",
			mutate: func(report *Report) {
				result := &report.BaselineTrain.Cases[0]
				result.ExpectNoTools = true
				result.ExpectedTools = []ToolCall{{Name: "lookup"}}
			},
			wantError: "explicit no-tool expectation",
		},
		{
			name: "candidate search parent binding",
			mutate: func(report *Report) {
				report.Candidates[0].SearchParentHash = "forged"
			},
			wantError: "search parent",
		},
		{
			name: "PromptIter invocation required",
			mutate: func(report *Report) {
				report.Candidates[0].PromptIterRunID = ""
			},
			wantError: "PromptIter run id",
		},
		{
			name: "PromptIter status required",
			mutate: func(report *Report) {
				report.Candidates[0].PromptIterStatus = ""
			},
			wantError: "PromptIter status",
		},
		{
			name: "completed candidate patch required",
			mutate: func(report *Report) {
				report.Candidates[0].Patches = nil
			},
			wantError: "without a PromptIter patch",
		},
		{
			name: "candidate patch bound to profile",
			mutate: func(report *Report) {
				report.Candidates[0].Patches[0].Value = "forged"
			},
			wantError: "does not match the candidate profile",
		},
		{
			name: "delta comparison label",
			mutate: func(report *Report) {
				report.Candidates[0].Deltas.VsInitial.Comparison = "forged"
			},
			wantError: "does not match its bound snapshots",
		},
		{
			name: "delta before profile hash",
			mutate: func(report *Report) {
				report.Candidates[0].Deltas.VsSearchParent.BeforeProfileHash = "forged"
			},
			wantError: "does not match its bound snapshots",
		},
		{
			name: "delta aggregate validated",
			mutate: func(report *Report) {
				report.Candidates[0].Deltas.VsReleased.NewlyPassing = 0
			},
			wantError: "does not match its bound snapshots",
		},
		{
			name: "accepted search transition",
			mutate: func(report *Report) {
				candidate := &report.Candidates[0]
				candidate.SearchDecision.Status = DecisionAccepted
				candidate.Transition.SearchUpdated = false
				candidate.Transition.SearchAfter = candidate.SearchParentHash
			},
			wantError: "search transition",
		},
		{
			name: "rejected release transition",
			mutate: func(report *Report) {
				candidate := &report.Candidates[0]
				candidate.ReleaseDecision.Status = DecisionRejected
			},
			wantError: "release transition",
		},
		{
			name: "candidate decision reasons required",
			mutate: func(report *Report) {
				report.Candidates[0].ReleaseDecision.Reasons = nil
			},
			wantError: "release decision reasons",
		},
		{
			name: "final decision reasons required",
			mutate: func(report *Report) {
				report.FinalDecision.Reasons = []string{""}
			},
			wantError: "final decision reasons",
		},
		{
			name: "negative resource entry",
			mutate: func(report *Report) {
				entry := ResourceEntry{
					Stage: "forged",
					Usage: ResourceUsage{
						ModelCalls: Count{Available: true, Value: -1},
					},
				}
				report.Resources = ResourceLedger{
					Entries:    []ResourceEntry{entry},
					Cumulative: entry.Usage,
				}
			},
			wantError: "model calls must be non-negative",
		},
		{
			name: "unavailable resource has value",
			mutate: func(report *Report) {
				entry := ResourceEntry{
					Stage: "forged",
					Usage: ResourceUsage{
						InputTokens: Count{Value: 1},
					},
				}
				report.Resources = ResourceLedger{
					Entries:    []ResourceEntry{entry},
					Cumulative: entry.Usage,
				}
			},
			wantError: "unavailable input tokens has a value",
		},
		{
			name: "global resource cumulative",
			mutate: func(report *Report) {
				report.Resources.Cumulative.ModelCalls = Count{
					Available: true,
					Value:     1,
				}
			},
			wantError: "cumulative resources do not equal",
		},
		{
			name: "candidate resource entry global subset",
			mutate: func(report *Report) {
				entry := ResourceEntry{
					Stage: "candidate-only",
					Round: 1,
					Usage: ResourceUsage{
						ModelCalls: Count{Available: true, Value: 1},
					},
				}
				report.Candidates[0].Resources = ResourceLedger{
					Entries:    []ResourceEntry{entry},
					Cumulative: entry.Usage,
				}
			},
			wantError: "absent from the global ledger",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := testReport(t)
			test.mutate(report)
			_, err := RenderJSON(report)
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestRenderRejectsInvalidResourcesForFailedReport(t *testing.T) {
	report := testReport(t)
	report.Status = PipelineRunFailed
	report.StopReason = StopNecessaryRunFailed
	entry := ResourceEntry{
		Stage: "failed",
		Usage: ResourceUsage{
			ModelCalls: Count{Available: true, Value: -1},
		},
	}
	report.Resources = ResourceLedger{
		Entries:    []ResourceEntry{entry},
		Cumulative: entry.Usage,
	}

	_, err := RenderJSON(report)
	require.ErrorContains(t, err, "model calls must be non-negative")
}

func TestRenderAllowsCompletedEvaluationWithNotEvaluableGateAndNoPointerUpdates(t *testing.T) {
	report := testReport(t)
	candidate := &report.Candidates[0]
	candidate.SearchDecision = Decision{
		Status:  DecisionNotEvaluable,
		Reasons: []string{"resource measurement unavailable"},
	}
	candidate.ReleaseDecision = Decision{
		Status:  DecisionNotEvaluable,
		Reasons: []string{"resource measurement unavailable"},
	}
	candidate.Transition = StateTransition{
		SearchBefore:   candidate.SearchParentHash,
		SearchAfter:    candidate.SearchParentHash,
		ReleasedBefore: candidate.ReleasedParentHash,
		ReleasedAfter:  candidate.ReleasedParentHash,
		Explanation:    "not-evaluable decision leaves both pointers unchanged",
	}
	released := report.InitialProfile
	released.Role = ProfileReleased
	report.ReleasedProfile = released
	report.FinalDecision = Decision{
		Status:  DecisionNotEvaluable,
		Reasons: []string{"resource measurement unavailable"},
	}
	_, err := RenderJSON(report)
	require.NoError(t, err)

	candidate.Transition.SearchUpdated = true
	_, err = RenderJSON(report)
	require.ErrorContains(t, err, "not-evaluable decision that updates a profile pointer")

	candidate.Transition.SearchUpdated = false
	candidate.Transition.ReleasedAfter = candidate.Profile.Hash
	_, err = RenderJSON(report)
	require.ErrorContains(t, err, "not-evaluable decision that updates a profile pointer")
}

func decodeReportManifest(t *testing.T, data []byte) reportManifest {
	t.Helper()
	var manifest reportManifest
	require.NoError(t, json.Unmarshal(data, &manifest))
	require.Equal(
		t,
		reportArtifactFormatVersion,
		manifest.ArtifactFormatVersion,
	)
	return manifest
}

func manifestSnapshot(
	t *testing.T,
	manifest reportManifest,
	ref string,
) reportManifestSnapshot {
	t.Helper()
	require.NotEmpty(t, ref)
	snapshot, ok := manifest.Snapshots[ref]
	require.Truef(t, ok, "snapshot ref %q is absent", ref)
	return snapshot
}

func testReport(t *testing.T) *Report {
	t.Helper()
	const (
		seed          = int64(2003)
		trainSplit    = "train"
		heldoutSplit  = "heldout_validation"
		targetSurface = "agent#instruction"
	)
	initial := testProfileRecord(t, ProfileInitial, "baseline prompt")
	search := initial
	search.Role = ProfileSearch
	candidate := testProfileRecord(t, ProfileCandidate, "candidate prompt")
	released := candidate
	released.Role = ProfileReleased
	metricsHash := hashStrings("test-metrics")
	train := DatasetSpec{
		EvalSetID:   "train-set",
		EvalSetHash: hashStrings("train-set"),
		MetricsHash: metricsHash,
		CaseIDs:     []string{"train-case"},
		MetricNames: []string{"quality"},
		NormalizedInputHashes: map[string]string{
			"train-case": hashStrings("train-input"),
		},
	}
	validation := DatasetSpec{
		EvalSetID:   "validation-set",
		EvalSetHash: hashStrings("validation-set"),
		MetricsHash: metricsHash,
		CaseIDs:     []string{"validation-case"},
		MetricNames: []string{"quality"},
		NormalizedInputHashes: map[string]string{
			"validation-case": hashStrings("validation-input"),
		},
	}
	gate := GatePolicy{
		PrimaryMetric: "quality",
		MetricDirections: map[string]ScoreDirection{
			"quality": ScoreHigherIsBetter,
		},
		Epsilon:               1e-9,
		MinValidationGain:     0.1,
		NoNewHardFailures:     true,
		NoCriticalRegressions: true,
	}
	runtime := RuntimeConfig{
		Engine:    "deterministic-fake",
		Seed:      seed,
		Model:     map[string]any{"name": "fake-model"},
		Evaluator: map[string]any{"name": "deterministic-evaluator"},
	}
	inputHashes := map[string]string{
		"trainEvalSet":      train.EvalSetHash,
		"validationEvalSet": validation.EvalSetHash,
		"metrics":           metricsHash,
		"baselinePrompt":    hashStrings("baseline-prompt"),
		"promptIterConfig":  hashStrings("promptiter-config"),
		"regressionConfig":  hashStrings("regression-config"),
	}
	metricPolicyHash := testMetricPolicyHash(t, inputHashes, gate)
	evaluatorConfigHash := testEvaluatorConfigHash(
		t,
		runtime,
		train,
		validation,
		metricPolicyHash,
	)
	baselineTrain := testReportSnapshot(
		initial.Hash,
		train,
		trainSplit,
		"run-2003/baseline_train",
		0.4,
		false,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	baselineValidation := testReportSnapshot(
		initial.Hash,
		validation,
		heldoutSplit,
		"run-2003/baseline_validation",
		0.4,
		false,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	candidateTrain := testReportSnapshot(
		candidate.Hash,
		train,
		trainSplit,
		"run-2003/candidate_train/1",
		0.8,
		true,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	candidateValidation := testReportSnapshot(
		candidate.Hash,
		validation,
		heldoutSplit,
		"run-2003/candidate_validation/1",
		0.8,
		true,
		seed,
		evaluatorConfigHash,
		metricPolicyHash,
	)
	initial.EvaluationRunID = baselineValidation.Provenance.RunID
	search.EvaluationRunID = baselineValidation.Provenance.RunID
	candidate.EvaluationRunID = candidateValidation.Provenance.RunID
	released.EvaluationRunID = candidateValidation.Provenance.RunID
	vsInitial, err := CalculateDelta("vs_initial", baselineValidation, candidateValidation, gate)
	require.NoError(t, err)
	vsSearchParent, err := CalculateDelta(
		"vs_search_parent",
		baselineValidation,
		candidateValidation,
		gate,
	)
	require.NoError(t, err)
	vsReleased, err := CalculateDelta(
		"vs_released",
		baselineValidation,
		candidateValidation,
		gate,
	)
	require.NoError(t, err)
	releaseScoreDelta := vsReleased.ScoreDelta
	resolved := ResolvedConfig{
		Seed:       seed,
		Train:      train,
		Validation: validation,
		PromptIter: PromptIterPolicy{
			MaxOuterRounds:             1,
			SearchMinScoreGain:         0.1,
			InternalValidationStrategy: internalValidationTrainAll,
			TargetSurfaceID:            targetSurface,
		},
		Gate:          gate,
		Output:        OutputConfig{JSON: "optimization_report.json", Markdown: "optimization_report.md"},
		EvidenceLimit: 20,
	}
	return &Report{
		SchemaVersion:      SchemaVersion,
		ReportID:           "report-2003",
		RunID:              "run-2003",
		GeneratedAt:        time.Unix(0, 0).UTC(),
		Status:             PipelineSucceeded,
		StopReason:         StopMaxRounds,
		ResolvedConfig:     resolved,
		InputHashes:        inputHashes,
		InitialProfile:     initial,
		SearchProfile:      search,
		ReleasedProfile:    released,
		BaselineTrain:      baselineTrain,
		BaselineValidation: baselineValidation,
		Candidates: []CandidateReport{{
			Round:              1,
			ID:                 "candidate-1",
			Status:             EvaluationCompleted,
			SearchParentHash:   initial.Hash,
			ReleasedParentHash: initial.Hash,
			Profile:            &candidate,
			Patches: []PatchRecord{{
				SurfaceID: targetSurface,
				Value:     "candidate prompt",
				Reason:    "improve deterministic quality",
			}},
			OptimizationReason: "improve deterministic quality",
			PromptIterRunID:    "run-2003/promptiter/1",
			PromptIterStatus:   "succeeded",
			Train:              candidateTrain,
			Validation:         candidateValidation,
			Deltas: &DeltaSet{
				VsInitial:      vsInitial,
				VsSearchParent: vsSearchParent,
				VsReleased:     vsReleased,
			},
			SearchDecision: Decision{Status: DecisionRejected, Reasons: []string{"training objective did not improve"}},
			ReleaseDecision: Decision{
				Status:     DecisionAccepted,
				Reasons:    []string{"held-out release gates passed"},
				ScoreDelta: &releaseScoreDelta,
			},
			Transition: StateTransition{
				SearchBefore: initial.Hash, SearchAfter: initial.Hash,
				ReleasedBefore: initial.Hash, ReleasedAfter: candidate.Hash,
				ReleaseUpdated: true,
				Explanation:    "candidate advanced released profile only",
			},
		}},
		FinalDecision: Decision{Status: DecisionAccepted, Reasons: []string{"candidate-1 released"}},
		Runtime:       runtime,
	}
}

func testProfileRecord(t *testing.T, role ProfileRole, prompt string) ProfileRecord {
	t.Helper()
	value := prompt
	profile := &promptiter.Profile{
		StructureID: "structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value:     astructure.SurfaceValue{Text: &value},
		}},
	}
	hash, err := ProfileFingerprint(profile)
	require.NoError(t, err)
	return ProfileRecord{
		Role:            role,
		Hash:            hash,
		StructureID:     "structure",
		TargetSurfaceID: "agent#instruction",
		Prompt:          prompt,
		Profile:         profile,
	}
}

func testReportSnapshot(
	profileHash string,
	dataset DatasetSpec,
	split string,
	runID string,
	score float64,
	passed bool,
	seed int64,
	evaluatorConfigHash string,
	metricPolicyHash string,
) *EvaluationSnapshot {
	status := "failed"
	if passed {
		status = "passed"
	}
	return &EvaluationSnapshot{
		Status: EvaluationCompleted,
		Provenance: EvaluationProvenance{
			RunID:               runID,
			ProfileHash:         profileHash,
			EvalSetID:           dataset.EvalSetID,
			EvalSetHash:         dataset.EvalSetHash,
			MetricsHash:         dataset.MetricsHash,
			Split:               split,
			Seed:                seed,
			EvaluatorConfigHash: evaluatorConfigHash,
			MetricPolicyHash:    metricPolicyHash,
		},
		Inventory: ExpectedInventory{
			CaseIDs:     append([]string(nil), dataset.CaseIDs...),
			MetricNames: append([]string(nil), dataset.MetricNames...),
		},
		OverallScore: score,
		Passed:       boolInt(passed),
		Failed:       boolInt(!passed),
		Cases: []CaseResult{{
			EvalSetID:        dataset.EvalSetID,
			CaseID:           dataset.CaseIDs[0],
			Status:           status,
			Passed:           passed,
			PrimaryMetric:    "quality",
			FinalResponse:    `{"answer":"observed"}`,
			ExpectedResponse: `{"answer":"expected"}`,
			ToolTrajectory: []ToolCall{{
				Sequence: 1,
				Name:     "lookup",
				Arguments: map[string]any{
					"query": "weather",
				},
				Result: map[string]any{"temperature": 25},
			}},
			Trace: []TraceStep{{
				StepID: "step-1",
				NodeID: "router",
				Branch: "lookup",
				Input:  "weather",
				Output: "25",
			}},
			Metrics: []MetricResult{{
				MetricName: "quality",
				Score:      score,
				Status:     status,
				Passed:     passed,
				Threshold:  0.7,
				Direction:  ScoreHigherIsBetter,
				Reason:     "deterministic rubric",
			}},
		}},
	}
}

func testMetricPolicyHash(
	t *testing.T,
	inputHashes map[string]string,
	gate GatePolicy,
) string {
	t.Helper()
	gateJSON, err := json.Marshal(gate)
	require.NoError(t, err)
	return hashStrings(
		"native-metric-policy-v1",
		inputHashes["metrics"],
		string(gateJSON),
	)
}

func testEvaluatorConfigHash(
	t *testing.T,
	runtime RuntimeConfig,
	train DatasetSpec,
	validation DatasetSpec,
	metricPolicyHash string,
) string {
	t.Helper()
	runtimeHash, err := RuntimeConfigFingerprint(runtime)
	require.NoError(t, err)
	return hashStrings(
		"runtime-bound-evaluator-v1",
		train.EvalSetHash,
		validation.EvalSetHash,
		metricPolicyHash,
		runtimeHash,
	)
}

func rebindTestReportProvenance(t *testing.T, report *Report) {
	t.Helper()
	metricPolicyHash := testMetricPolicyHash(
		t,
		report.InputHashes,
		report.ResolvedConfig.Gate,
	)
	evaluatorConfigHash := testEvaluatorConfigHash(
		t,
		report.Runtime,
		report.ResolvedConfig.Train,
		report.ResolvedConfig.Validation,
		metricPolicyHash,
	)
	for _, snapshot := range []*EvaluationSnapshot{
		report.BaselineTrain,
		report.BaselineValidation,
		report.Candidates[0].Train,
		report.Candidates[0].Validation,
	} {
		snapshot.Provenance.MetricPolicyHash = metricPolicyHash
		snapshot.Provenance.EvaluatorConfigHash = evaluatorConfigHash
	}
}

func replaceTestCandidateProfile(
	t *testing.T,
	report *Report,
	profile ProfileRecord,
) {
	t.Helper()
	candidate := &report.Candidates[0]
	profile.EvaluationRunID = candidate.Validation.Provenance.RunID
	candidate.Profile = &profile
	candidate.Patches[0].Value = profile.Prompt
	candidate.Train.Provenance.ProfileHash = profile.Hash
	candidate.Validation.Provenance.ProfileHash = profile.Hash
	vsInitial, err := CalculateDelta(
		"vs_initial",
		report.BaselineValidation,
		candidate.Validation,
		report.ResolvedConfig.Gate,
	)
	require.NoError(t, err)
	vsSearchParent, err := CalculateDelta(
		"vs_search_parent",
		report.BaselineValidation,
		candidate.Validation,
		report.ResolvedConfig.Gate,
	)
	require.NoError(t, err)
	vsReleased, err := CalculateDelta(
		"vs_released",
		report.BaselineValidation,
		candidate.Validation,
		report.ResolvedConfig.Gate,
	)
	require.NoError(t, err)
	candidate.Deltas = &DeltaSet{
		VsInitial:      vsInitial,
		VsSearchParent: vsSearchParent,
		VsReleased:     vsReleased,
	}
	candidate.ReleaseDecision.ScoreDelta = &candidate.Deltas.VsReleased.ScoreDelta
	candidate.Transition.ReleasedAfter = profile.Hash
	released := profile
	released.Role = ProfileReleased
	report.ReleasedProfile = released
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func deeplyNestedValue(depth int) any {
	value := any("leaf")
	for index := 0; index < depth; index++ {
		value = map[string]any{"next": value}
	}
	return value
}
