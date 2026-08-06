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
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	internalValidationTrainCaseIDs = "train_case_ids"
	internalValidationTrainAll     = "train_all"
)

// LoadPromptIterConfig strictly loads and validates custom PromptIter configuration.
func LoadPromptIterConfig(path string) (*PromptIterConfig, error) {
	var config PromptIterConfig
	data, err := decodeStrictJSONFile(path, &config)
	if err != nil {
		return nil, fmt.Errorf("load PromptIter config: %w", err)
	}
	if err := validatePromptIterRequiredFields(data); err != nil {
		return nil, fmt.Errorf("validate PromptIter config: %w", err)
	}
	if err := validatePromptIterConfig(config); err != nil {
		return nil, fmt.Errorf("validate PromptIter config: %w", err)
	}
	return &config, nil
}

// LoadRegressionConfig strictly loads and validates custom release configuration.
func LoadRegressionConfig(path string) (*RegressionConfig, error) {
	var config RegressionConfig
	data, err := decodeStrictJSONFile(path, &config)
	if err != nil {
		return nil, fmt.Errorf("load regression config: %w", err)
	}
	if err := validateRegressionRequiredFields(data); err != nil {
		return nil, fmt.Errorf("validate regression config: %w", err)
	}
	if err := validateRegressionConfig(config); err != nil {
		return nil, fmt.Errorf("validate regression config: %w", err)
	}
	return &config, nil
}

// LoadRunConfig resolves strict custom configuration and native Evaluation
// resources into one provenance-bound pipeline input.
//
//nolint:gocyclo // Ordered loading and provenance checks intentionally fail at the exact input.
func LoadRunConfig(ctx context.Context, appName string, files InputFiles) (*RunConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(appName) == "" {
		return nil, errors.New("app name is empty")
	}
	paths, err := validateInputFiles(files)
	if err != nil {
		return nil, err
	}
	rawInputs, hashes, err := readAndHashInputs(paths)
	if err != nil {
		return nil, err
	}

	var promptIterConfig PromptIterConfig
	if err := decodeStrictJSON(rawInputs["promptIterConfig"], &promptIterConfig); err != nil {
		return nil, fmt.Errorf("decode PromptIter config %s: %w", files.PromptIterConfig, err)
	}
	if err := validatePromptIterRequiredFields(rawInputs["promptIterConfig"]); err != nil {
		return nil, fmt.Errorf("validate PromptIter config: %w", err)
	}
	if err := validatePromptIterConfig(promptIterConfig); err != nil {
		return nil, fmt.Errorf("validate PromptIter config: %w", err)
	}
	var regressionConfig RegressionConfig
	if err := decodeStrictJSON(rawInputs["regressionConfig"], &regressionConfig); err != nil {
		return nil, fmt.Errorf("decode regression config %s: %w", files.RegressionConfig, err)
	}
	if err := validateRegressionRequiredFields(rawInputs["regressionConfig"]); err != nil {
		return nil, fmt.Errorf("validate regression config: %w", err)
	}
	if err := validateRegressionConfig(regressionConfig); err != nil {
		return nil, fmt.Errorf("validate regression config: %w", err)
	}
	if err := validateOutputInputCollisions(regressionConfig.Output, paths); err != nil {
		return nil, err
	}

	if err := validateSingleJSON(rawInputs["trainEvalSet"]); err != nil {
		return nil, fmt.Errorf("validate native train eval set JSON: %w", err)
	}
	if err := validateSingleJSON(rawInputs["validationEvalSet"]); err != nil {
		return nil, fmt.Errorf("validate native validation eval set JSON: %w", err)
	}
	if err := validateSingleJSON(rawInputs["metrics"]); err != nil {
		return nil, fmt.Errorf("validate native metrics JSON: %w", err)
	}
	trainSet, err := loadNativeEvalSet(ctx, appName, rawInputs["trainEvalSet"], "train")
	if err != nil {
		return nil, err
	}
	validationSet, err := loadNativeEvalSet(
		ctx,
		appName,
		rawInputs["validationEvalSet"],
		"validation",
	)
	if err != nil {
		return nil, err
	}
	nativeMetrics, err := loadNativeMetrics(
		ctx,
		appName,
		trainSet.EvalSetID,
		rawInputs["metrics"],
	)
	if err != nil {
		return nil, err
	}

	trainSpec, err := buildDatasetSpec(trainSet, hashes["trainEvalSet"], hashes["metrics"], nativeMetrics)
	if err != nil {
		return nil, fmt.Errorf("validate train inventory: %w", err)
	}
	validationSpec, err := buildDatasetSpec(validationSet, hashes["validationEvalSet"], hashes["metrics"], nativeMetrics)
	if err != nil {
		return nil, fmt.Errorf("validate validation inventory: %w", err)
	}
	if err := validateHeldoutExclusion(trainSpec, validationSpec); err != nil {
		return nil, err
	}
	if err := validateInternalValidation(promptIterConfig.Policy, trainSpec, validationSpec); err != nil {
		return nil, err
	}
	if err := validateConfiguredCases(regressionConfig, validationSpec); err != nil {
		return nil, err
	}
	if err := validateGateMetrics(regressionConfig.Gate, trainSpec.MetricNames); err != nil {
		return nil, err
	}

	baselinePrompt := strings.TrimSpace(string(rawInputs["baselinePrompt"]))
	if baselinePrompt == "" {
		return nil, errors.New("baseline prompt is empty")
	}
	initialProfile := profileFromPrompt(baselinePrompt, promptIterConfig.Policy.TargetSurfaceID)
	gateJSON, err := json.Marshal(regressionConfig.Gate)
	if err != nil {
		return nil, fmt.Errorf("marshal metric gate policy: %w", err)
	}
	metricPolicyHash := hashStrings("native-metric-policy-v1", hashes["metrics"], string(gateJSON))
	config := &RunConfig{
		ReportID:               regressionConfig.ReportID,
		GeneratedAt:            regressionConfig.GeneratedAt.UTC(),
		Seed:                   promptIterConfig.Seed,
		InitialProfile:         initialProfile,
		Train:                  trainSpec,
		Validation:             validationSpec,
		PromptIter:             promptIterConfig.Policy,
		Gate:                   regressionConfig.Gate,
		Output:                 regressionConfig.Output,
		InputHashes:            hashes,
		MetricPolicyHash:       metricPolicyHash,
		EvidenceLimit:          regressionConfig.EvidenceLimit,
		CriticalCaseIDs:        append([]string(nil), regressionConfig.CriticalCaseIDs...),
		HardFailureCaseIDs:     append([]string(nil), regressionConfig.HardFailureCaseIDs...),
		trainEvalSetInput:      bytes.Clone(rawInputs["trainEvalSet"]),
		validationEvalSetInput: bytes.Clone(rawInputs["validationEvalSet"]),
		metricsInput:           bytes.Clone(rawInputs["metrics"]),
	}
	config.executionNonce, err = newExecutionNonce()
	if err != nil {
		return nil, err
	}
	if err := BindRuntime(config, RuntimeConfig{
		Engine: "promptiter-native",
		Seed:   promptIterConfig.Seed,
		Evaluator: map[string]any{
			"appName":        appName,
			"trainEvalSet":   trainSpec.EvalSetID,
			"heldoutEvalSet": validationSpec.EvalSetID,
		},
	}); err != nil {
		return nil, err
	}
	if err := sealSourceConfig(config); err != nil {
		return nil, err
	}
	return config, nil
}

