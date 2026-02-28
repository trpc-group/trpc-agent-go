//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides memory evaluation benchmark for trpc-agent-go.
// Evaluates long-term conversational memory using the LoCoMo dataset.
//
// Evaluation Scenarios:
//   - Long-Context: Full conversation as context (baseline).
//   - Agentic: Agent with memory tools (add/update/search/load).
//   - Auto: Automatic memory extraction + search.
//
// Memory Backends:
//   - inmemory: In-memory storage (keyword-based).
//   - sqlite: SQLite storage (keyword-based).
//   - sqlitevec: SQLite + sqlite-vec (vector similarity).
//   - pgvector: PostgreSQL with vector similarity.
//   - mysql: MySQL storage (full-text search).
//
// Metrics (aligned with LoCoMo paper):
//   - F1 Score: Token-level F1.
//   - BLEU Score: N-gram overlap.
//   - LLM-score: LLM-as-Judge evaluation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/scenarios"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Command-line flags.
var (
	flagModel     = flag.String("model", "", "Model name (env MODEL_NAME or gpt-4o-mini)")
	flagEvalModel = flag.String("eval-model", "", "Evaluation model for LLM judge")
	flagDataset   = flag.String("dataset", "../data", "Dataset directory")
	flagDataFile  = flag.String("data-file", "locomo10.json", "Dataset file name")
	flagOutput    = flag.String("output", "../results", "Output directory")

	flagScenario = flag.String(
		"scenario",
		"long_context",
		"Evaluation scenario (comma-separated): "+
			"long_context, agentic, auto, all",
	)
	// Memory backend flags (comma-separated for multiple).
	flagMemoryBackends = flag.String(
		"memory-backend",
		"inmemory",
		"Memory backends (comma-separated): "+
			"inmemory, sqlite, sqlitevec, pgvector, mysql",
	)
	flagPGVectorDSN = flag.String(
		"pgvector-dsn",
		"",
		"PostgreSQL DSN for pgvector (env PGVECTOR_DSN)",
	)
	flagEmbedModel = flag.String(
		"embed-model",
		"",
		"Embedding model for vector backends (pgvector, sqlitevec) "+
			"(env EMBED_MODEL_NAME or text-embedding-3-small)",
	)
	flagMySQLDSN = flag.String(
		"mysql-dsn",
		"",
		"MySQL DSN for mysql backend (env MYSQL_DSN)",
	)

	flagSampleID          = flag.String("sample-id", "", "Filter by sample ID")
	flagCategory          = flag.String("category", "", "Filter by QA category")
	flagMaxTasks          = flag.Int("max-tasks", 0, "Maximum tasks (0=all)")
	flagMaxContext        = flag.Int("max-context", 128000, "Maximum context length")
	flagSessionEventLimit = flag.Int("session-event-limit", 1000, "Max events kept in each session (0=unlimited)")
	flagQAHistoryTurns    = flag.Int(
		"qa-history-turns", 0,
		"Recent conversation turns injected as context during QA (0=none, auto/agentic only)",
	)
	flagLLMJudge = flag.Bool("llm-judge", false, "Enable LLM-as-Judge evaluation")
	flagVerbose  = flag.Bool("verbose", false, "Verbose output")
	// Debug flags (auto scenario diagnosis).
	flagDebugDumpMemories = flag.Bool("debug-dump-memories", false, "Dump extracted memories (auto scenario only)")
	flagDebugMemLimit     = flag.Int("debug-mem-limit", 200, "Max memories to dump when debug-dump-memories is enabled")
	flagDebugQALimit      = flag.Int("debug-qa-limit", 5, "Dump retrieval hits for the first N questions (auto scenario only)")
	flagResume            = flag.Bool("resume", false, "Resume from checkpoint (TODO: implement)")
)

const (
	pgvectorTableDefault  = "memory_eval"
	pgvectorTableAuto     = "memory_eval_auto"
	mysqlTableDefault     = "memory_eval_mysql"
	mysqlTableAuto        = "memory_eval_auto_mysql"
	sqliteTableDefault    = "memory_eval_sqlite"
	sqliteTableAuto       = "memory_eval_auto_sqlite"
	sqliteVecTableDefault = "memory_eval_sqlitevec"
	sqliteVecTableAuto    = "memory_eval_auto_sqlitevec"

	autoMemoryAsyncWorkers = 3
	autoMemoryQueueSize    = 200
	autoMemoryJobTimeout   = 2 * time.Minute
)

