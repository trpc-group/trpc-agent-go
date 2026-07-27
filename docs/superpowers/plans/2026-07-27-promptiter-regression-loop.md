# PromptIter Regression Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a compact example-only Evaluation + PromptIter regression loop with deterministic and live model modes, held-out release gating, and auditable artifacts.

**Architecture:** All feature code stays in one unexported `main` package under `examples/evaluation/promptiter/regressionloop`. Both modes use the real Agent, Evaluation Service, metric registry, PromptIter Engine, Profile compiler, and tool-matching semantics; only model construction differs. PromptIter receives training data for both search inputs, while a separate outer Evaluation call reserves held-out validation exclusively for release decisions.

**Tech Stack:** Go, tRPC-Agent-Go Evaluation Service, PromptIter Engine, local evalset/metric managers, OpenAI-compatible model adapter, standard-library JSON/templates/filesystem, Testify.

---

## File Map

All new Go files use the Tencent Apache 2.0 header from `CONTRIBUTING.md`.

- `config.go`: strict configuration decoding, effective role configuration, path and threshold validation.
- `catalog.go`: load exact input bytes, validate native evalset/metric identity, and build content fingerprints.
- `types.go`: example-local audit, evaluation, attribution, delta, gate, and usage types.
- `normalize.go`: project real `evaluation.EvaluationResult` without dropping error-only or incomplete cases.
- `attribution.go`: metric-scoped primary attribution and secondary evidence.
- `delta.go`: shape-complete case and metric comparison.
- `gate.go`: fail-closed release decision over one immutable held-out snapshot.
- `ledger.go`: cumulative model/tool call accounting.
- `report.go`: stable JSON and Markdown rendering.
- `artifact.go`: staged `0700`/`0600` artifact-bundle publication.
- `model.go`: counted model wrapper and live role construction.
- `fake_model.go`: deterministic responses at the `model.Model` boundary.
- `agent.go`: candidate, judge, backwarder, aggregator, optimizer Agents.
- `runtime.go`: local managers, runners, Evaluation Service, PromptIter Engine ownership.
- `pipeline.go`: baseline, one-round search, held-out validation, gate, state transition, audit.
- `main.go`: CLI and exit behavior.
- `data/`: baseline prompt, three train cases, three held-out validation cases, metrics, configuration.
- `output/`: deterministic JSON and Markdown example reports plus accepted Profile.
- `DESIGN.md`: 300–500 Chinese-character issue deliverable.
- `README.md`: deterministic and live usage, security, and report interpretation.

## Task 1: Strict Configuration and Input Fingerprints

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/config_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/config.go`
- Create: `examples/evaluation/promptiter/regressionloop/catalog_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/catalog.go`

- [ ] **Step 1: Write failing strict-decoding and zero-budget tests**

```go
func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := writeTestFile(t, `{"mode":"deterministic","maxModelCalls":0,"maxModelCallz":1}`)
	_, err := loadConfig(path)
	require.ErrorContains(t, err, "unknown field")
}

func TestLoadConfigPreservesExplicitZeroBudget(t *testing.T) {
	path := writeTestFile(t, `{"mode":"deterministic","maxModelCalls":0}`)
	cfg, err := loadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.MaxModelCalls)
	assert.Zero(t, *cfg.MaxModelCalls)
}