// RuntimeConfigFingerprint returns the canonical hash of an auditable runtime
// configuration. encoding/json deterministically orders string map keys.
func RuntimeConfigFingerprint(runtime RuntimeConfig) (string, error) {
	data, err := json.Marshal(runtime)
	if err != nil {
		return "", fmt.Errorf("marshal runtime config: %w", err)
	}
	return hashStrings("runtime-config-v1", string(data)), nil
}

// BindRuntime copies runtime into config and refreshes evaluator provenance.
// This prevents a caller mutation after binding from silently changing the
// configuration represented by EvaluatorConfigHash.
func BindRuntime(config *RunConfig, runtime RuntimeConfig) error {
	if config == nil {
		return errors.New("run config is nil")
	}
	if strings.TrimSpace(config.executionNonce) == "" {
		return errors.New("execution nonce is empty")
	}
	if strings.TrimSpace(runtime.Engine) == "" {
		return errors.New("runtime engine is empty")
	}
	if runtime.Seed != config.Seed {
		return fmt.Errorf("runtime seed %d does not match run seed %d", runtime.Seed, config.Seed)
	}
	data, err := json.Marshal(runtime)
	if err != nil {
		return fmt.Errorf("marshal runtime config: %w", err)
	}
	var copied RuntimeConfig
	if err := json.Unmarshal(data, &copied); err != nil {
		return fmt.Errorf("copy runtime config: %w", err)
	}
	fingerprint, err := RuntimeConfigFingerprint(copied)
	if err != nil {
		return err
	}
	inputValues := make([]string, 0, 6)
	for _, name := range []string{
		"trainEvalSet",
		"validationEvalSet",
		"metrics",
		"baselinePrompt",
		"promptIterConfig",
		"regressionConfig",
	} {
		value := strings.TrimSpace(config.InputHashes[name])
		if value == "" {
			return fmt.Errorf("input hash %q is empty", name)
		}
		inputValues = append(inputValues, value)
	}
	runIdentity := []string{
		"regression-run-v1",
		config.ReportID,
		fmt.Sprintf("%d", config.Seed),
		config.executionNonce,
	}
	runIdentity = append(runIdentity, inputValues...)
	runIdentity = append(runIdentity, fingerprint)
	runFingerprint := hashStrings(runIdentity...)
	config.Runtime = copied
	config.RunID = config.ReportID + "-" + runFingerprint[:12]
	config.EvaluatorConfigHash = hashStrings(
		"runtime-bound-evaluator-v1",
		config.Train.EvalSetHash,
		config.Validation.EvalSetHash,
		config.MetricPolicyHash,
		fingerprint,
	)
	return nil
}

