package dedup

import (
	"context"
	"testing"

	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/state"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func makeFinding(file string, line int, cat string, conf float64, source string) types.Finding {
	return types.Finding{
		ID:           file + cat + string(rune(line)),
		File:         file,
		Line:         line,
		Category:     cat,
		Confidence:   conf,
		Source:       source,
		DecisionKind: "heuristic",
		Severity:     "medium",
	}
}

func TestDedup_SameLocationHigherConfidenceWins(t *testing.T) {
	ruleFindings := []types.Finding{
		makeFinding("a.go", 10, "security", 0.7, "rule_engine"),
	}
	llmFindings := []types.Finding{
		makeFinding("a.go", 10, "security", 0.9, "llm"),
	}
	gs := graph.State{
		state.StateKeyRuleFindings: ruleFindings,
		state.StateKeyLLMFindings:  llmFindings,
		state.StateKeyDedupConfig:  types.DedupConfig{ConfidenceThreshold: 0.6},
	}

	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Dedup failed: %v", err)
	}
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyFindings].([]types.Finding)
	warnings, _ := finalState[state.StateKeyWarnings].([]types.Finding)

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Confidence != 0.9 {
		t.Errorf("expected confidence 0.9 (higher wins), got %.1f", findings[0].Confidence)
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d", len(warnings))
	}
}

func TestDedup_DifferentCategoriesBothKept(t *testing.T) {
	ruleFindings := []types.Finding{
		makeFinding("a.go", 10, "security", 1.0, "rule_engine"),
	}
	llmFindings := []types.Finding{
		makeFinding("a.go", 10, "error_handling", 0.8, "llm"),
	}
	gs := graph.State{
		state.StateKeyRuleFindings: ruleFindings,
		state.StateKeyLLMFindings:  llmFindings,
		state.StateKeyDedupConfig:  types.DedupConfig{ConfidenceThreshold: 0.6},
	}

	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyFindings].([]types.Finding)

	if len(findings) != 2 {
		t.Fatalf("different categories should both be kept, got %d", len(findings))
	}
}

func TestDedup_LowConfidenceGoesToWarnings(t *testing.T) {
	llmFindings := []types.Finding{
		makeFinding("a.go", 10, "style", 0.3, "llm"),
		makeFinding("b.go", 20, "style", 0.9, "llm"),
	}
	gs := graph.State{
		state.StateKeyLLMFindings: llmFindings,
		state.StateKeyDedupConfig: types.DedupConfig{ConfidenceThreshold: 0.6},
	}

	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyFindings].([]types.Finding)
	warnings, _ := finalState[state.StateKeyWarnings].([]types.Finding)

	if len(findings) != 1 {
		t.Errorf("expected 1 finding (0.9 > threshold), got %d", len(findings))
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning (0.3 < threshold), got %d", len(warnings))
	}
	if len(warnings) > 0 && warnings[0].Severity != "warning" {
		t.Errorf("warning severity should be 'warning', got %q", warnings[0].Severity)
	}
}

func TestDedup_EmptyInputs(t *testing.T) {
	gs := graph.State{
		state.StateKeyDedupConfig: types.DedupConfig{ConfidenceThreshold: 0.6},
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyFindings].([]types.Finding)
	warnings, _ := finalState[state.StateKeyWarnings].([]types.Finding)

	if len(findings) != 0 || len(warnings) != 0 {
		t.Errorf("empty inputs should produce empty outputs, got f=%d w=%d", len(findings), len(warnings))
	}
}

func TestDedup_MaxFindingsCaps(t *testing.T) {
	var many []types.Finding
	for i := 0; i < 10; i++ {
		many = append(many, makeFinding("a.go", i, "style", 0.9, "llm"))
	}
	gs := graph.State{
		state.StateKeyLLMFindings: many,
		state.StateKeyDedupConfig: types.DedupConfig{
			ConfidenceThreshold: 0.6,
			MaxFindingsPerFile:  5,
			MaxTotalFindings:    5,
		},
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyFindings].([]types.Finding)

	if len(findings) > 5 {
		t.Errorf("expected at most 5 findings (capped), got %d", len(findings))
	}
}