// benchmarkExtractorPrompt is optimized for retrieval-based benchmark evaluation.
// The default memory extractor prompt is user-profile oriented; for benchmarks we
// need dense, queryable, factual memories (entities, dates, relations).
const benchmarkExtractorPrompt = `You are a memory extraction engine for retrieval-based QA benchmarks.

Goal: Extract factual, queryable memories from multi-session conversations so that downstream QA can answer questions by searching memories.

CRITICAL RULES (TIME):
- Do NOT use relative time words like "yesterday", "last week", "two days ago", "next month".
- Always write an ABSOLUTE DATE when possible:
  - Prefer ISO date: YYYY-MM-DD.
  - If only a textual date is available, keep it as-is (e.g., "7 May 2023", "late June 2023").
- Every memory MUST start with a date prefix:
  - Format: [DATE: <absolute-date-or-unknown>]
  - Examples: [DATE: 2023-05-07] ... , [DATE: 7 May 2023] ... , [DATE: late June 2023] ...
  - Use [DATE: unknown] only if no absolute date can be inferred from the provided context.

EXTRACTION RULES (CONTENT):
- Prefer atomic memories: one fact per memory.
- Extract concrete facts that can answer future questions: who/what/when/where/relationships/preferences/attributes/events.
- Include facts about all mentioned people (not only the user).
- Be comprehensive rather than conservative: store many small facts.
  - Aim for at least 3-8 atomic memories per session when possible.
- Do NOT guess. If not stated, omit it.
- Avoid vague summaries like "They discussed their plans".
- Avoid duplicates: update existing memories when the same fact is refined.

OUTPUT:
- Use the provided tools to add/update/delete memories.
- Use short topics (1-3) such as: person, event, date, location, preference.
`

type memoryMode string

const (
	memoryModeNone   memoryMode = "none"
	memoryModeManual memoryMode = "manual"
	memoryModeAuto   memoryMode = "auto"
)

type memoryConfig struct {
	backend string
	mode    memoryMode
}

// EvaluationResult holds the complete evaluation result.
type EvaluationResult struct {
	Metadata      *EvalMetadata                      `json:"metadata"`
	Summary       *EvalSummary                       `json:"summary"`
	ByCategory    map[string]metrics.CategoryMetrics `json:"by_category"`
	SampleResults []*scenarios.SampleResult          `json:"sample_results,omitempty"`
}

// EvalMetadata holds evaluation metadata.
type EvalMetadata struct {
	Framework      string    `json:"framework"`
	Version        string    `json:"version"`
	Timestamp      time.Time `json:"timestamp"`
	Model          string    `json:"model"`
	EvalModel      string    `json:"eval_model,omitempty"`
	Scenario       string    `json:"scenario"`
	MemoryBackend  string    `json:"memory_backend,omitempty"`
	MaxContext     int       `json:"max_context"`
	QAHistoryTurns int       `json:"qa_history_turns,omitempty"`
	LLMJudge       bool      `json:"llm_judge"`
}

// EvalSummary holds aggregated evaluation summary.
type EvalSummary struct {
	TotalSamples    int     `json:"total_samples"`
	TotalQuestions  int     `json:"total_questions"`
	OverallF1       float64 `json:"overall_f1"`
	OverallBLEU     float64 `json:"overall_bleu"`
	OverallLLMScore float64 `json:"overall_llm_score,omitempty"`
	TotalTimeMs     int64   `json:"total_time_ms"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`

	// Token usage statistics.
	TotalPromptTokens     int     `json:"total_prompt_tokens"`
	TotalCompletionTokens int     `json:"total_completion_tokens"`
	TotalTokens           int     `json:"total_tokens"`
	TotalLLMCalls         int     `json:"total_llm_calls"`
	AvgPromptTokensPerQA  float64 `json:"avg_prompt_tokens_per_qa"`
	AvgCompletionPerQA    float64 `json:"avg_completion_tokens_per_qa"`
	AvgLLMCallsPerQA      float64 `json:"avg_llm_calls_per_qa"`
}