func newExecutionNonce() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate execution nonce: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

type sourceConfigBinding struct {
	ReportID           string
	GeneratedAt        time.Time
	Seed               int64
	InitialProfile     *promptiter.Profile
	Train              DatasetSpec
	Validation         DatasetSpec
	PromptIter         PromptIterPolicy
	Gate               GatePolicy
	Output             OutputConfig
	InputHashes        map[string]string
	MetricPolicyHash   string
	CriticalCaseIDs    []string
	HardFailureCaseIDs []string
	EvidenceLimit      int
}

func sourceConfigFingerprint(config *RunConfig) (string, error) {
	if config == nil {
		return "", errors.New("run config is nil")
	}
	binding := sourceConfigBinding{
		ReportID:           config.ReportID,
		GeneratedAt:        config.GeneratedAt,
		Seed:               config.Seed,
		InitialProfile:     config.InitialProfile,
		Train:              config.Train,
		Validation:         config.Validation,
		PromptIter:         config.PromptIter,
		Gate:               config.Gate,
		Output:             config.Output,
		InputHashes:        config.InputHashes,
		MetricPolicyHash:   config.MetricPolicyHash,
		CriticalCaseIDs:    config.CriticalCaseIDs,
		HardFailureCaseIDs: config.HardFailureCaseIDs,
		EvidenceLimit:      config.EvidenceLimit,
	}
	data, err := json.Marshal(binding)
	if err != nil {
		return "", fmt.Errorf("marshal source configuration binding: %w", err)
	}
	return hashStrings("source-config-v1", string(data)), nil
}

func sealSourceConfig(config *RunConfig) error {
	fingerprint, err := sourceConfigFingerprint(config)
	if err != nil {
		return err
	}
	config.sourceConfigHash = fingerprint
	return nil
}

// NewInputManagers returns official local managers backed by immutable copies
// of the exact bytes represented by config.
func NewInputManagers(config *RunConfig) (evalset.Manager, metric.Manager, error) {
	if config == nil {
		return nil, nil, errors.New("run config is nil")
	}
	if err := validateRunConfig(config); err != nil {
		return nil, nil, fmt.Errorf("validate run config: %w", err)
	}
	inputNames := []string{"trainEvalSet", "validationEvalSet", "metrics"}
	raw := map[string][]byte{
		"trainEvalSet":      bytes.Clone(config.trainEvalSetInput),
		"validationEvalSet": bytes.Clone(config.validationEvalSetInput),
		"metrics":           bytes.Clone(config.metricsInput),
	}
	for _, name := range inputNames {
		actualHash := hashBytes(raw[name])
		if actualHash != config.InputHashes[name] {
			label := strings.ReplaceAll(name, "EvalSet", " eval-set")
			return nil, nil, fmt.Errorf(
				"%s input hash %q does not match configured hash %q",
				label,
				actualHash,
				config.InputHashes[name],
			)
		}
	}
	snapshotDir, err := os.MkdirTemp("", "trpc-agent-regression-inputs-")
	if err != nil {
		return nil, nil, fmt.Errorf("create immutable input directory: %w", err)
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(snapshotDir)
		}
	}()
	snapshotPaths := map[string]string{
		"trainEvalSet":      filepath.Join(snapshotDir, "train.evalset.json"),
		"validationEvalSet": filepath.Join(snapshotDir, "validation.evalset.json"),
		"metrics":           filepath.Join(snapshotDir, "metrics.json"),
	}
	for _, name := range inputNames {
		if err := os.WriteFile(snapshotPaths[name], raw[name], 0o600); err != nil {
			return nil, nil, fmt.Errorf("write immutable %s input: %w", name, err)
		}
	}
	evalLocator := &inputEvalSetLocator{
		paths: map[string]string{
			config.Train.EvalSetID:      snapshotPaths["trainEvalSet"],
			config.Validation.EvalSetID: snapshotPaths["validationEvalSet"],
		},
	}
	cleanup := &frozenInputCleanup{dir: snapshotDir, remaining: 2}
	evalManager := &frozenEvalSetManager{
		Manager: evalsetlocal.New(evalset.WithLocator(evalLocator)),
		cleanup: cleanup,
	}
	metricManager := &frozenMetricManager{
		Manager: metriclocal.New(metric.WithLocator(&inputMetricLocator{
			path: snapshotPaths["metrics"],
		})),
		cleanup: cleanup,
	}
	cleanupOnError = false
	return evalManager, metricManager, nil
}