func TestValidateConfigRejectsHeldOutSearchInput(t *testing.T) {
	cfg := validConfig()
	cfg.SearchEvalSetID = cfg.ValidationEvalSetID
	require.ErrorContains(t, cfg.validate(), "held-out validation")
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestLoadConfig|TestValidateConfig' -count=1`

Expected: FAIL because `loadConfig`, `config`, and `validConfig` do not exist.

- [ ] **Step 3: Add the exact configuration contracts**

```go
type runMode string

const (
	modeDeterministic runMode = "deterministic"
	modeLive          runMode = "live"
)

type roleConfig struct {
	Model     string  `json:"model"`
	BaseURL   string  `json:"baseURL"`
	APIKeyEnv string  `json:"apiKeyEnv"`
	InputPerM *float64 `json:"inputPerMillion,omitempty"`
	OutputPerM *float64 `json:"outputPerMillion,omitempty"`
}

type criticalRule struct {
	EvalCaseID  string   `json:"evalCaseId"`
	MetricName  string   `json:"metricName,omitempty"`
	MustPass    bool     `json:"mustPass"`
	MinScore    *float64 `json:"minScore,omitempty"`
	MaxScoreDrop *float64 `json:"maxScoreDrop,omitempty"`
}

type config struct {
	Mode                     runMode       `json:"mode"`
	Seed                     int64         `json:"seed"`
	MaxRounds                int           `json:"maxRounds"`
	TrainEvalSetID           string        `json:"trainEvalSetId"`
	SearchEvalSetID          string        `json:"searchEvalSetId"`
	ValidationEvalSetID      string        `json:"validationEvalSetId"`
	MetricFileID             string        `json:"metricFileId"`
	MinValidationGain        float64       `json:"minValidationGain"`
	MaxHardFailures          int           `json:"maxHardFailures"`
	MaxCaseScoreDrop         float64       `json:"maxCaseScoreDrop"`
	MaxModelCalls            *int          `json:"maxModelCalls,omitempty"`
	MaxToolCalls             *int          `json:"maxToolCalls,omitempty"`
	MaxTokens                *int          `json:"maxTokens,omitempty"`
	MaxEstimatedCost         *float64      `json:"maxEstimatedCost,omitempty"`
	MaxLatencyMillis         *int64        `json:"maxLatencyMillis,omitempty"`
	Critical                 []criticalRule `json:"critical,omitempty"`
	EvalCaseParallelism      int           `json:"evalCaseParallelism"`
	ParallelInference        bool          `json:"parallelInference"`
	ParallelEvaluation       bool          `json:"parallelEvaluation"`
	Candidate                roleConfig    `json:"candidate"`
	Judge                    roleConfig    `json:"judge"`
	Worker                   roleConfig    `json:"worker"`
}

func loadConfig(path string) (*config, error) {
	f, err := os.Open(path)
	if err != nil { return nil, fmt.Errorf("open config: %w", err) }
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var cfg config
	if err := dec.Decode(&cfg); err != nil { return nil, fmt.Errorf("decode config: %w", err) }
	if err := dec.Decode(new(any)); err != io.EOF { return nil, errors.New("decode config: trailing JSON value") }
	if err := cfg.validate(); err != nil { return nil, err }
	return &cfg, nil
}
```

Implement `validate` with explicit checks: supported mode; positive rounds and parallelism; non-empty IDs; `SearchEvalSetID != ValidationEvalSetID`; search ID equals the train ID for this two-set example; nonnegative gain/drop/budgets; critical rules have at least one independent condition; live roles require model, API-key environment name, and explicit Base URL for non-default credentials.

- [ ] **Step 4: Add catalog and fingerprint tests, then implementation**

```go
func TestBuildCatalogRejectsNullAndDuplicateCases(t *testing.T) {
	_, err := buildCatalog([]byte(`{"evalSetId":"v","evalCases":[null,{"evalId":"x"},{"evalId":"x"}]}`), []byte(`[]`))
	require.Error(t, err)
}

func TestFingerprintChangesWithExecutedInput(t *testing.T) {
	a := fingerprintInputs([]byte("prompt-a"), []byte("train"), []byte("validation"), []byte("metrics"), []byte("config"))
	b := fingerprintInputs([]byte("prompt-b"), []byte("train"), []byte("validation"), []byte("metrics"), []byte("config"))
	assert.NotEqual(t, a, b)
}
```

Implement `catalog` with `EvalSetID`, ordered case IDs, ordered metric names, and a `map[resultKey]struct{}` where `resultKey` contains `EvalSetID`, `EvalCaseID`, and `MetricName`. Decode using the existing `evalset.EvalSet` and `[]metric.EvalMetric` shapes rather than parallel JSON structs. Hash the exact loaded bytes with SHA-256 and hex encoding.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestLoadConfig|TestValidateConfig|TestBuildCatalog|TestFingerprint' -count=1`

Expected: PASS.

```bash
git add examples/evaluation/promptiter/regressionloop/{config.go,config_test.go,catalog.go,catalog_test.go}
git commit -m "feat(evaluation): validate regression inputs"
```

## Task 2: Lossless Evaluation Projection

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/types.go`
- Create: `examples/evaluation/promptiter/regressionloop/normalize_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/normalize.go`

- [ ] **Step 1: Write failure-shape tests**

```go
func TestNormalizeEvaluationKeepsExecutionOnlyFailure(t *testing.T) {
	input := &evaluation.EvaluationResult{EvalSetID: "validation", EvalCases: []*evaluation.EvaluationCaseResult{{
		EvalCaseID: "critical",
		OverallStatus: status.EvalStatusFailed,
		RunDetails: []*evaluation.EvaluationCaseRunDetails{{Inference: &evaluation.EvaluationInferenceDetails{
			Status: status.EvalStatusFailed, ErrorMessage: "provider timeout",
		}}},
	}}}
	got, err := normalizeEvaluation(input, validationCatalog("critical", "quality"))
	require.NoError(t, err)
	require.Len(t, got.Cases, 1)
	assert.Equal(t, status.EvalStatusFailed, got.Cases[0].Metrics[0].Status)
	assert.Equal(t, "provider timeout", got.Cases[0].ExecutionError)
}

func TestNormalizeEvaluationRejectsAggregateOnlyAndNotEvaluated(t *testing.T) {
	_, err := normalizeEvaluation(&evaluation.EvaluationResult{EvalSetID: "validation"}, validationCatalog("critical", "quality"))
	require.ErrorContains(t, err, "missing case")
}
```

- [ ] **Step 2: Run RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run TestNormalizeEvaluation -count=1`

Expected: FAIL because normalization contracts do not exist.

- [ ] **Step 3: Define stable local result types**

```go
type metricResult struct { Name string; Score float64; Threshold float64; Status status.EvalStatus; Reason string }
type caseResult struct { EvalSetID string; EvalCaseID string; Status status.EvalStatus; Score float64; ExecutionError string; Metrics []metricResult; RunDetails []*evaluation.EvaluationCaseRunDetails }
type evaluationSnapshot struct { EvalSetID string; Status status.EvalStatus; Score float64; Cases []caseResult; Duration time.Duration }

func (s evaluationSnapshot) index() map[resultKey]metricResult {
	out := make(map[resultKey]metricResult)
	for _, c := range s.Cases { for _, m := range c.Metrics { out[resultKey{c.EvalSetID, c.EvalCaseID, m.Name}] = m } }
	return out
}
```

- [ ] **Step 4: Implement normalization**

For every catalog key, find the matching case and metric. Read reasons first from the single retained `EvalCaseResults[0].OverallEvalMetricResults`, then fall back to the aggregate metric. If inference or run errors exist without metrics, materialize a failed metric with score zero and the execution error. Reject nil cases, duplicate identities, missing/extra identities, `unknown`, and `not_evaluated`. Compute weighted score only after exact shape validation.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run TestNormalizeEvaluation -count=1`

Expected: PASS.

```bash
git add examples/evaluation/promptiter/regressionloop/{types.go,normalize.go,normalize_test.go}
git commit -m "feat(evaluation): preserve regression evidence"
```

## Task 3: Attribution, Delta, and Fail-Closed Gate

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/attribution_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/attribution.go`
- Create: `examples/evaluation/promptiter/regressionloop/delta_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/delta.go`
- Create: `examples/evaluation/promptiter/regressionloop/gate_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/gate.go`

- [ ] **Step 1: Write exact attribution tests**

```go
func TestAttributeUsesMetricScopedToolEvidence(t *testing.T) {
	c := failedCase("final_response", "answer mismatch")
	c.RunDetails = differingToolTrajectory()
	got := attributeCase(c)
	assert.Equal(t, attributionFinalResponseMismatch, got.Primary.Category)
}

func TestAttributeIncompleteEvaluation(t *testing.T) {
	c := failedCase("quality", "")
	c.Metrics[0].Status = status.EvalStatusNotEvaluated
	assert.Equal(t, attributionEvaluationIncomplete, attributeCase(c).Primary.Category)
}
```

Define `attributionCategory`, `attribution`, and `caseAttribution`. Primary selection order is runtime, failed tool metric, format, route, retrieval, final response, incomplete, unclassified. Secondary evidence never changes primary counts.

- [ ] **Step 2: Write delta and gate tests**

```go
func TestGateRejectsMissingEvidenceRetainedHardFailAndCriticalDrop(t *testing.T) {
	decision := decide(gateInput{
		Policy: gatePolicy{MinGain: 0.1, MaxHardFailures: 0, Critical: []criticalRule{{EvalCaseID:"critical", MustPass:true}}},
		Baseline: snapshot(0.5, passing("critical")),
		Candidate: snapshot(0.8, failing("critical")),
		Usage: usageSummary{ModelCalls: knownInt(1)},
	})
	assert.False(t, decision.Accepted)
	assert.ElementsMatch(t, []string{"hard_failures", "critical_case"}, failedCheckIDs(decision))
}

func TestGateTreatsExplicitZeroAsLimit(t *testing.T) {
	zero := 0
	d := decide(gateInput{Policy: gatePolicy{MaxModelCalls:&zero}, Baseline: completeBaseline(), Candidate: completeCandidate(), Usage: usageSummary{ModelCalls:knownInt(1)}})
	assert.False(t, d.Accepted)
}
```

- [ ] **Step 3: Run RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestAttribute|TestGate' -count=1`

Expected: FAIL because attribution, delta, and gate functions do not exist.

- [ ] **Step 4: Implement pure decisions**

Define delta kinds `new_pass`, `new_failure`, `improved`, `regressed`, `unchanged_pass`, and `unchanged_failure`. `compareSnapshots` rejects shape differences instead of scoring them. `decide` creates stable checks named `run_status`, `evidence_shape`, `minimum_gain`, `hard_failures`, `critical_case`, `case_drop`, `model_calls`, `tool_calls`, `tokens`, `estimated_cost`, and `latency`; acceptance is the conjunction of all enabled checks. The score used by `minimum_gain` is the same weighted score written into the report.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestAttribute|TestCompare|TestGate' -count=1`

Expected: PASS.

```bash
git add examples/evaluation/promptiter/regressionloop/{attribution.go,attribution_test.go,delta.go,delta_test.go,gate.go,gate_test.go}
git commit -m "feat(evaluation): gate prompt regressions"
```

## Task 4: Cumulative Resource Ledger

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/ledger_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/ledger.go`
- Create: `examples/evaluation/promptiter/regressionloop/model_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/model.go`

- [ ] **Step 1: Write cumulative and unknown-usage tests**

```go
func TestLedgerChargesRejectedRoundsAndRetries(t *testing.T) {
	l := newLedger()
	l.record(modelCall{Stage:"baseline", Role:"candidate", PromptTokens:10, CompletionTokens:5})
	l.record(modelCall{Stage:"round-1", Role:"worker", PromptTokens:4, CompletionTokens:2})
	l.record(modelCall{Stage:"round-2", Role:"worker", PromptTokens:4, CompletionTokens:2})
	assert.Equal(t, 3, l.snapshot().ModelCalls.Value)
	assert.Equal(t, 27, l.snapshot().Tokens.Value)
}

func TestCountedModelMarksMissingUsageUnknown(t *testing.T) {
	base := &scriptedModel{responses: []*model.Response{{Done:true}}}
	counted := newCountedModel("candidate", "baseline", base, newLedger(), pricing{})
	drainModel(t, counted)
	assert.False(t, counted.ledger.snapshot().Tokens.Known)
}
```

- [ ] **Step 2: Run RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestLedger|TestCountedModel' -count=1`

Expected: FAIL because ledger and wrapper types do not exist.

- [ ] **Step 3: Implement ledger and counted model**

Use mutex-protected `measurement[T]{Known bool; Value T}` fields. The counted model increments calls before delegating, wraps the response channel, records every final response usage, records function-level and response-level errors, and measures wall duration. Never infer a model call from case count. Cost uses the effective role pricing captured at construction.

- [ ] **Step 4: Add budget reservation**

Implement `ledger.canReserve(reservation usageSummary, policy gatePolicy) error` using projected cumulative values. Call it before candidate generation and again before held-out validation. Explicit zero limits reject nonzero reservations.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestLedger|TestCountedModel|TestReserve' -count=1 -race`

Expected: PASS with no race report.

```bash
git add examples/evaluation/promptiter/regressionloop/{ledger.go,ledger_test.go,model.go,model_test.go}
git commit -m "feat(evaluation): audit regression usage"
```

## Task 5: Safe Reports and Atomic Artifact Bundle

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/report_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/report.go`
- Create: `examples/evaluation/promptiter/regressionloop/artifact_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/artifact.go`

- [ ] **Step 1: Write Markdown injection and artifact rollback tests**

```go
func TestRenderMarkdownEscapesDynamicValues(t *testing.T) {
	r := minimalReport()
	r.Rounds[0].Delta.Cases[0].EvalCaseID = "case|\n## Decision: ACCEPT"
	r.Rounds[0].CandidatePrompt = "before\n```\nafter"
	md, err := renderMarkdown(r)
	require.NoError(t, err)
	assert.Contains(t, md, `case\| ## Decision: ACCEPT`)
	assert.Contains(t, md, "````text")
}

func TestPublishBundleDoesNotLeaveCandidateOnFailure(t *testing.T) {
	out := t.TempDir()
	err := publishBundle(out, acceptedReport(), invalidProfileForJSON())
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(out, "run-1", "candidate_profile.json"))
	assert.ErrorIs(t, statErr, fs.ErrNotExist)
}
```

- [ ] **Step 2: Run RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestRenderMarkdown|TestPublishBundle' -count=1`