func main() {
	flag.Parse()
	validateFlags()

	modelName := getModelName()
	evalModelName := getEvalModelName()
	outputDir := *flagOutput

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Parse memory backends.
	backends := parseMemoryBackends(*flagMemoryBackends)

	log.Printf("=== Memory Evaluation (LoCoMo Benchmark) ===")
	log.Printf("Model: %s", modelName)
	log.Printf("Eval Model: %s", evalModelName)
	log.Printf("Scenario: %s", *flagScenario)
	log.Printf("Memory Backends: %v", backends)
	log.Printf("LLM Judge: %v", *flagLLMJudge)
	if *flagQAHistoryTurns > 0 {
		log.Printf("QA History Turns: %d", *flagQAHistoryTurns)
	}
	log.Printf("Output: %s", outputDir)
	if *flagResume {
		log.Printf("Resume mode: enabled (checkpoint will be loaded if exists)")
	}

	// Load dataset.
	loader := dataset.NewLoader(*flagDataset)
	samples, err := loader.LoadSamples(*flagDataFile)
	if err != nil {
		log.Fatalf("Failed to load dataset: %v", err)
	}
	log.Printf("Loaded %d samples", len(samples))

	// Filter samples if specified.
	samples = filterSamples(samples)
	if len(samples) == 0 {
		log.Fatalf("No samples to evaluate")
	}

	// Apply max tasks limit.
	if *flagMaxTasks > 0 && *flagMaxTasks < len(samples) {
		samples = samples[:*flagMaxTasks]
		log.Printf("Limited to %d samples", len(samples))
	}

	// Create models.
	llm := openaimodel.New(modelName)
	var evalLLM = llm
	if evalModelName != "" && evalModelName != modelName {
		evalLLM = openaimodel.New(evalModelName)
	}

	// Base scenario config.
	baseConfig := scenarios.Config{
		MaxContext:        *flagMaxContext,
		EnableLLMJudge:    *flagLLMJudge,
		Verbose:           *flagVerbose,
		SessionEventLimit: *flagSessionEventLimit,
		QAHistoryTurns:    *flagQAHistoryTurns,
		DebugDumpMemories: *flagDebugDumpMemories,
		DebugMemLimit:     *flagDebugMemLimit,
		DebugQALimit:      *flagDebugQALimit,
	}

	// Determine scenarios to run.
	scenariosToRun := getScenarios(*flagScenario)

	// Run evaluation for each scenario and backend combination.
	for _, scenarioType := range scenariosToRun {
		// Long-context doesn't need memory backends.
		if scenarioType == scenarios.ScenarioLongContext {
			runScenario(samples, llm, evalLLM, scenarioType, "", baseConfig, outputDir)
			continue
		}

		// Run each backend for memory-based scenarios.
		for _, backend := range backends {
			runScenario(samples, llm, evalLLM, scenarioType, backend, baseConfig, outputDir)
		}
	}
}

func parseMemoryBackends(backendsStr string) []string {
	parts := strings.Split(backendsStr, ",")
	backends := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			backends = append(backends, p)
		}
	}
	return backends
}

func getScenarios(scenario string) []scenarios.ScenarioType {
	scenarioMap := map[string]scenarios.ScenarioType{
		"long_context": scenarios.ScenarioLongContext,
		"agentic":      scenarios.ScenarioAgentic,
		"auto":         scenarios.ScenarioAuto,
	}
	if scenario == "all" {
		return []scenarios.ScenarioType{
			scenarios.ScenarioLongContext,
			scenarios.ScenarioAgentic,
			scenarios.ScenarioAuto,
		}
	}
	// Support comma-separated scenarios.
	var result []scenarios.ScenarioType
	seen := make(map[string]bool)
	for _, s := range strings.Split(scenario, ",") {
		s = strings.TrimSpace(s)
		if seen[s] {
			continue
		}
		seen[s] = true
		st, ok := scenarioMap[s]
		if !ok {
			log.Fatalf("Invalid scenario: %s", s)
		}
		result = append(result, st)
	}
	return result
}

func filterSamples(samples []*dataset.LoCoMoSample) []*dataset.LoCoMoSample {
	// Filter by sample ID.
	if *flagSampleID != "" {
		filtered := make([]*dataset.LoCoMoSample, 0)
		for _, s := range samples {
			if s.SampleID == *flagSampleID {
				filtered = append(filtered, s)
			}
		}
		samples = filtered
		log.Printf("Filtered to %d samples (sample_id=%s)", len(samples), *flagSampleID)
	}

	// Filter by category.
	if *flagCategory != "" {
		for _, s := range samples {
			filtered := make([]dataset.QAItem, 0)
			for _, qa := range s.QA {
				if qa.Category == *flagCategory {
					filtered = append(filtered, qa)
				}
			}
			s.QA = filtered
		}
		log.Printf("Filtered QA by category: %s", *flagCategory)
	}
	return samples
}