var errFrozenInputsReadOnly = errors.New("regression input managers are read-only")

type frozenInputCleanup struct {
	mu        sync.Mutex
	dir       string
	remaining int
}

func (c *frozenInputCleanup) release() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remaining == 0 {
		return nil
	}
	c.remaining--
	if c.remaining > 0 {
		return nil
	}
	return os.RemoveAll(c.dir)
}

type frozenEvalSetManager struct {
	evalset.Manager
	cleanup   *frozenInputCleanup
	closeOnce sync.Once
	closeErr  error
}

func (m *frozenEvalSetManager) Create(
	context.Context,
	string,
	string,
) (*evalset.EvalSet, error) {
	return nil, errFrozenInputsReadOnly
}

func (m *frozenEvalSetManager) Delete(context.Context, string, string) error {
	return errFrozenInputsReadOnly
}

func (m *frozenEvalSetManager) AddCase(
	context.Context,
	string,
	string,
	*evalset.EvalCase,
) error {
	return errFrozenInputsReadOnly
}

func (m *frozenEvalSetManager) UpdateCase(
	context.Context,
	string,
	string,
	*evalset.EvalCase,
) error {
	return errFrozenInputsReadOnly
}

func (m *frozenEvalSetManager) DeleteCase(
	context.Context,
	string,
	string,
	string,
) error {
	return errFrozenInputsReadOnly
}

func (m *frozenEvalSetManager) Close() error {
	m.closeOnce.Do(func() {
		m.closeErr = errors.Join(m.Manager.Close(), m.cleanup.release())
	})
	return m.closeErr
}

type frozenMetricManager struct {
	metric.Manager
	cleanup   *frozenInputCleanup
	closeOnce sync.Once
	closeErr  error
}

func (m *frozenMetricManager) Add(
	context.Context,
	string,
	string,
	*metric.EvalMetric,
) error {
	return errFrozenInputsReadOnly
}

func (m *frozenMetricManager) Delete(context.Context, string, string, string) error {
	return errFrozenInputsReadOnly
}

func (m *frozenMetricManager) Update(
	context.Context,
	string,
	string,
	*metric.EvalMetric,
) error {
	return errFrozenInputsReadOnly
}

func (m *frozenMetricManager) Close() error {
	m.closeOnce.Do(func() {
		m.closeErr = errors.Join(m.Manager.Close(), m.cleanup.release())
	})
	return m.closeErr
}

func validatePromptIterConfig(config PromptIterConfig) error {
	if config.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %q", config.SchemaVersion)
	}
	policy := config.Policy
	switch {
	case policy.MaxOuterRounds <= 0:
		return errors.New("maxOuterRounds must be greater than zero")
	case !finiteConfigNumber(policy.SearchMinScoreGain) || policy.SearchMinScoreGain < 0:
		return errors.New("searchMinScoreGain must be finite and non-negative")
	case strings.TrimSpace(policy.TargetSurfaceID) == "":
		return errors.New("targetSurfaceId is empty")
	}
	switch policy.InternalValidationStrategy {
	case internalValidationTrainCaseIDs:
		if len(policy.InternalValidationCaseIDs) == 0 {
			return errors.New("train_case_ids requires internalValidationCaseIds")
		}
	case internalValidationTrainAll:
		if len(policy.InternalValidationCaseIDs) != 0 {
			return errors.New("train_all must not set internalValidationCaseIds")
		}
	default:
		return fmt.Errorf("unsupported internalValidationStrategy %q", policy.InternalValidationStrategy)
	}
	return validateUniqueNonEmpty("internal validation case id", policy.InternalValidationCaseIDs)
}

func validateRegressionConfig(config RegressionConfig) error {
	if config.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema version %q", config.SchemaVersion)
	}
	switch {
	case strings.TrimSpace(config.ReportID) == "":
		return errors.New("reportId is empty")
	case config.GeneratedAt.IsZero():
		return errors.New("generatedAt is required")
	case config.EvidenceLimit <= 0:
		return errors.New("evidenceLimit must be greater than zero")
	case config.EvidenceLimit > 100:
		return errors.New("evidenceLimit must not exceed 100")
	}
	if err := validateGatePolicy(config.Gate); err != nil {
		return err
	}
	if err := validateUniqueNonEmpty("critical case id", config.CriticalCaseIDs); err != nil {
		return err
	}
	if err := validateUniqueNonEmpty("hard failure case id", config.HardFailureCaseIDs); err != nil {
		return err
	}
	return validateOutputConfig(config.Output)
}