Expected: FAIL because report and publication functions do not exist.

- [ ] **Step 3: Implement stable report rendering**

Define report schema constant `1`, report/run/round models, stable sorting by evalset/case/metric/check ID, JSON indentation, Markdown table-cell escaping, and a code fence one backtick longer than the longest prompt backtick run. The report stores effective models/endpoints, input hashes, StructureID, seed, pricing, cumulative usage, every attempt, and primary attribution counts.

- [ ] **Step 4: Implement staged publication**

Create a sibling staging directory with `0700`, write files with `0600`, sync and close every file, verify JSON parses and candidate Profile exists only for accepted succeeded runs, then rename the staging directory to `output/<run-id>`. Reject absolute, parent-traversal, symlink-escape, and colliding paths. On failure remove staging and preserve an existing completed bundle.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestRender|TestPublish|TestArtifact' -count=1`

Expected: PASS.

```bash
git add examples/evaluation/promptiter/regressionloop/{report.go,report_test.go,artifact.go,artifact_test.go}
git commit -m "feat(evaluation): publish regression audits"
```

## Task 6: Deterministic and Live Runtime Assembly

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/fake_model_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/fake_model.go`
- Create: `examples/evaluation/promptiter/regressionloop/agent.go`
- Create: `examples/evaluation/promptiter/regressionloop/runtime_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/runtime.go`

- [ ] **Step 1: Write model-boundary and credential-routing tests**

```go
func TestDeterministicRuntimeUsesRealPromptIterStages(t *testing.T) {
	rt := buildTestRuntime(t, modeDeterministic)
	assert.NotNil(t, rt.engine)
	assert.NotNil(t, rt.evaluator)
	assert.NotNil(t, rt.backwarder)
	assert.NotNil(t, rt.aggregator)
	assert.NotNil(t, rt.optimizer)
}