func runScenario(
	samples []*dataset.LoCoMoSample,
	llm, evalLLM model.Model,
	scenarioType scenarios.ScenarioType,
	backend string,
	baseConfig scenarios.Config,
	outputDir string,
) {
	config := baseConfig
	config.Scenario = scenarioType

	var evaluator scenarios.Evaluator
	var memSvc memory.Service
	var err error
	memCfg := buildMemoryConfig(scenarioType, backend)
	memOpts := buildMemoryServiceOptions(memCfg, llm)

	switch scenarioType {
	case scenarios.ScenarioLongContext:
		evaluator = scenarios.NewLongContextEvaluator(llm, evalLLM, config)
	case scenarios.ScenarioAgentic:
		memSvc, err = createMemoryService(memCfg, memOpts)
		if err != nil {
			log.Printf("Failed to create %s memory service: %v", backend, err)
			return
		}
		evaluator = scenarios.NewAgenticEvaluator(llm, evalLLM, memSvc, config)
	case scenarios.ScenarioAuto:
		memSvc, err = createMemoryService(memCfg, memOpts)
		if err != nil {
			log.Printf("Failed to create %s memory service: %v", backend, err)
			return
		}
		evaluator = scenarios.NewAutoEvaluator(llm, evalLLM, memSvc, config)
	}

	// Determine output directory.
	scenarioDir := buildScenarioDir(outputDir, scenarioType, backend)
	if err := os.MkdirAll(scenarioDir, 0755); err != nil {
		log.Printf("Failed to create scenario directory: %v", err)
		return
	}

	log.Printf("")
	log.Printf("=== Running: %s (backend=%s) ===", evaluator.Name(), backend)

	result := runEvaluation(samples, evaluator, config, backend)
	saveResults(scenarioDir, result)
	printSummary(result)

	// Cleanup memory service.
	if memSvc != nil {
		memSvc.Close()
	}
}

func buildScenarioDir(outputDir string, scenario scenarios.ScenarioType, backend string) string {
	if scenario == scenarios.ScenarioLongContext {
		return filepath.Join(outputDir, string(scenario))
	}
	return filepath.Join(outputDir, fmt.Sprintf("%s_%s", scenario, backend))
}

func validateFlags() {
	validScenarios := map[string]bool{
		"long_context": true,
		"agentic":      true,
		"auto":         true,
		"all":          true,
	}
	for _, s := range strings.Split(*flagScenario, ",") {
		s = strings.TrimSpace(s)
		if !validScenarios[s] {
			log.Fatalf("Invalid scenario: %s", s)
		}
	}

	validBackends := map[string]bool{
		"inmemory":  true,
		"sqlite":    true,
		"sqlitevec": true,
		"pgvector":  true,
		"mysql":     true,
	}
	for _, b := range parseMemoryBackends(*flagMemoryBackends) {
		if !validBackends[b] {
			log.Fatalf("Invalid memory backend: %s", b)
		}
	}

	if *flagMaxContext <= 0 {
		log.Fatalf("Invalid max-context: %d", *flagMaxContext)
	}
	if *flagSessionEventLimit < 0 {
		log.Fatalf("Invalid session-event-limit: %d", *flagSessionEventLimit)
	}
}

func getModelName() string {
	if *flagModel != "" {
		return *flagModel
	}
	if env := os.Getenv("MODEL_NAME"); env != "" {
		return env
	}
	return "gpt-4o-mini"
}

func getEvalModelName() string {
	if *flagEvalModel != "" {
		return *flagEvalModel
	}
	if env := os.Getenv("EVAL_MODEL_NAME"); env != "" {
		return env
	}
	return getModelName()
}

func getEmbedModelName() string {
	if *flagEmbedModel != "" {
		return *flagEmbedModel
	}
	if env := os.Getenv("EMBED_MODEL_NAME"); env != "" {
		return env
	}
	return "text-embedding-3-small"
}

const (
	envOpenAIBaseURL          = "OPENAI_BASE_URL"
	envOpenAIEmbeddingAPIKey  = "OPENAI_EMBEDDING_API_KEY"
	envOpenAIEmbeddingBaseURL = "OPENAI_EMBEDDING_BASE_URL"
)