func validateGatePolicy(policy GatePolicy) error {
	switch {
	case strings.TrimSpace(policy.PrimaryMetric) == "":
		return errors.New("gate primaryMetric is empty")
	case len(policy.MetricDirections) == 0:
		return errors.New("gate metricDirections is empty")
	case !finiteConfigNumber(policy.Epsilon) || policy.Epsilon <= 0:
		return errors.New("gate epsilon must be finite and greater than zero")
	case !finiteConfigNumber(policy.MinValidationGain) || policy.MinValidationGain < 0:
		return errors.New("gate minValidationGain must be finite and non-negative")
	case policy.ModelCallStopThreshold < 0:
		return errors.New("gate modelCallStopThreshold must be non-negative")
	}
	for name, direction := range policy.MetricDirections {
		if strings.TrimSpace(name) == "" {
			return errors.New("gate metric direction contains an empty metric name")
		}
		if direction != ScoreHigherIsBetter && direction != ScoreLowerIsBetter {
			return fmt.Errorf("gate metric %q has invalid direction %q", name, direction)
		}
	}
	return nil
}

func validateOutputConfig(output OutputConfig) error {
	jsonName := strings.TrimSpace(output.JSON)
	markdownName := strings.TrimSpace(output.Markdown)
	switch {
	case jsonName == "":
		return errors.New("output JSON name is empty")
	case markdownName == "":
		return errors.New("output Markdown name is empty")
	case jsonName == "." || jsonName == ".." ||
		filepath.IsAbs(jsonName) || filepath.Base(jsonName) != jsonName:
		return errors.New("output JSON must be a file name")
	case markdownName == "." || markdownName == ".." ||
		filepath.IsAbs(markdownName) || filepath.Base(markdownName) != markdownName:
		return errors.New("output Markdown must be a file name")
	case strings.EqualFold(filepath.Clean(jsonName), filepath.Clean(markdownName)):
		return errors.New("output JSON and Markdown names collide")
	}
	return nil
}

func validateInputFiles(files InputFiles) (map[string]string, error) {
	paths := map[string]string{
		"trainEvalSet":      files.TrainEvalSet,
		"validationEvalSet": files.ValidationEvalSet,
		"metrics":           files.Metrics,
		"baselinePrompt":    files.BaselinePrompt,
		"promptIterConfig":  files.PromptIterConfig,
		"regressionConfig":  files.RegressionConfig,
	}
	seen := make(map[string]string, len(paths))
	for name, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("%s path is empty", name)
		}
		canonical, err := canonicalPath(path)
		if err != nil {
			return nil, fmt.Errorf("resolve %s path: %w", name, err)
		}
		if previous, exists := seen[canonical]; exists {
			return nil, fmt.Errorf("%s and %s input paths collide", previous, name)
		}
		seen[canonical] = name
		paths[name] = canonical
	}
	return paths, nil
}

func validateOutputInputCollisions(output OutputConfig, paths map[string]string) error {
	inputNames := make(map[string]string, len(paths))
	for role, path := range paths {
		inputNames[strings.ToLower(filepath.Base(path))] = role
	}
	for role, name := range map[string]string{"JSON": output.JSON, "Markdown": output.Markdown} {
		if inputRole, exists := inputNames[strings.ToLower(filepath.Base(name))]; exists {
			return fmt.Errorf("output %s name collides with %s input", role, inputRole)
		}
	}
	return nil
}

func readAndHashInputs(paths map[string]string) (map[string][]byte, map[string]string, error) {
	raw := make(map[string][]byte, len(paths))
	hashes := make(map[string]string, len(paths))
	for name, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s input %s: %w", name, path, err)
		}
		raw[name] = data
		hashes[name] = hashBytes(data)
	}
	return raw, hashes, nil
}

func decodeStrictJSONFile(path string, target any) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := decodeStrictJSON(data, target); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return data, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func validatePromptIterRequiredFields(data []byte) error {
	root, err := requireJSONObjectFields(
		data,
		"PromptIter config",
		"schemaVersion",
		"seed",
		"policy",
	)
	if err != nil {
		return err
	}
	_, err = requireJSONObjectFields(
		root["policy"],
		"PromptIter policy",
		"maxOuterRounds",
		"searchMinScoreGain",
		"internalValidationStrategy",
		"targetSurfaceId",
	)
	return err
}