func TestLiveRoleRequiresExplicitEndpointForGenericCredential(t *testing.T) {
	t.Setenv("CUSTOM_KEY", "secret")
	_, err := newLiveModel("candidate", roleConfig{Model:"custom", APIKeyEnv:"CUSTOM_KEY"}, newLedger())
	require.ErrorContains(t, err, "base URL")
}
```

- [ ] **Step 2: Run RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestDeterministicRuntime|TestLiveRole' -count=1`

Expected: FAIL because runtime assembly does not exist.

- [ ] **Step 3: Implement fake models at the existing boundary**

Implement one thread-safe scripted `model.Model` per role. Candidate responses depend on the effective instruction marker and eval case input. Backwarder, aggregator, optimizer, and judge models return valid structured JSON expected by the existing stage Agents. Optimizer call sequence yields balanced, ineffective, and overfit complete instruction patches. Responses include deterministic usage and no wall-clock timestamps.

- [ ] **Step 4: Assemble real framework components**

Use `llmagent.New`, `runner.NewRunner`, `evaluation.New`, local evalset/metric/result managers, `backwarder.New`, `aggregator.New`, `optimizer.New`, and `promptiterengine.New`. Construct live models with `openai.New(name, openai.WithAPIKey(os.Getenv(env)), openai.WithBaseURL(baseURL))`, wrapped by counted models. Set generation temperature to zero. Runtime owns and closes every evaluator and runner exactly once.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestDeterministicRuntime|TestLiveRole|TestRuntimeClose' -count=1 -race`