func newEmbeddingEmbedder(modelName string) *openai.Embedder {
	opts := []openai.Option{
		openai.WithModel(modelName),
	}

	if apiKey := os.Getenv(envOpenAIEmbeddingAPIKey); apiKey != "" {
		opts = append(opts, openai.WithAPIKey(apiKey))
	}

	baseURL := os.Getenv(envOpenAIEmbeddingBaseURL)
	if baseURL == "" {
		baseURL = os.Getenv(envOpenAIBaseURL)
	}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}

	return openai.New(opts...)
}

func getPGVectorDSN() string {
	if *flagPGVectorDSN != "" {
		return *flagPGVectorDSN
	}
	return os.Getenv("PGVECTOR_DSN")
}

func getMySQLDSN() string {
	if *flagMySQLDSN != "" {
		return *flagMySQLDSN
	}
	return os.Getenv("MYSQL_DSN")
}

func buildMemoryConfig(
	scenarioType scenarios.ScenarioType,
	backend string,
) memoryConfig {
	switch scenarioType {
	case scenarios.ScenarioAuto:
		return memoryConfig{
			backend: backend,
			mode:    memoryModeAuto,
		}
	case scenarios.ScenarioAgentic:
		return memoryConfig{
			backend: backend,
			mode:    memoryModeManual,
		}
	default:
		return memoryConfig{
			mode: memoryModeNone,
		}
	}
}

func buildMemoryServiceOptions(
	cfg memoryConfig,
	extractorModel model.Model,
) memoryServiceOptions {
	if cfg.mode != memoryModeAuto {
		return memoryServiceOptions{}
	}
	return memoryServiceOptions{
		enableExtractor: true,
		extractorModel:  extractorModel,
	}
}

type memoryServiceOptions struct {
	enableExtractor bool
	extractorModel  model.Model
}

func createMemoryService(
	cfg memoryConfig,
	opts memoryServiceOptions,
) (memory.Service, error) {
	switch cfg.backend {
	case "pgvector":
		return createPGVectorService(opts)
	case "mysql":
		return createMySQLService(opts)
	case "sqlite":
		return createSQLiteService(opts)
	case "sqlitevec":
		return createSQLiteVecService(opts)
	default:
		return createInMemoryService(opts), nil
	}
}

func createPGVectorService(
	opts memoryServiceOptions,
) (memory.Service, error) {
	dsn := getPGVectorDSN()
	if dsn == "" {
		return nil, fmt.Errorf(
			"pgvector-dsn or PGVECTOR_DSN is required for pgvector backend",
		)
	}
	embedModelName := getEmbedModelName()
	emb := newEmbeddingEmbedder(embedModelName)
	tableName := pgvectorTableDefault
	var ext extractor.MemoryExtractor
	if opts.enableExtractor {
		log.Printf(
			"Creating pgvector memory service with extractor "+
				"(embed_model=%s)",
			embedModelName,
		)
		tableName = pgvectorTableAuto
		ext = extractor.NewExtractor(opts.extractorModel, extractor.WithPrompt(benchmarkExtractorPrompt))
	} else {
		log.Printf(
			"Creating pgvector memory service (embed_model=%s)",
			embedModelName,
		)
	}
	svcOpts := []memorypgvector.ServiceOpt{
		memorypgvector.WithPGVectorClientDSN(dsn),
		memorypgvector.WithEmbedder(emb),
		memorypgvector.WithTableName(tableName),
		memorypgvector.WithExtractor(ext),
	}
	if opts.enableExtractor {
		svcOpts = append(svcOpts,
			memorypgvector.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			memorypgvector.WithMemoryQueueSize(autoMemoryQueueSize),
			memorypgvector.WithMemoryJobTimeout(autoMemoryJobTimeout),
		)
	}
	return memorypgvector.NewService(svcOpts...)
}

func createMySQLService(
	opts memoryServiceOptions,
) (memory.Service, error) {
	dsn := getMySQLDSN()
	if dsn == "" {
		return nil, fmt.Errorf(
			"mysql-dsn or MYSQL_DSN is required for mysql backend",
		)
	}

	tableName := mysqlTableDefault
	var ext extractor.MemoryExtractor
	if opts.enableExtractor {
		log.Printf("Creating mysql memory service with extractor")
		tableName = mysqlTableAuto
		ext = extractor.NewExtractor(opts.extractorModel, extractor.WithPrompt(benchmarkExtractorPrompt))
	} else {
		log.Printf("Creating mysql memory service")
	}

	svcOpts := []memorymysql.ServiceOpt{
		memorymysql.WithMySQLClientDSN(dsn),
		memorymysql.WithTableName(tableName),
		memorymysql.WithExtractor(ext),
	}
	if opts.enableExtractor {
		svcOpts = append(svcOpts,
			memorymysql.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			memorymysql.WithMemoryQueueSize(autoMemoryQueueSize),
			memorymysql.WithMemoryJobTimeout(autoMemoryJobTimeout),
		)
	}
	return memorymysql.NewService(svcOpts...)
}

