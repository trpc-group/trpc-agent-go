package response

import (
    "context"
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
)

// Evaluator implements a lightweight response match metric that performs a
// normalized string comparison between the agent's final response and the
// reference response for each invocation.
type Evaluator struct{}

// New creates a new response evaluator.
func New() *Evaluator { return &Evaluator{} }

// Evaluate compares response quality between actual and expected invocations.
//
// The current implementation applies a strict match after normalizing
// whitespace and casing. A score of 1.0 indicates an exact match, while 0.0
// signals a mismatch. Missing invocations are treated as empty responses.
func (e *Evaluator) Evaluate(ctx context.Context, actual,
    expected []evalset.Invocation) (*evaluator.EvaluationResult, error) {
    _ = ctx
    result := &evaluator.EvaluationResult{
        OverallStatus:        evalresult.EvalStatusNotEvaluated,
        PerInvocationResults: []evaluator.PerInvocationResult{},
    }

    if len(expected) == 0 {
        return result, nil
    }

    totalScore := 0.0
    passedCount := 0
    notEvaluatedCount := 0
    perInvocation := make([]evaluator.PerInvocationResult, 0, len(expected))

    for idx := 0; idx < len(expected); idx++ {
        var actualInv *evalset.Invocation
        if idx < len(actual) {
            actualInv = &actual[idx]
        }
        expectedInv := &expected[idx]

        aText := normalizeResponse(actualInv)
        eText := normalizeResponse(expectedInv)

        score := 0.0
        status := evalresult.EvalStatusFailed
        switch {
        case eText == "" && aText == "":
            score = 1.0
            status = evalresult.EvalStatusNotEvaluated
        case aText == eText:
            score = 1.0
            status = evalresult.EvalStatusPassed
        }

        totalScore += score
        if status == evalresult.EvalStatusPassed {
            passedCount++
        }
        if status == evalresult.EvalStatusNotEvaluated {
            notEvaluatedCount++
        }

        perInvocation = append(perInvocation, evaluator.PerInvocationResult{
            ActualInvocation:   actualInv,
            ExpectedInvocation: expectedInv,
            Score:              score,
            Status:             status,
        })
    }

    result.PerInvocationResults = perInvocation
    result.OverallScore = totalScore / float64(len(expected))

    switch {
    case passedCount == len(expected):
        result.OverallStatus = evalresult.EvalStatusPassed
    case notEvaluatedCount == len(expected):
        result.OverallStatus = evalresult.EvalStatusNotEvaluated
    default:
        result.OverallStatus = evalresult.EvalStatusFailed
    }

    return result, nil
}

// Name returns the canonical metric name supported by this evaluator.
func (e *Evaluator) Name() string { return "response_match_score" }

// Description explains what the evaluator measures.
func (e *Evaluator) Description() string {
    return "Performs normalized exact match comparison between final responses"
}

// normalizeResponse extracts and normalizes the final response text from an
// invocation. It lowercases the string and collapses consecutive whitespace to
// minimise formatting differences.
func normalizeResponse(inv *evalset.Invocation) string {
    if inv == nil || inv.FinalResponse == nil {
        return ""
    }
    var builder strings.Builder
    for _, part := range inv.FinalResponse.Parts {
        if part.Text != "" {
            if builder.Len() > 0 {
                builder.WriteRune(' ')
            }
            builder.WriteString(strings.TrimSpace(part.Text))
        }
    }
    return strings.ToLower(strings.Join(strings.Fields(builder.String()), " "))
}

// Ensure interface compliance.
var _ evaluator.Evaluator = (*Evaluator)(nil)