func validateRegressionRequiredFields(data []byte) error {
	root, err := requireJSONObjectFields(
		data,
		"regression config",
		"schemaVersion",
		"reportId",
		"generatedAt",
		"gate",
		"evidenceLimit",
		"output",
	)
	if err != nil {
		return err
	}
	if _, err := requireJSONObjectFields(
		root["gate"],
		"release gate",
		"primaryMetric",
		"metricDirections",
		"epsilon",
		"minValidationGain",
		"noNewHardFailures",
		"noCriticalRegressions",
		"modelCallStopThreshold",
	); err != nil {
		return err
	}
	_, err = requireJSONObjectFields(
		root["output"],
		"output config",
		"json",
		"markdown",
	)
	return err
}

func requireJSONObjectFields(
	data []byte,
	label string,
	required ...string,
) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %w", label, err)
	}
	if object == nil {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	for _, name := range required {
		value, exists := object[name]
		if !exists {
			return nil, fmt.Errorf("%s missing required field %q", label, name)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("%s required field %q must not be null", label, name)
		}
	}
	return object, nil
}

func validateSingleJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func loadNativeEvalSet(
	ctx context.Context,
	appName string,
	data []byte,
	role string,
) (*evalset.EvalSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(appName) == "" {
		return nil, errors.New("app name is empty")
	}
	var result evalset.EvalSet
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode native %s eval set: %w", role, err)
	}
	if result.EvalCases == nil {
		result.EvalCases = []*evalset.EvalCase{}
	}
	return &result, nil
}

func loadNativeMetrics(
	ctx context.Context,
	appName string,
	evalSetID string,
	data []byte,
) ([]*metric.EvalMetric, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(appName) == "" {
		return nil, errors.New("app name is empty")
	}
	if strings.TrimSpace(evalSetID) == "" {
		return nil, errors.New("eval set id is empty")
	}
	var decoded []*metric.EvalMetric
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decode native metrics: %w", err)
	}
	if len(decoded) == 0 {
		return nil, errors.New("native metrics inventory is empty")
	}
	results := make([]*metric.EvalMetric, 0, len(decoded))
	seen := make(map[string]struct{}, len(decoded))
	for _, item := range decoded {
		if item == nil {
			return nil, errors.New("native metric is nil")
		}
		name := item.MetricName
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("native metric name is empty")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate native metric name %q", name)
		}
		seen[name] = struct{}{}
		if item.Criterion == nil {
			return nil, fmt.Errorf("native metric %q has no criterion", name)
		}
		if !finiteConfigNumber(item.Threshold) {
			return nil, fmt.Errorf("native metric %q threshold is not finite", name)
		}
		results = append(results, item)
	}
	return results, nil
}

func buildDatasetSpec(
	set *evalset.EvalSet,
	evalSetHash string,
	metricsHash string,
	metrics []*metric.EvalMetric,
) (DatasetSpec, error) {
	if set == nil {
		return DatasetSpec{}, errors.New("eval set is nil")
	}
	if strings.TrimSpace(set.EvalSetID) == "" {
		return DatasetSpec{}, errors.New("eval set id is empty")
	}
	if len(set.EvalCases) == 0 {
		return DatasetSpec{}, fmt.Errorf("eval set %q has no cases", set.EvalSetID)
	}
	spec := DatasetSpec{
		EvalSetID:             set.EvalSetID,
		EvalSetHash:           evalSetHash,
		MetricsHash:           metricsHash,
		NormalizedInputHashes: make(map[string]string, len(set.EvalCases)),
	}
	inputOwners := make(map[string]string, len(set.EvalCases))
	for i, evalCase := range set.EvalCases {
		if evalCase == nil {
			return DatasetSpec{}, fmt.Errorf("eval case %d is nil", i+1)
		}
		caseID := strings.TrimSpace(evalCase.EvalID)
		if caseID == "" {
			return DatasetSpec{}, fmt.Errorf("eval case %d id is empty", i+1)
		}
		if _, exists := spec.NormalizedInputHashes[caseID]; exists {
			return DatasetSpec{}, fmt.Errorf("duplicate eval case id %q", caseID)
		}
		normalized, err := normalizedEvalCaseInput(evalCase)
		if err != nil {
			return DatasetSpec{}, fmt.Errorf("normalize eval case %q input: %w", caseID, err)
		}
		inputHash := hashBytes([]byte(normalized))
		if previous, exists := inputOwners[inputHash]; exists {
			return DatasetSpec{}, fmt.Errorf("eval cases %q and %q have duplicate normalized input", previous, caseID)
		}
		inputOwners[inputHash] = caseID
		spec.CaseIDs = append(spec.CaseIDs, caseID)
		spec.NormalizedInputHashes[caseID] = inputHash
	}
	for _, item := range metrics {
		spec.MetricNames = append(spec.MetricNames, item.MetricName)
	}
	sort.Strings(spec.CaseIDs)
	sort.Strings(spec.MetricNames)
	return spec, nil
}

