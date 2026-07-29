//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package assist provides optional, governed model assistance for code review.
package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	inmemoryartifact "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/governance"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

const (
	defaultModelName = "gpt-4o-mini"
	maxEvidenceBytes = 64 << 10
)

var offlineEnvironment = map[string]string{
	"CGO_ENABLED": "0",
	"GOPROXY":     "off",
	"GOSUMDB":     "off",
	"GOTOOLCHAIN": "local",
	"GOWORK":      "off",
}

// DegradationKind identifies why optional model assistance produced no findings.
type DegradationKind string

const (
	// DegradationModelError indicates that a model request failed.
	DegradationModelError DegradationKind = "model_error"
	// DegradationMalformedOutput indicates that strict structured output could not be decoded.
	DegradationMalformedOutput DegradationKind = "malformed_output"
	// DegradationRejectedOutput indicates that untrusted output violated the finding contract.
	DegradationRejectedOutput DegradationKind = "rejected_output"
)

// Degradation is a caller-visible, sanitized optional-assistance failure.
type Degradation struct {
	// Kind classifies the degradation for caller control flow.
	Kind DegradationKind
	// Stage identifies evidence collection, structured output, or validation.
	Stage string
	// Message is sanitized and safe for caller-visible diagnostics.
	Message string
}

// Result contains canonical model findings or a typed optional-assistance degradation.
type Result struct {
	// Findings contains only canonical findings accepted by the findings package.
	Findings []review.Finding
	// Degradation is non-nil when optional model assistance safely produced no findings.
	Degradation *Degradation
}

// Config configures one Assistant. NewModel is primarily an integration hook;
// model mode otherwise constructs the framework's OpenAI-compatible model.
type Config struct {
	// Mode selects rule-only, deterministic fake-model, or OpenAI-compatible model assistance.
	Mode review.Mode
	// TaskID owns all sessions and recorded governance decisions for this assistant.
	TaskID string
	// SkillsRoot contains the filesystem-backed code-review Skill.
	SkillsRoot string
	// Executor must expose an offline workspace engine with clean-environment support.
	Executor codeexecutor.CodeExecutor
	// DecisionSink records tool visibility and permission decisions.
	DecisionSink governance.DecisionSink
	// ModelName selects the OpenAI-compatible model and defaults to gpt-4o-mini.
	ModelName string
	// BaseURL optionally selects an OpenAI-compatible endpoint.
	BaseURL string
	// APIKey optionally supplies the endpoint credential; provider environment lookup remains supported.
	APIKey string
	// NewModel overrides construction for integration tests and custom compatible adapters.
	NewModel func() model.Model
}

// Assistant performs optional two-stage model assistance.
type Assistant struct {
	mode         review.Mode
	taskID       string
	skills       skill.Repository
	executor     codeexecutor.CodeExecutor
	decisionSink governance.DecisionSink
	model        model.Model
	scripts      []governance.TrustedScript
}

// New constructs an Assistant without creating a model in rule-only mode.
func New(config Config) (*Assistant, error) {
	assistant := &Assistant{mode: config.Mode, taskID: config.TaskID}
	if config.Mode == review.ModeRuleOnly {
		return assistant, nil
	}
	if config.Mode != review.ModeFakeModel && config.Mode != review.ModeModel {
		return nil, errors.New("new assistant: unsupported review mode")
	}
	if config.TaskID == "" || config.SkillsRoot == "" || config.Executor == nil ||
		config.DecisionSink == nil {
		return nil, errors.New("new assistant: task id, skills root, executor, and decision sink are required")
	}
	provider, ok := config.Executor.(codeexecutor.EngineProvider)
	if !ok || provider.Engine() == nil || provider.Engine().Manager() == nil ||
		provider.Engine().FS() == nil || provider.Engine().Runner() == nil {
		return nil, errors.New("new assistant: executor must provide a live workspace engine")
	}
	capabilities := provider.Engine().Describe()
	if !capabilities.SupportsCleanEnv || capabilities.NetworkAllowed {
		return nil, errors.New("new assistant: executor must provide clean environment and disabled network")
	}
	repository, err := skill.NewFSRepository(config.SkillsRoot)
	if err != nil {
		return nil, fmt.Errorf("new assistant: load skills: %w", err)
	}
	if _, err := repository.Get("code-review"); err != nil {
		return nil, fmt.Errorf("new assistant: load code-review skill: %w", err)
	}
	scripts, err := loadTrustedScripts(config.SkillsRoot)
	if err != nil {
		return nil, err
	}

	var modelInstance model.Model
	if config.NewModel != nil {
		modelInstance = config.NewModel()
	} else if config.Mode == review.ModeFakeModel {
		modelInstance = NewFakeModel()
	} else {
		name := strings.TrimSpace(config.ModelName)
		if name == "" {
			name = defaultModelName
		}
		var options []openai.Option
		if config.BaseURL != "" {
			options = append(options, openai.WithBaseURL(config.BaseURL))
		}
		if config.APIKey != "" {
			options = append(options, openai.WithAPIKey(config.APIKey))
		}
		modelInstance = openai.New(name, options...)
	}
	if modelInstance == nil {
		return nil, errors.New("new assistant: model factory returned nil")
	}
	assistant.skills = repository
	assistant.executor = config.Executor
	assistant.decisionSink = config.DecisionSink
	assistant.model = modelInstance
	assistant.scripts = scripts
	return assistant, nil
}