Expected: PASS.

```bash
git add examples/evaluation/promptiter/regressionloop/{fake_model.go,fake_model_test.go,agent.go,runtime.go,runtime_test.go}
git commit -m "feat(evaluation): assemble regression runtime"
```

## Task 7: TDD the Outer Pipeline and State Machine

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/pipeline_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/pipeline.go`

- [ ] **Step 1: Write held-out isolation and state tests**

```go
func TestPipelineNeverPassesHeldOutValidationToPromptIter(t *testing.T) {
	spy := &engineSpy{}
	p := testPipeline(t, spy)
	_, err := p.run(context.Background())
	require.NoError(t, err)
	for _, req := range spy.requests {
		assert.Equal(t, "train", req.Train[0].EvalSetID)
		assert.Equal(t, "train", req.Validation[0].EvalSetID)
		assert.NotEqual(t, "validation", req.Validation[0].EvalSetID)
		assert.Equal(t, 1, req.MaxRounds)
	}
}

func TestRejectedCandidateDoesNotAdvanceReleasedOrSearchProfile(t *testing.T) {
	result := runScenario(t, "overfit")
	assert.Equal(t, result.InitialProfile, result.ReleasedProfile)
	assert.Equal(t, result.InitialProfile, result.SearchProfile)
	assert.False(t, result.Rounds[0].Gate.Accepted)
}
```

- [ ] **Step 2: Run RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestPipeline|TestRejectedCandidate' -count=1`

