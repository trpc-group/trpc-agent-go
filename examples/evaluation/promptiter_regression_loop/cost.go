package main

import (
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// CostReport estimates the cost of a pipeline run. In fake mode the numbers are
// deterministic (driven by case/worker counts and configured unit prices) so the
// cost-budget gate is fully testable without an API key. In real mode the same
// formula applies using the actual evaluated case counts.
type CostReport struct {
	EvalUnits      int     `json:"evalUnits"`
	WorkerUnits    int     `json:"workerUnits"`
	UnitEvalCost   float64 `json:"unitEvalCost"`
	UnitWorkerCost float64 `json:"unitWorkerCost"`
	Total          float64 `json:"total"`
}

// estimateCost derives a cost estimate from the run result plus the configured
// unit prices:
//   - eval units  = baseline validation + per round (train + validation) evals
//   - worker units = per round (one backward gradient per train case + aggregate + optimize)
func estimateCost(cfg regressionConfig, result *promptiterengine.RunResult) *CostReport {
	valCases := countCases(result.BaselineValidation)
	trainCases := 0
	rounds := len(result.Rounds)
	if rounds > 0 && result.Rounds[0].Train != nil {
		trainCases = countCases(result.Rounds[0].Train)
	}
	evalUnits := valCases
	workerUnits := 0
	for i := 0; i < rounds; i++ {
		evalUnits += trainCases + valCases
		// backward (one gradient per train case) + aggregate + optimize
		workerUnits += trainCases + 2
	}
	return &CostReport{
		EvalUnits:      evalUnits,
		WorkerUnits:    workerUnits,
		UnitEvalCost:   cfg.CostPerEval,
		UnitWorkerCost: cfg.CostPerWorker,
		Total:          float64(evalUnits)*cfg.CostPerEval + float64(workerUnits)*cfg.CostPerWorker,
	}
}

func countCases(res *promptiterengine.EvaluationResult) int {
	if res == nil {
		return 0
	}
	n := 0
	for _, es := range res.EvalSets {
		n += len(es.Cases)
	}
	return n
}

// ConfigSnapshot is the serializable subset of regressionConfig persisted to the
// audit report so a run is reproducible from the artifact alone.
type ConfigSnapshot struct {
	AppName              string   `json:"appName"`
	CandidateAgentName   string   `json:"candidateAgentName"`
	PromptType           string   `json:"promptType"`
	TargetSurfaces       []string `json:"targetSurfaces"`
	TrainEvalSetIDs      []string `json:"trainEvalSetIDs"`
	ValidationEvalSetIDs []string `json:"validationEvalSetIDs"`
	MetricFileID         string   `json:"metricFileID"`
	CandidateModelName   string   `json:"candidateModelName"`
	JudgeModelName       string   `json:"judgeModelName"`
	WorkerModelName      string   `json:"workerModelName"`
	MinScoreGain         float64  `json:"minScoreGain"`
	MaxRounds            int      `json:"maxRounds"`
	TargetScore          float64  `json:"targetScore"`
	KeyCaseIDs           []string `json:"keyCaseIDs"`
	CostPerEval          float64  `json:"costPerEval"`
	CostPerWorker        float64  `json:"costPerWorker"`
	CostBudget           float64  `json:"costBudget"`
	Seed                 int      `json:"seed"`
	Fake                 bool     `json:"fake"`
	FakeScenario         string   `json:"fakeScenario"`
	DataDir              string   `json:"dataDir"`
	OutputDir            string   `json:"outputDir"`
}

func snapshotConfig(cfg regressionConfig) ConfigSnapshot {
	return ConfigSnapshot{
		AppName:              appName,
		CandidateAgentName:   candidateAgentName,
		PromptType:           cfg.PromptType,
		TargetSurfaces:       cfg.TargetSurfaces,
		TrainEvalSetIDs:      []string{cfg.TrainEvalSetID},
		ValidationEvalSetIDs: []string{cfg.ValidationEvalSetID},
		MetricFileID:         cfg.MetricFileID,
		CandidateModelName:   cfg.CandidateModelName,
		JudgeModelName:       cfg.JudgeModelName,
		WorkerModelName:      cfg.WorkerModelName,
		MinScoreGain:         cfg.MinScoreGain,
		MaxRounds:            cfg.MaxRounds,
		TargetScore:          cfg.TargetScore,
		KeyCaseIDs:           cfg.KeyCaseIDs,
		CostPerEval:          cfg.CostPerEval,
		CostPerWorker:        cfg.CostPerWorker,
		CostBudget:           cfg.CostBudget,
		Seed:                 cfg.Seed,
		Fake:                 cfg.Fake,
		FakeScenario:         string(cfg.FakeScenario),
		DataDir:              cfg.DataDir,
		OutputDir:            cfg.OutputDir,
	}
}