func createInMemoryService(opts memoryServiceOptions) memory.Service {
	if opts.enableExtractor {
		log.Printf("Creating inmemory memory service with extractor")
		ext := extractor.NewExtractor(opts.extractorModel, extractor.WithPrompt(benchmarkExtractorPrompt))
		return inmemory.NewMemoryService(
			inmemory.WithExtractor(ext),
			inmemory.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			inmemory.WithMemoryQueueSize(autoMemoryQueueSize),
			inmemory.WithMemoryJobTimeout(autoMemoryJobTimeout),
		)
	}
	return inmemory.NewMemoryService()
}

func runEvaluation(
	samples []*dataset.LoCoMoSample,
	evaluator scenarios.Evaluator,
	config scenarios.Config,
	backend string,
) *EvaluationResult {
	startTime := time.Now()
	catAgg := metrics.NewCategoryAggregator()
	sampleResults := make([]*scenarios.SampleResult, 0, len(samples))
	var totalQuestions int
	var totalUsage scenarios.TokenUsage

	for i, sample := range samples {
		log.Printf("[%d/%d] Evaluating sample: %s (%d QA)",
			i+1, len(samples), sample.SampleID, len(sample.QA))

		sampleStart := time.Now()
		result, err := evaluator.Evaluate(context.Background(), sample)
		if err != nil {
			log.Printf("  Error: %v", err)
			continue
		}

		sampleResults = append(sampleResults, result)
		totalQuestions += len(result.QAResults)

		// Aggregate category metrics.
		for _, qaResult := range result.QAResults {
			catAgg.Add(qaResult.Category, qaResult.Metrics)
		}

		// Aggregate token usage.
		if result.TokenUsage != nil {
			totalUsage.Add(*result.TokenUsage)
		}

		log.Printf("  Completed in %v | F1=%.3f BLEU=%.3f",
			time.Since(sampleStart).Round(time.Millisecond),
			result.Overall.F1,
			result.Overall.BLEU)
		if result.TokenUsage != nil &&
			result.TokenUsage.LLMCalls > 0 {
			log.Printf(
				"  Tokens: prompt=%d completion=%d calls=%d",
				result.TokenUsage.PromptTokens,
				result.TokenUsage.CompletionTokens,
				result.TokenUsage.LLMCalls,
			)
		}
	}

	totalTime := time.Since(startTime)
	overall := catAgg.GetOverall()

	qCount := max(totalQuestions, 1)
	return &EvaluationResult{
		Metadata: &EvalMetadata{
			Framework:      "trpc-agent-go",
			Version:        "1.0.0",
			Timestamp:      time.Now(),
			Model:          getModelName(),
			EvalModel:      getEvalModelName(),
			Scenario:       string(config.Scenario),
			MemoryBackend:  backend,
			MaxContext:     config.MaxContext,
			QAHistoryTurns: config.QAHistoryTurns,
			LLMJudge:       config.EnableLLMJudge,
		},
		Summary: &EvalSummary{
			TotalSamples:          len(sampleResults),
			TotalQuestions:        totalQuestions,
			OverallF1:             overall.F1,
			OverallBLEU:           overall.BLEU,
			OverallLLMScore:       overall.LLMScore,
			TotalTimeMs:           totalTime.Milliseconds(),
			AvgLatencyMs:          float64(totalTime.Milliseconds()) / float64(qCount),
			TotalPromptTokens:     totalUsage.PromptTokens,
			TotalCompletionTokens: totalUsage.CompletionTokens,
			TotalTokens:           totalUsage.TotalTokens,
			TotalLLMCalls:         totalUsage.LLMCalls,
			AvgPromptTokensPerQA:  float64(totalUsage.PromptTokens) / float64(qCount),
			AvgCompletionPerQA:    float64(totalUsage.CompletionTokens) / float64(qCount),
			AvgLLMCallsPerQA:      float64(totalUsage.LLMCalls) / float64(qCount),
		},
		ByCategory:    catAgg.GetCategoryMetrics(),
		SampleResults: sampleResults,
	}
}