Expected: FAIL because pipeline state does not exist.

- [ ] **Step 3: Implement baseline and one-round search**

`pipeline.run` performs preflight, initial Profile normalization through `engine.Describe`, baseline train evaluation, baseline held-out evaluation, then up to configured outer rounds. Every PromptIter request uses train for `Train` and `Validation`, `MaxRounds: 1`, current search Profile, target surface ID, parallel options, and training-only LossHints. Ignore engine acceptance for release.

- [ ] **Step 4: Implement held-out release transition**

For every non-nil OutputProfile, independently call the Evaluation Service with the held-out evalset and full Profile run options, normalize one immutable snapshot, calculate delta against released Profile, run gate with cumulative ledger, append round audit, then advance both search and released Profiles only on acceptance. Canceled, failed, incomplete, budget-exhausted, and rejected attempts remain audited and cannot publish a candidate.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run 'TestPipeline|TestRejectedCandidate|TestAcceptedCandidate|TestCanceledRun' -count=1 -race`

Expected: PASS.

```bash
git add examples/evaluation/promptiter/regressionloop/{pipeline.go,pipeline_test.go}
git commit -m "feat(evaluation): close prompt regression loop"
```

## Task 8: Six Cases, CLI, and End-to-End Reports

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/data/regression-app/train.evalset.json`
- Create: `examples/evaluation/promptiter/regressionloop/data/regression-app/validation.evalset.json`
- Create: `examples/evaluation/promptiter/regressionloop/data/regression-app/regression.metrics.json`
- Create: `examples/evaluation/promptiter/regressionloop/data/baseline_prompt.txt`
- Create: `examples/evaluation/promptiter/regressionloop/data/promptiter.json`
- Create: `examples/evaluation/promptiter/regressionloop/main_test.go`
- Create: `examples/evaluation/promptiter/regressionloop/main.go`

- [ ] **Step 1: Add six compact customer-support cases**

Train cases: return-window instruction can improve; tool-argument instruction can improve; knowledge-gap case remains failing. Held-out cases: shipping-return case improves; account-security critical case regresses under the overfit patch; JSON-format case detects format regression. Each file uses the repository's native `evalSetId`, `evalCases`, `conversation`, and `sessionInput` schema. Metrics use registered final-response, structured JSON, and tool-trajectory criteria; no example-local scoring schema is added.

- [ ] **Step 2: Write the failing end-to-end test**

```go
func TestDeterministicEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out := t.TempDir()
	result, err := runCLI(ctx, cliOptions{Config:"data/promptiter.json", DataDir:"data", OutputDir:out, RunID:"golden"})
	require.NoError(t, err)
	require.Len(t, result.Rounds, 3)
	assert.True(t, result.Rounds[0].Gate.Accepted)
	assert.False(t, result.Rounds[1].Gate.Accepted)
	assert.False(t, result.Rounds[2].Gate.Accepted)
	assert.Equal(t, "critical_case", firstFailedCheck(result.Rounds[2].Gate))
	assert.FileExists(t, filepath.Join(out, "golden", "candidate_profile.json"))
}
```