// Review collects governed evidence, requests tool-free structured output, and
// canonicalizes every accepted model finding against diff.
func (a *Assistant) Review(ctx context.Context, diff input.Diff) (Result, error) {
	if a == nil {
		return Result{}, errors.New("review assistance: nil assistant")
	}
	if ctx == nil {
		return Result{}, errors.New("review assistance: nil context")
	}
	if a.mode == review.ModeRuleOnly {
		return Result{}, nil
	}
	if a.model == nil || a.skills == nil || a.executor == nil || a.decisionSink == nil {
		return Result{}, errors.New("review assistance: assistant is not initialized")
	}
	inputJSON, err := sanitizedDiffJSON(diff)
	if err != nil {
		return Result{}, err
	}
	evidence, degradation, err := a.collectEvidence(ctx, inputJSON)
	if err != nil {
		return Result{}, err
	}
	if degradation != nil {
		return Result{Degradation: degradation}, nil
	}
	return a.collectFindings(ctx, diff, inputJSON, evidence), nil
}

func (a *Assistant) collectEvidence(
	ctx context.Context,
	inputJSON []byte,
) (string, *Degradation, error) {
	assetPolicy, err := governance.NewPolicy(nil, a.scripts...)
	if err != nil {
		return "", nil, fmt.Errorf("collect evidence: prepare trusted scripts: %w", err)
	}
	bootstrap := codeexecutor.WorkspaceBootstrapSpec{
		Files: []codeexecutor.WorkspaceFile{{
			Key:     "review-input",
			Target:  "work/review-input.json",
			Content: inputJSON,
			Mode:    0o444,
		}},
	}
	allowedExecutables := []string{"go", "staticcheck"}
	for _, asset := range assetPolicy.ScriptAssets() {
		bootstrap.Files = append(bootstrap.Files, codeexecutor.WorkspaceFile{
			Key:     "trusted-script-" + filepath.Base(asset.Command),
			Target:  asset.Command,
			Content: asset.Content,
			Mode:    0o555,
		})
		allowedExecutables = append(allowedExecutables, asset.Command)
	}

	executor := codeexecutor.NewEnvInjectingCodeExecutor(
		withoutInteractiveExecution(a.executor),
		func(context.Context) map[string]string {
			values := make(map[string]string, len(offlineEnvironment))
			for key, value := range offlineEnvironment {
				values[key] = value
			}
			return values
		},
	)
	deferred := newDeferredGovernance(a.taskID, a.decisionSink, a.scripts)
	agt := llmagent.New(
		"code-review-evidence",
		llmagent.WithModel(a.model),
		llmagent.WithDescription("Collect bounded evidence for a Go code review."),
		llmagent.WithInstruction(evidenceInstruction),
		llmagent.WithSkills(a.skills),
		llmagent.WithSkillToolProfile(llmagent.SkillToolProfileFull),
		llmagent.WithAllowedSkillTools(
			llmagent.SkillToolLoad,
			llmagent.SkillToolListDocs,
			llmagent.SkillToolSelectDocs,
		),
		llmagent.WithCodeExecutor(executor),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
		llmagent.WithWorkspaceExecAllowedCommands(allowedExecutables...),
		llmagent.WithWorkspaceExecDeniedCommands(
			"curl", "wget", "nc", "netcat", "ssh", "scp", "git", "goenv",
		),
		llmagent.WithWorkspaceExecOutputLimits(workspaceexec.OutputLimits{MaxOutputBytes: 32 << 10}),
		llmagent.WithWorkspaceBootstrap(bootstrap),
		llmagent.WithMaxLLMCalls(4),
		llmagent.WithMaxToolIterations(3),
	)
	if err := deferred.RecordSurface(ctx, agt.Tools()); err != nil {
		return "", nil, fmt.Errorf("collect evidence: record tool surface: %w", err)
	}

	run := runner.NewRunner(
		"code-review-evidence",
		agt,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer run.Close()
	events, runErr := run.Run(
		ctx,
		"reviewer",
		a.taskID+"-evidence",
		model.NewUserMessage("Load the code-review Skill and collect bounded evidence."),
		agent.WithToolPermissionPolicy(deferred),
	)
	if runErr != nil {
		return "", modelDegradation("evidence", runErr), nil
	}
	evidence, responseErr := collectText(events)
	if responseErr != nil {
		return "", modelDegradation("evidence", responseErr), nil
	}
	if filterErr := deferred.FilterError(); filterErr != nil {
		return "", nil, fmt.Errorf("collect evidence: record tool filter: %w", filterErr)
	}
	return bound(redact.String(evidence), maxEvidenceBytes), nil, nil
}

type deferredGovernance struct {
	mu        sync.Mutex
	taskID    string
	sink      governance.DecisionSink
	scripts   []governance.TrustedScript
	recorders map[string]*governance.RecordingPolicy
	err       error
}

func newDeferredGovernance(
	taskID string,
	sink governance.DecisionSink,
	scripts []governance.TrustedScript,
) *deferredGovernance {
	return &deferredGovernance{
		taskID:    taskID,
		sink:      sink,
		scripts:   append([]governance.TrustedScript(nil), scripts...),
		recorders: make(map[string]*governance.RecordingPolicy),
	}
}

func (p *deferredGovernance) CheckToolPermission(
	ctx context.Context,
	request *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if p == nil || request == nil || request.Tool == nil {
		return tool.DenyPermission("review governance is unavailable"),
			errors.New("review governance: invalid permission request")
	}
	p.mu.Lock()
	recorder := p.recorders[request.ToolCallID]
	var err error
	if recorder == nil {
		policy, policyErr := governance.NewPolicy([]tool.Tool{request.Tool}, p.scripts...)
		if policyErr != nil {
			p.mu.Unlock()
			return tool.DenyPermission("review governance is unavailable"), policyErr
		}
		recorder, err = governance.NewRecordingPolicy(policy, p.sink, p.taskID)
		if err == nil {
			p.recorders[request.ToolCallID] = recorder
		}
	}
	p.mu.Unlock()
	if err != nil {
		return tool.DenyPermission("review governance is unavailable"), err
	}
	return recorder.CheckToolPermission(ctx, request)
}

func (p *deferredGovernance) RecordSurface(ctx context.Context, tools []tool.Tool) error {
	policy, err := governance.NewPolicy(tools, p.scripts...)
	if err != nil {
		return err
	}
	recorder, err := governance.NewRecordingPolicy(policy, p.sink, p.taskID)
	if err != nil {
		return err
	}
	filter := recorder.Filter()
	for _, candidate := range tools {
		if !filter(ctx, candidate) {
			return errors.New("review governance: unexpected tool in model surface")
		}
	}
	if err := recorder.FilterError(); err != nil {
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		return err
	}
	return nil
}

func (p *deferredGovernance) FilterError() error {
	if p == nil {
		return errors.New("review governance is unavailable")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	return nil
}

var _ tool.PermissionPolicy = (*deferredGovernance)(nil)

type nonInteractiveExecutor struct {
	codeexecutor.CodeExecutor
	engine codeexecutor.Engine
}

func withoutInteractiveExecution(executor codeexecutor.CodeExecutor) codeexecutor.CodeExecutor {
	provider := executor.(codeexecutor.EngineProvider)
	engine := provider.Engine()
	return &nonInteractiveExecutor{
		CodeExecutor: executor,
		engine: codeexecutor.NewEngineWithCapabilities(
			engine.Manager(),
			engine.FS(),
			struct{ codeexecutor.ProgramRunner }{ProgramRunner: engine.Runner()},
			engine.Describe(),
		),
	}
}

func (e *nonInteractiveExecutor) Engine() codeexecutor.Engine {
	return e.engine
}

var _ codeexecutor.EngineProvider = (*nonInteractiveExecutor)(nil)

func (a *Assistant) collectFindings(
	ctx context.Context,
	diff input.Diff,
	inputJSON []byte,
	evidence string,
) Result {
	agt := llmagent.New(
		"code-review-output",
		llmagent.WithModel(a.model),
		llmagent.WithDescription("Return strict structured code review findings."),
		llmagent.WithInstruction(outputInstruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
		llmagent.WithStructuredOutputJSON(new(candidateEnvelope), true, "Canonical model finding candidates."),
		llmagent.WithWorkspaceExecSurfaceEnabled(false),
		llmagent.WithMaxLLMCalls(1),
	)
	run := runner.NewRunner(
		"code-review-output",
		agt,
		runner.WithSessionService(inmemory.NewSessionService()),
		runner.WithArtifactService(inmemoryartifact.NewService()),
	)
	defer run.Close()
	prompt := "Sanitized parsed diff:\n" + string(inputJSON) + "\n\nCollected evidence:\n" + evidence
	events, err := run.Run(
		ctx,
		"reviewer",
		a.taskID+"-output",
		model.NewUserMessage(prompt),
	)
	if err != nil {
		return Result{Degradation: modelDegradation("structured_output", err)}
	}
	raw, typed, responseErr := collectStructured(events)
	if responseErr != nil {
		trimmed := strings.TrimSpace(raw)
		if strings.Contains(strings.ToLower(responseErr.Error()), "unmarshal") ||
			strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return Result{Degradation: malformedDegradation(responseErr)}
		}
		return Result{Degradation: modelDegradation("structured_output", responseErr)}
	}
	decoded, err := decodeEnvelope(raw)
	if err != nil {
		return Result{Degradation: malformedDegradation(err)}
	}
	if typed == nil {
		return Result{Degradation: malformedDegradation(errors.New("typed structured output is missing"))}
	}
	if len(decoded.Findings) != len(typed.Findings) {
		return Result{Degradation: malformedDegradation(errors.New("typed structured output does not match JSON"))}
	}
	candidates := make([]findings.Candidate, 0, len(decoded.Findings))
	for _, output := range decoded.Findings {
		candidate := output.candidate(a.taskID)
		if output.Source != review.SourceModel || candidateHasSecret(candidate) {
			return Result{Degradation: rejectedDegradation(errors.New("model output contains an invalid source or secret"))}
		}
		candidates = append(candidates, candidate)
	}
	canonical, err := findings.Normalize(a.taskID, diff, candidates)
	if err != nil {
		return Result{Degradation: rejectedDegradation(err)}
	}
	return Result{Findings: canonical}
}

type candidateEnvelope struct {
	Findings []candidateOutput `json:"findings"`
}

type candidateOutput struct {
	SchemaVersion  string             `json:"schema_version"`
	Severity       review.Severity    `json:"severity"`
	Category       string             `json:"category"`
	Layer          review.ChangeLayer `json:"layer"`
	File           string             `json:"file"`
	Line           int                `json:"line"`
	EndLine        int                `json:"end_line,omitempty"`
	SemanticAnchor string             `json:"semantic_anchor"`
	Title          string             `json:"title"`
	Evidence       string             `json:"evidence"`
	Recommendation string             `json:"recommendation"`
	Confidence     review.Confidence  `json:"confidence"`
	Source         review.Source      `json:"source"`
	RuleID         string             `json:"rule_id"`
	Disposition    review.Disposition `json:"disposition"`
}

func (o candidateOutput) candidate(taskID string) findings.Candidate {
	return findings.Candidate{
		SchemaVersion:  o.SchemaVersion,
		TaskID:         taskID,
		Severity:       o.Severity,
		Category:       o.Category,
		Layer:          o.Layer,
		File:           o.File,
		Line:           o.Line,
		EndLine:        o.EndLine,
		SemanticAnchor: o.SemanticAnchor,
		Title:          o.Title,
		Evidence:       o.Evidence,
		Recommendation: o.Recommendation,
		Confidence:     o.Confidence,
		Source:         o.Source,
		RuleID:         o.RuleID,
		Disposition:    o.Disposition,
	}
}

func sanitizedDiffJSON(diff input.Diff) ([]byte, error) {
	raw, err := json.Marshal(diff)
	if err != nil {
		return nil, fmt.Errorf("review assistance: encode diff: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var compatible any
	if err := decoder.Decode(&compatible); err != nil {
		return nil, fmt.Errorf("review assistance: normalize diff: %w", err)
	}
	value, err := redact.Value(compatible)
	if err != nil {
		return nil, fmt.Errorf("review assistance: sanitize diff: %w", err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("review assistance: encode diff: %w", err)
	}
	return data, nil
}

func decodeEnvelope(raw string) (*candidateEnvelope, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var output candidateEnvelope
	if err := decoder.Decode(&output); err != nil {
		return nil, fmt.Errorf("decode model output: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode model output: trailing data")
	}
	if output.Findings == nil {
		return nil, errors.New("decode model output: findings must be present")
	}
	return &output, nil
}

func collectText(events <-chan *event.Event) (string, error) {
	var output strings.Builder
	for current := range events {
		if current == nil {
			continue
		}
		if current.Error != nil {
			return "", errors.New(current.Error.Message)
		}
		if current.Response == nil {
			continue
		}
		for _, choice := range current.Response.Choices {
			content := choice.Message.Content
			if content == "" {
				content = choice.Delta.Content
			}
			if content != "" {
				output.WriteString(content)
				output.WriteByte('\n')
			}
		}
	}
	return output.String(), nil
}

func collectStructured(events <-chan *event.Event) (string, *candidateEnvelope, error) {
	var raw string
	var typed *candidateEnvelope
	for current := range events {
		if current == nil {
			continue
		}
		if current.StructuredOutput != nil {
			if output, ok := current.StructuredOutput.(*candidateEnvelope); ok {
				typed = output
			}
		}
		if current.Response != nil {
			for _, choice := range current.Response.Choices {
				if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
					raw = choice.Message.Content
				}
			}
		}
		if current.Error != nil {
			return raw, typed, errors.New(current.Error.Message)
		}
	}
	return raw, typed, nil
}

func candidateHasSecret(candidate findings.Candidate) bool {
	values := []string{
		candidate.SchemaVersion,
		candidate.TaskID,
		string(candidate.Severity),
		candidate.Category,
		string(candidate.Layer),
		candidate.File,
		candidate.SemanticAnchor,
		candidate.Title,
		candidate.Evidence,
		candidate.Recommendation,
		string(candidate.Confidence),
		string(candidate.Source),
		candidate.RuleID,
		string(candidate.Disposition),
	}
	for _, value := range values {
		if redact.String(value) != value {
			return true
		}
	}
	return false
}

func loadTrustedScripts(root string) ([]governance.TrustedScript, error) {
	directory := filepath.Join(root, "code-review", "scripts")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("new assistant: read trusted scripts: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sh") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	scripts := make([]governance.TrustedScript, 0, len(names))
	for _, name := range names {
		content, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			return nil, fmt.Errorf("new assistant: read trusted script: %w", readErr)
		}
		scripts = append(scripts, governance.TrustedScript{Name: name, Content: content})
	}
	if len(scripts) == 0 {
		return nil, errors.New("new assistant: code-review skill has no trusted scripts")
	}
	return scripts, nil
}

func modelDegradation(stage string, err error) *Degradation {
	return &Degradation{
		Kind:    DegradationModelError,
		Stage:   stage,
		Message: redact.String(err.Error()),
	}
}

func malformedDegradation(err error) *Degradation {
	return &Degradation{
		Kind:    DegradationMalformedOutput,
		Stage:   "structured_output",
		Message: redact.String(err.Error()),
	}
}

func rejectedDegradation(err error) *Degradation {
	return &Degradation{
		Kind:    DegradationRejectedOutput,
		Stage:   "validation",
		Message: redact.String(err.Error()),
	}
}

func bound(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return string(bytes.ToValidUTF8([]byte(value[:maximum]), []byte("\uFFFD")))
}

const evidenceInstruction = `Use only the code-review Skill and the visible governed tools.
Load code-review first. Inspect work/review-input.json. Run at most one exact
workspace check from: go test ./..., go vet ./..., staticcheck ./.... Assume the
workspace is offline: do not install dependencies or request network access.
Return a concise evidence summary; do not return final findings in this stage.`

const outputInstruction = `Return only strict structured output. Tools are unavailable.
Every finding must identify an added line in the sanitized parsed diff, use
source "model", use a versioned rule_id ending in /v1, and contain no secrets.
Use low confidence when evidence requires human confirmation.`