func saveResults(outputDir string, result *EvaluationResult) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Printf("Failed to create output directory: %v", err)
		return
	}

	// Save full results.
	resultsPath := filepath.Join(outputDir, "results.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal results: %v", err)
		return
	}
	if err := os.WriteFile(resultsPath, data, 0644); err != nil {
		log.Printf("Failed to write results: %v", err)
		return
	}
	log.Printf("Results saved to: %s", resultsPath)

	// Save checkpoint (same as results for now).
	checkpointPath := filepath.Join(outputDir, "checkpoint.json")
	if err := os.WriteFile(checkpointPath, data, 0644); err != nil {
		log.Printf("Failed to write checkpoint: %v", err)
	}
}

func printSummary(result *EvaluationResult) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Memory Evaluation Results - %s\n", result.Metadata.Scenario)
	fmt.Println(strings.Repeat("=", 60))

	fmt.Printf("\nModel: %s\n", result.Metadata.Model)
	fmt.Printf("Scenario: %s\n", result.Metadata.Scenario)
	if result.Metadata.MemoryBackend != "" {
		fmt.Printf("Memory Backend: %s\n",
			result.Metadata.MemoryBackend)
	}
	if result.Metadata.QAHistoryTurns > 0 {
		fmt.Printf("QA History Turns: %d\n",
			result.Metadata.QAHistoryTurns)
	}
	fmt.Printf("Samples: %d | Questions: %d\n",
		result.Summary.TotalSamples, result.Summary.TotalQuestions)

	fmt.Println("\n--- Overall Metrics ---")
	fmt.Printf("F1 Score:   %.4f (%.1f)\n", result.Summary.OverallF1, result.Summary.OverallF1*100)
	fmt.Printf("BLEU Score: %.4f\n", result.Summary.OverallBLEU)
	if result.Summary.OverallLLMScore > 0 {
		fmt.Printf("LLM Score:  %.4f\n", result.Summary.OverallLLMScore)
	}
	fmt.Printf("Total Time: %dms | Avg Latency: %.1fms\n",
		result.Summary.TotalTimeMs, result.Summary.AvgLatencyMs)

	if result.Summary.TotalLLMCalls > 0 {
		fmt.Println("\n--- Token Usage ---")
		fmt.Printf("Prompt Tokens:     %d (avg %.0f/QA)\n",
			result.Summary.TotalPromptTokens,
			result.Summary.AvgPromptTokensPerQA)
		fmt.Printf("Completion Tokens: %d (avg %.0f/QA)\n",
			result.Summary.TotalCompletionTokens,
			result.Summary.AvgCompletionPerQA)
		fmt.Printf("Total Tokens:      %d\n",
			result.Summary.TotalTokens)
		fmt.Printf("LLM Calls:         %d (avg %.1f/QA)\n",
			result.Summary.TotalLLMCalls,
			result.Summary.AvgLLMCallsPerQA)
	}

	fmt.Println("\n--- By Category ---")
	fmt.Printf("%-15s %8s %8s %8s %8s\n", "Category", "Count", "F1", "BLEU", "LLM")
	fmt.Println(strings.Repeat("-", 51))

	categories := []string{"single-hop", "multi-hop", "temporal", "open-domain", "adversarial"}
	for _, cat := range categories {
		if m, ok := result.ByCategory[cat]; ok {
			llmStr := "-"
			if m.LLMScore > 0 {
				llmStr = fmt.Sprintf("%.3f", m.LLMScore)
			}
			fmt.Printf("%-15s %8d %8.3f %8.3f %8s\n",
				cat, m.Count, m.F1, m.BLEU, llmStr)
		}
	}

	// Print any other categories not in the standard list.
	for cat, m := range result.ByCategory {
		found := false
		for _, c := range categories {
			if c == cat {
				found = true
				break
			}
		}
		if !found {
			llmStr := "-"
			if m.LLMScore > 0 {
				llmStr = fmt.Sprintf("%.3f", m.LLMScore)
			}
			fmt.Printf("%-15s %8d %8.3f %8.3f %8s\n",
				cat, m.Count, m.F1, m.BLEU, llmStr)
		}
	}

	fmt.Println(strings.Repeat("=", 60))
}