- [ ] **Step 3: Run RED**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run TestDeterministicEndToEnd -count=1`

Expected: FAIL because CLI and data files do not exist.

- [ ] **Step 4: Implement CLI**

Use a dedicated `flag.FlagSet`, reject positional arguments, require explicit config/data/output paths, apply an overall context timeout, print run ID, baseline/released scores, round decisions, artifact path, and return a nonzero error for failed/incomplete runs. A gate rejection is an audited result, not a process failure, when a previously accepted released Profile remains available.

- [ ] **Step 5: Run GREEN and commit**

Run: `go -C examples/evaluation test ./promptiter/regressionloop -run TestDeterministicEndToEnd -count=3`

Expected: PASS three times with byte-identical reports after excluding no fields because clock and run ID are injected.

```bash
git add examples/evaluation/promptiter/regressionloop/{data,main.go,main_test.go}
git commit -m "feat(evaluation): add regression loop example"
```

## Task 9: Documentation, Golden Artifacts, and Full Validation

**Files:**
- Create: `examples/evaluation/promptiter/regressionloop/README.md`
- Create: `examples/evaluation/promptiter/regressionloop/DESIGN.md`
- Create: `examples/evaluation/promptiter/regressionloop/output/optimization_report.json`
- Create: `examples/evaluation/promptiter/regressionloop/output/optimization_report.md`
- Create: `examples/evaluation/promptiter/regressionloop/output/candidate_profile.json`
- Modify: `examples/evaluation/promptiter/README.md`

- [ ] **Step 1: Write user documentation**

README documents deterministic default, live role-specific model/Base URL/API-key environment configuration, held-out isolation, cumulative budget semantics, explicit-zero limits, sensitive artifact permissions, trace limitations, and exact commands. `DESIGN.md` provides the required 300–500 Chinese-character explanation of attribution, release gate, overfitting prevention, PromptIter integration, and audit publication.

- [ ] **Step 2: Generate and inspect golden artifacts**

Run: `go -C examples/evaluation run ./promptiter/regressionloop -config ./promptiter/regressionloop/data/promptiter.json -data-dir ./promptiter/regressionloop/data -output-dir ./promptiter/regressionloop/output -run-id sample`

Expected: accepted balanced Profile; ineffective and overfit rounds rejected; JSON, Markdown, and complete Profile written under the sample bundle; no credentials or absolute local paths.

- [ ] **Step 3: Run targeted quality checks**

```bash
go -C examples/evaluation test ./promptiter/regressionloop -count=1
go -C examples/evaluation test -race ./promptiter/regressionloop -count=1
go -C examples/evaluation vet ./promptiter/regressionloop
gofmt -r 'interface{} -> any' -l examples/evaluation/promptiter/regressionloop
goimports -l examples/evaluation/promptiter/regressionloop
```

Expected: all tests pass; no race report; vet succeeds; formatting/import commands print nothing.

- [ ] **Step 4: Perform the mandatory public-API and framework-design second pass**

Run: `git diff upstream/main...HEAD -- '*.go'`

Expected review result: no new exported symbols, no core-package changes, no changed defaults or serialization outside the example. Confirm complete Profile semantics, nil/empty distinctions, cancellation, cleanup, effective configuration, and artifact permissions against the design spec.

- [ ] **Step 5: Run repository-proportional validation and commit**

```bash
go build ./...
go test ./...
go -C test test ./...
.github/scripts/run-go-tests.sh
.github/scripts/check-examples.sh
golangci-lint run --timeout=10m
git diff --check
```

Expected: all applicable checks pass. If a tool is unavailable, record the exact missing binary; do not claim that check passed.

```bash
git add examples/evaluation/promptiter/README.md examples/evaluation/promptiter/regressionloop
git commit -m "docs(evaluation): document regression loop"
```

## Plan Self-Review Result

- Spec coverage: all design sections map to Tasks 1–9.
- Public surface: none; all feature declarations remain unexported in the example `main` package.
- Held-out isolation: protected by a direct `RunRequest` spy test in Task 7.
- Evidence completeness: catalog and normalization tests cover null, duplicate, missing, extra, execution-only, unknown, and not-evaluated shapes.
- Type consistency: `config`, `catalog`, `evaluationSnapshot`, `gatePolicy`, `usageSummary`, `report`, `runtime`, and `pipeline` names are stable across tasks.
- Compatibility: no root or evaluation module API changes are planned; every Go file carries the required license header.
