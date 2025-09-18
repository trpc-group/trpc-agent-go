package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Service is a local implementation of EvaluationService.
// It orchestrates inference using the provided runner and applies registered
// evaluators against expected eval cases.
type Service struct {
	evalSetManager    evalset.Manager
	registry          *evaluator.Registry
	now               func() time.Time
	sessionIDSupplier func() string

	mu sync.RWMutex
}

// New returns a new local evaluation service. The service ships with sensible
// defaults: an in-memory eval set manager, an in-memory result manager and a
// no-op evaluator registry. Callers can override these via options.
func New(opts ...Option) *Service {
	svc := &Service{
		evalSetManager:    evalsetinmemory.NewManager(),
		registry:          evaluator.NewRegistry(),
		now:               time.Now,
		sessionIDSupplier: defaultSessionIDSupplier,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

// SetRegistry overrides the evaluator registry at runtime. Primarily used by
// AgentEvaluator to ensure the service shares the same registry instance.
func (s *Service) SetRegistry(r *evaluator.Registry) {
	if r == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry = r
}

// PerformInference executes agent inference for the requested eval cases. The
// method streams results through the returned channel until completion or
// context cancellation.
func (s *Service) PerformInference(ctx context.Context, req *service.InferenceRequest) (<-chan *service.InferenceResult, error) {
	if req == nil {
		return nil, errors.New("inference request is nil")
	}
	if req.Runner == nil {
		return nil, errors.New("runner is required for inference")
	}
	if s.evalSetManager == nil {
		return nil, errors.New("eval set manager not configured")
	}
	ch := make(chan *service.InferenceResult)

	evalCases, err := s.resolveEvalCases(ctx, req)
	if err != nil {
		close(ch)
		return nil, err
	}

	go func() {
		defer close(ch)
		for _, ec := range evalCases {
			select {
			case <-ctx.Done():
				return
			default:
			}

			result := &service.InferenceResult{
				AppName:    req.AppName,
				EvalSetID:  req.EvalSetID,
				EvalCaseID: ec.EvalID,
				Status:     service.InferenceStatusUnknown,
			}

			actualInvocations, sessionID, runErr := s.runEvalCase(ctx, req, ec)
			if runErr != nil {
				result.Status = service.InferenceStatusFailure
				result.ErrorMessage = runErr.Error()
			} else {
				result.Status = service.InferenceStatusSuccess
				result.Inferences = actualInvocations
				result.SessionID = sessionID
			}

			select {
			case <-ctx.Done():
				return
			case ch <- result:
			}
		}
	}()

	return ch, nil
}

// Evaluate applies the configured metrics to inference results and streams
// per-case evaluation results.
func (s *Service) Evaluate(ctx context.Context, req *service.EvaluateRequest) (<-chan *evalresult.EvalCaseResult, error) {
	if req == nil {
		return nil, errors.New("evaluate request is nil")
	}
	if s.registry == nil {
		return nil, errors.New("evaluator registry not configured")
	}

	ch := make(chan *evalresult.EvalCaseResult)

	go func() {
		defer close(ch)
		for i := range req.InferenceResults {
			select {
			case <-ctx.Done():
				return
			default:
			}

			inference := req.InferenceResults[i]
			caseResult := s.evaluateSingle(ctx, inference, req.EvaluateConfig)

			select {
			case <-ctx.Done():
				return
			case ch <- caseResult:
			}
		}
	}()

	return ch, nil
}

func (s *Service) resolveEvalCases(ctx context.Context, req *service.InferenceRequest) ([]*evalset.EvalCase, error) {
	evalSet, err := s.evalSetManager.Get(ctx, req.EvalSetID)
	if err != nil {
		return nil, err
	}

	if len(req.EvalCaseIDs) == 0 {
		cases := make([]*evalset.EvalCase, 0, len(evalSet.EvalCases))
		for i := range evalSet.EvalCases {
			cases = append(cases, &evalSet.EvalCases[i])
		}
		return cases, nil
	}

	cases := make([]*evalset.EvalCase, 0, len(req.EvalCaseIDs))
	for _, id := range req.EvalCaseIDs {
		ec, err := s.evalSetManager.GetCase(ctx, req.EvalSetID, id)
		if err != nil {
			return nil, err
		}
		cases = append(cases, ec)
	}
	return cases, nil
}

func (s *Service) runEvalCase(ctx context.Context, req *service.InferenceRequest, ec *evalset.EvalCase) ([]evalset.Invocation, string, error) {
	userID := "eval-user"
	if ec.SessionInput != nil && ec.SessionInput.UserID != "" {
		userID = ec.SessionInput.UserID
	}
	sessionID := s.sessionIDSupplier()

	actualInvocations := make([]evalset.Invocation, 0, len(ec.Conversation))
	for idx := range ec.Conversation {
		select {
		case <-ctx.Done():
			return nil, sessionID, ctx.Err()
		default:
		}

		expectedInvocation := ec.Conversation[idx]
		if expectedInvocation.UserContent == nil {
			continue
		}

		msg := contentToMessage(expectedInvocation.UserContent)
		// Ensure the message is treated as user input for runner execution.
		msg.Role = model.RoleUser

		events, err := req.Runner.Run(ctx, userID, sessionID, msg)
		if err != nil {
			return nil, sessionID, err
		}

		finalResponse, intermediate, runErr := s.consumeEvents(ctx, events)
		if runErr != nil {
			return nil, sessionID, runErr
		}

		actual := evalset.Invocation{
			InvocationID:      expectedInvocation.InvocationID,
			UserContent:       cloneContent(expectedInvocation.UserContent),
			FinalResponse:     newContent(string(model.RoleAssistant), finalResponse),
			IntermediateData:  intermediate,
			CreationTimestamp: evalset.EpochTime{Time: s.now()},
		}
		actualInvocations = append(actualInvocations, actual)
	}

	return actualInvocations, sessionID, nil
}

func (s *Service) consumeEvents(ctx context.Context, events <-chan *event.Event) (string, *evalset.IntermediateData, error) {
	var (
		builder       strings.Builder
		finalResponse string
		firstErr      error
		intermediate  = &evalset.IntermediateData{}
	)

	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case ev, ok := <-events:
			if !ok {
				if finalResponse == "" {
					finalResponse = builder.String()
				}
				if finalResponse == "" && firstErr != nil {
					return "", nil, firstErr
				}
				if finalResponse == "" {
					return "", nil, errors.New("no response generated")
				}
				if len(intermediate.ToolUses) == 0 && len(intermediate.ToolResponses) == 0 && len(intermediate.IntermediateResponses) == 0 {
					intermediate = nil
				}
				return finalResponse, intermediate, nil
			}
			if ev == nil {
				continue
			}
			if ev.Response != nil {
				if ev.Response.Error != nil && firstErr == nil {
					firstErr = errors.New(ev.Response.Error.Message)
				}
				for _, choice := range ev.Response.Choices {
					if choice.Delta.Content != "" {
						builder.WriteString(choice.Delta.Content)
					}
					if choice.Message.Content != "" {
						finalResponse = choice.Message.Content
					}
					// Collect tool call information when present.
					if len(choice.Message.ToolCalls) > 0 {
						if intermediate.ToolUses == nil {
							intermediate.ToolUses = make([]evalset.FunctionCall, 0)
						}
						for _, tc := range choice.Message.ToolCalls {
							var args map[string]interface{}
							if len(tc.Function.Arguments) > 0 {
								_ = json.Unmarshal(tc.Function.Arguments, &args)
							}
							intermediate.ToolUses = append(intermediate.ToolUses, evalset.FunctionCall{
								ID:   tc.ID,
								Name: tc.Function.Name,
								Args: args,
							})
						}
					}
				}
			}
			if ev.Object == model.ObjectTypeToolResponse && ev.Response != nil {
				if intermediate.ToolResponses == nil {
					intermediate.ToolResponses = make([]evalset.ToolResponse, 0)
				}
				intermediate.ToolResponses = append(intermediate.ToolResponses, evalset.ToolResponse{
					Name: ev.Author,
				})
			}
		}
	}
}