const normalizedEvalCaseInputVersion = "inference-input-v2"

type canonicalEvalCaseInput struct {
	Version         string                         `json:"version"`
	Scenario        *canonicalConversationScenario `json:"scenario,omitempty"`
	ContextMessages []*model.Message               `json:"contextMessages,omitempty"`
	SessionInput    *canonicalSessionInput         `json:"sessionInput,omitempty"`
	Turns           []canonicalInputTurn           `json:"turns,omitempty"`
}

type canonicalConversationScenario struct {
	Driver                string `json:"driver"`
	StartingPrompt        string `json:"startingPrompt"`
	ConversationPlan      string `json:"conversationPlan"`
	StopSignal            string `json:"stopSignal"`
	MaxAllowedInvocations *int   `json:"maxAllowedInvocations"`
}

type canonicalSessionInput struct {
	AppName string `json:"appName"`
	UserID  string `json:"userId"`
	State   any    `json:"state"`
}

type canonicalInputTurn struct {
	UserContent *model.Message `json:"userContent"`
	ToolMock    any            `json:"toolMock,omitempty"`
}

func normalizedEvalCaseInput(evalCase *evalset.EvalCase) (string, error) {
	if evalCase == nil {
		return "", errors.New("eval case is nil")
	}
	identity := canonicalEvalCaseInput{Version: normalizedEvalCaseInputVersion}
	var err error
	identity.ContextMessages, err = canonicalInputMessages(evalCase.ContextMessages)
	if err != nil {
		return "", fmt.Errorf("canonicalize context messages: %w", err)
	}
	if evalCase.SessionInput != nil {
		state, err := canonicalInputValue(evalCase.SessionInput.State)
		if err != nil {
			return "", fmt.Errorf("canonicalize session state: %w", err)
		}
		identity.SessionInput = &canonicalSessionInput{
			AppName: evalCase.SessionInput.AppName,
			UserID:  evalCase.SessionInput.UserID,
			State:   state,
		}
	}

	hasUserInput := false
	if scenario := evalCase.ConversationScenario; scenario != nil {
		identity.Scenario = &canonicalConversationScenario{
			Driver:                string(scenario.Driver),
			StartingPrompt:        normalizeInputText(scenario.StartingPrompt),
			ConversationPlan:      normalizeInputText(scenario.ConversationPlan),
			StopSignal:            normalizeInputText(scenario.StopSignal),
			MaxAllowedInvocations: scenario.MaxAllowedInvocations,
		}
		hasUserInput = identity.Scenario.StartingPrompt != "" ||
			identity.Scenario.ConversationPlan != ""
	} else {
		invocations := evalCase.Conversation
		if evalCase.EvalMode == evalset.EvalModeTrace && len(evalCase.ActualConversation) > 0 {
			invocations = evalCase.ActualConversation
		}
		identity.Turns = make([]canonicalInputTurn, len(invocations))
		for i, invocation := range invocations {
			if invocation == nil {
				continue
			}
			identity.Turns[i].UserContent, err = canonicalInputMessage(invocation.UserContent)
			if err != nil {
				return "", fmt.Errorf("canonicalize user message %d: %w", i+1, err)
			}
			hasUserInput = hasUserInput || invocation.UserContent != nil
			if evalCase.EvalMode != evalset.EvalModeTrace &&
				invocation.ToolMock != nil &&
				len(invocation.ToolMock.Actual) > 0 {
				identity.Turns[i].ToolMock, err = canonicalInputValue(invocation.ToolMock.Actual)
				if err != nil {
					return "", fmt.Errorf("canonicalize tool mock %d: %w", i+1, err)
				}
			}
		}
	}
	if !hasUserInput {
		return "", errors.New("user input is empty")
	}
	data, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("marshal canonical input: %w", err)
	}
	return string(data), nil
}

func canonicalInputMessages(messages []*model.Message) ([]*model.Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	result := make([]*model.Message, len(messages))
	for i, message := range messages {
		copied, err := canonicalInputMessage(message)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i+1, err)
		}
		result[i] = copied
	}
	return result, nil
}

func canonicalInputMessage(message *model.Message) (*model.Message, error) {
	if message == nil {
		return nil, nil
	}
	copied := *message
	copied.Role = model.Role(normalizeInputText(string(message.Role)))
	copied.Content = normalizeInputText(message.Content)
	copied.ReasoningContent = normalizeInputText(message.ReasoningContent)
	copied.ContentParts = append([]model.ContentPart(nil), message.ContentParts...)
	for i := range copied.ContentParts {
		part := &copied.ContentParts[i]
		if part.Text != nil {
			text := normalizeInputText(*part.Text)
			part.Text = &text
		}
	}
	copied.ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
	for i := range copied.ToolCalls {
		call := &copied.ToolCalls[i]
		call.Function.Description = normalizeInputText(call.Function.Description)
		call.Function.Arguments = bytes.Clone(call.Function.Arguments)
		extraFields, err := canonicalInputValue(call.ExtraFields)
		if err != nil {
			return nil, fmt.Errorf("canonicalize tool call extra fields: %w", err)
		}
		if extraFields == nil {
			call.ExtraFields = nil
		} else {
			call.ExtraFields = extraFields.(map[string]any)
		}
	}
	return &copied, nil
}

func canonicalInputValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func normalizeInputText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func validateHeldoutExclusion(train, validation DatasetSpec) error {
	if train.EvalSetID == validation.EvalSetID {
		return fmt.Errorf("train and validation eval set ids collide: %q", train.EvalSetID)
	}
	trainIDs := configStringSet(train.CaseIDs)
	trainInputs := make(map[string]string, len(train.NormalizedInputHashes))
	for caseID, hash := range train.NormalizedInputHashes {
		trainInputs[hash] = caseID
	}
	for caseID, hash := range validation.NormalizedInputHashes {
		if _, exists := trainIDs[caseID]; exists {
			return fmt.Errorf("held-out leakage: case id %q appears in train and validation", caseID)
		}
		if trainCaseID, exists := trainInputs[hash]; exists {
			return fmt.Errorf(
				"held-out leakage: train case %q and validation case %q have the same normalized input",
				trainCaseID,
				caseID,
			)
		}
	}
	return nil
}

func validateInternalValidation(policy PromptIterPolicy, train, validation DatasetSpec) error {
	if policy.InternalValidationStrategy == internalValidationTrainAll {
		return nil
	}
	trainIDs := configStringSet(train.CaseIDs)
	validationIDs := configStringSet(validation.CaseIDs)
	for _, caseID := range policy.InternalValidationCaseIDs {
		if _, exists := validationIDs[caseID]; exists {
			return fmt.Errorf("held-out case %q cannot be used for PromptIter internal validation", caseID)
		}
		if _, exists := trainIDs[caseID]; !exists {
			return fmt.Errorf("internal validation case %q is not in the train inventory", caseID)
		}
	}
	return nil
}

func validateConfiguredCases(config RegressionConfig, validation DatasetSpec) error {
	validationIDs := configStringSet(validation.CaseIDs)
	for _, item := range []struct {
		role string
		ids  []string
	}{
		{role: "critical", ids: config.CriticalCaseIDs},
		{role: "hard failure", ids: config.HardFailureCaseIDs},
	} {
		if err := validateUniqueNonEmpty(item.role+" case id", item.ids); err != nil {
			return err
		}
		for _, caseID := range item.ids {
			if _, exists := validationIDs[caseID]; !exists {
				return fmt.Errorf(
					"%s case id %q is not in validation inventory (held-out validation)",
					item.role,
					caseID,
				)
			}
		}
	}
	return nil
}

func validateGateMetrics(policy GatePolicy, metricNames []string) error {
	known := configStringSet(metricNames)
	if _, exists := known[policy.PrimaryMetric]; !exists {
		return fmt.Errorf("gate primary metric %q is not in native metric inventory", policy.PrimaryMetric)
	}
	for metricName := range policy.MetricDirections {
		if _, exists := known[metricName]; !exists {
			return fmt.Errorf(
				"metric direction inventory references unknown metric %q",
				metricName,
			)
		}
	}
	for _, metricName := range metricNames {
		if _, exists := policy.MetricDirections[metricName]; !exists {
			return fmt.Errorf("native metric %q has no configured direction", metricName)
		}
	}
	return nil
}

func profileFromPrompt(promptText, surfaceID string) *promptiter.Profile {
	value := promptText
	return &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: surfaceID,
		Value:     astructure.SurfaceValue{Text: &value},
	}}}
}

func validateUniqueNonEmpty(role string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", role)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", role, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func configStringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved, nil
	}
	return filepath.Clean(absolute), nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hashStrings(values ...string) string {
	hasher := sha256.New()
	for _, value := range values {
		hasher.Write([]byte(value))
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func finiteConfigNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

type inputEvalSetLocator struct {
	paths map[string]string
}

func (l *inputEvalSetLocator) Build(_, _, evalSetID string) string {
	if path, exists := l.paths[evalSetID]; exists {
		return path
	}
	return filepath.Join("__unknown_eval_set__", evalSetID+".evalset.json")
}

func (l *inputEvalSetLocator) List(_, _ string) ([]string, error) {
	ids := make([]string, 0, len(l.paths))
	for id := range l.paths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

type inputMetricLocator struct {
	path string
}

func (l *inputMetricLocator) Build(_, _, _ string) string {
	return l.path
}