func (s *Service) evaluateSingle(ctx context.Context, inference service.InferenceResult, cfg service.EvaluateConfig) *evalresult.EvalCaseResult {
	caseResult := &evalresult.EvalCaseResult{
		EvalSetID:       inference.EvalSetID,
		EvalCaseID:      inference.EvalCaseID,
		SessionID:       inference.SessionID,
		FinalEvalStatus: evalresult.EvalStatusNotEvaluated,
	}

	if inference.Status != service.InferenceStatusSuccess {
		caseResult.FinalEvalStatus = evalresult.EvalStatusFailed
		caseResult.OverallEvalMetricResults = []evalresult.EvalMetricResult{}
		caseResult.EvalMetricResultPerInvocation = []evalresult.EvalMetricResultPerInvocation{}
		if inference.ErrorMessage != "" {
			caseResult.OverallEvalMetricResults = append(caseResult.OverallEvalMetricResults, evalresult.EvalMetricResult{
				MetricName: "inference",
				Status:     evalresult.EvalStatusFailed,
				Threshold:  0,
				Details:    map[string]interface{}{"error": inference.ErrorMessage},
			})
		}
		return caseResult
	}

	// Load expected eval case
	expectedCase, err := s.evalSetManager.GetCase(ctx, inference.EvalSetID, inference.EvalCaseID)
	if err != nil {
		caseResult.FinalEvalStatus = evalresult.EvalStatusFailed
		caseResult.OverallEvalMetricResults = []evalresult.EvalMetricResult{{
			MetricName: "eval_case_lookup",
			Status:     evalresult.EvalStatusFailed,
			Threshold:  0,
			Details:    map[string]interface{}{"error": err.Error()},
		}}
		return caseResult
	}

	metrics := cfg.Metrics
	if len(metrics) == 0 {
		// Default to response match score if nothing configured.
		metrics = []metric.EvalMetric{{
			MetricName: metric.MetricResponseMatchScore,
			Threshold:  1.0,
		}}
	}

	overallMetricResults := make([]evalresult.EvalMetricResult, 0, len(metrics))
	perInvocation := make(map[int]*evalresult.EvalMetricResultPerInvocation)

	finalStatus := evalresult.EvalStatusPassed
	for _, metricConfig := range metrics {
		evalr, err := s.registry.Get(metricConfig.MetricName)
		if err != nil {
			overallMetricResults = append(overallMetricResults, evalresult.EvalMetricResult{
				MetricName: metricConfig.MetricName,
				Status:     evalresult.EvalStatusFailed,
				Threshold:  metricConfig.Threshold,
				Details:    map[string]interface{}{"error": err.Error()},
			})
			finalStatus = evalresult.EvalStatusFailed
			continue
		}

		evalRes, evalErr := evalr.Evaluate(ctx, inference.Inferences, expectedCase.Conversation)
		if evalErr != nil {
			overallMetricResults = append(overallMetricResults, evalresult.EvalMetricResult{
				MetricName: metricConfig.MetricName,
				Status:     evalresult.EvalStatusFailed,
				Threshold:  metricConfig.Threshold,
				Details:    map[string]interface{}{"error": evalErr.Error()},
			})
			finalStatus = evalresult.EvalStatusFailed
			continue
		}

		var scorePtr *float64
		if evalRes != nil {
			if evalRes.OverallStatus != evalresult.EvalStatusNotEvaluated || evalRes.OverallScore != 0 {
				score := evalRes.OverallScore
				scorePtr = &score
			}
		}

		metricStatus := evalresult.EvalStatusNotEvaluated
		if scorePtr != nil {
			if *scorePtr >= metricConfig.Threshold {
				metricStatus = evalresult.EvalStatusPassed
			} else {
				metricStatus = evalresult.EvalStatusFailed
			}
		} else if evalRes != nil {
			metricStatus = evalRes.OverallStatus
		}

		if metricStatus == evalresult.EvalStatusFailed {
			finalStatus = evalresult.EvalStatusFailed
		} else if metricStatus == evalresult.EvalStatusNotEvaluated && finalStatus == evalresult.EvalStatusPassed {
			finalStatus = evalresult.EvalStatusNotEvaluated
		}

		overallMetricResults = append(overallMetricResults, evalresult.EvalMetricResult{
			MetricName: metricConfig.MetricName,
			Score:      scorePtr,
			Status:     metricStatus,
			Threshold:  metricConfig.Threshold,
		})

		if evalRes != nil {
			for idx, pir := range evalRes.PerInvocationResults {
				entry := perInvocation[idx]
				if entry == nil {
					entry = &evalresult.EvalMetricResultPerInvocation{
						InvocationIndex: idx,
						MetricResults:   []evalresult.EvalMetricResult{},
					}
					perInvocation[idx] = entry
				}

				score := pir.Score
				entry.MetricResults = append(entry.MetricResults, evalresult.EvalMetricResult{
					MetricName: metricConfig.MetricName,
					Score:      &score,
					Status:     pir.Status,
					Threshold:  metricConfig.Threshold,
				})
			}
		}
	}

	// Convert map to slice preserving order
	invocationResults := make([]evalresult.EvalMetricResultPerInvocation, 0, len(perInvocation))
	keys := make([]int, 0, len(perInvocation))
	for idx := range perInvocation {
		keys = append(keys, idx)
	}
	sort.Ints(keys)
	for _, idx := range keys {
		if res, ok := perInvocation[idx]; ok {
			invocationResults = append(invocationResults, *res)
		}
	}

	caseResult.OverallEvalMetricResults = overallMetricResults
	caseResult.EvalMetricResultPerInvocation = invocationResults
	caseResult.FinalEvalStatus = finalStatus
	return caseResult
}

func contentToMessage(content *evalset.Content) model.Message {
	if content == nil {
		return model.Message{Role: model.RoleUser}
	}
	msg := model.Message{}
	role := model.Role(content.Role)
	if !role.IsValid() {
		role = model.RoleUser
	}
	msg.Role = role
	msg.Content = flattenParts(content.Parts)
	return msg
}

func newContent(role string, text string) *evalset.Content {
	if text == "" {
		return nil
	}
	return &evalset.Content{
		Role:  role,
		Parts: []evalset.Part{{Text: text}},
	}
}

func flattenParts(parts []evalset.Part) string {
	if len(parts) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func cloneContent(c *evalset.Content) *evalset.Content {
	if c == nil {
		return nil
	}
	clone := *c
	if c.Parts != nil {
		clone.Parts = make([]evalset.Part, len(c.Parts))
		copy(clone.Parts, c.Parts)
	}
	return &clone
}

func defaultSessionIDSupplier() string {
	return fmt.Sprintf("eval-session-%d", time.Now().UnixNano())
}

// Ensure interface compliance
var _ service.EvaluationService = (*Service)(nil)
