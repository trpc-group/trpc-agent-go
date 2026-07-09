//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

// This file provides deterministic scripted stand-ins for the candidate
// model and the PromptIter workers so the whole pipeline runs without any
// API key or network access. All components are pure functions of their
// inputs: the same request always produces the same response.

import (
	"context"
	"errors"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// OptimizedMarker is the token the scripted optimizer appends to a text
// surface and the scripted model looks for to switch into optimized behavior.
const OptimizedMarker = "优化标记M1"

// OptimizedDirective is the full instruction sentence appended by the
// scripted optimizer. It contains OptimizedMarker.
const OptimizedDirective = "【优化标记M1】回答订单问题时，必须以\"订单查询结果：\"开头，并包含订单状态、订单金额和预计送达时间。"

// Tool names used by the candidate agent and the behavior scripts.
const (
	// ToolQueryOrder is the correct tool for order status questions.
	ToolQueryOrder = "query_order"
	// ToolQueryLogistics is the wrong tool for order status questions.
	ToolQueryLogistics = "query_logistics"
)

// Good final responses. The evalset gold answers must equal these texts so
// that pass/fail flips exactly as the scenario matrix prescribes.
const (
	GoodTrain01 = "订单查询结果：订单 ORD-1001 当前状态为已发货，订单金额 299.00 元，预计送达时间 2026-07-08。"
	GoodTrain02 = "订单查询结果：订单 ORD-1002 当前状态为待发货，订单金额 58.50 元，预计送达时间 2026-07-10。"
	GoodTrain03 = "订单查询结果：订单 ORD-1003 当前状态为已签收，订单金额 1299.00 元，预计送达时间 2026-07-03。"
	GoodTrain04 = "订单查询结果：订单 ORD-1007 当前状态为待发货，订单金额 129.00 元，预计送达时间 2026-07-14。"
	GoodVal01   = "订单查询结果：订单 ORD-1004 当前状态为运输中，订单金额 449.00 元，预计送达时间 2026-07-09。"
	GoodVal02   = "订单 ORD-1005 已取消，退款 199.00 元将在 3 个工作日内原路退回。"
	GoodVal03   = "订单查询结果：订单 ORD-1006 当前状态为已发货，订单金额 88.00 元，预计送达时间 2026-07-11。"
)

// Bad final responses produced in failing turns.
const (
	BadTrain01 = "您的订单 ORD-1001 正在处理中，请耐心等待。"
	BadTrain02 = "根据物流信息，您的包裹正在运输途中，请留意配送通知。"
	// BadTrain04 is a well-formatted answer built on the wrong order's data:
	// the model transposed the id digits and queried ORD-1070 instead of
	// ORD-1007, so the tool succeeded but returned the wrong record.
	BadTrain04 = "订单查询结果：订单 ORD-1070 当前状态为已发货，订单金额 66.00 元，预计送达时间 2026-07-12。"
	BadVal01   = "您的订单 ORD-1004 已收到，我们会尽快安排。"
	// RegressedVal02 is the optimized-mode regression on the protected case:
	// the marker directive pushes the reply into an over-formatted template
	// that no longer matches the gold answer.
	RegressedVal02 = "订单查询结果：**订单 ORD-1005**\n- 状态：已取消\n- 退款金额：199.00 元"
)

// Turn scripts one model turn: an optional tool call followed by final text.
type Turn struct {
	// ToolName triggers a tool call before the final answer when non-empty.
	ToolName string
	// ToolArgs is the JSON argument payload for the tool call.
	ToolArgs string
	// Final is the final assistant response text.
	Final string
}

// CaseBehavior scripts one eval case in baseline and optimized modes.
type CaseBehavior struct {
	// Key is the substring that identifies the case in the user content.
	Key string
	// Baseline is the turn produced when the instruction lacks the marker.
	Baseline Turn
	// Optimized is the turn produced when the instruction has the marker.
	Optimized Turn
}

// Behaviors is the scripted scenario matrix. Four train and three validation
// cases cover: optimizable success, optimization ineffective (wrong tool),
// stable pass, optimization ineffective (wrong tool argument), generalizing
// success, protected-case regression, and stable pass.
var Behaviors = []CaseBehavior{
	{
		Key:       "ORD-1001",
		Baseline:  Turn{Final: BadTrain01},
		Optimized: Turn{Final: GoodTrain01},
	},
	{
		Key: "ORD-1002",
		Baseline: Turn{
			ToolName: ToolQueryLogistics,
			ToolArgs: `{"order_id":"ORD-1002"}`,
			Final:    BadTrain02,
		},
		Optimized: Turn{
			ToolName: ToolQueryLogistics,
			ToolArgs: `{"order_id":"ORD-1002"}`,
			Final:    BadTrain02,
		},
	},
	{
		Key: "ORD-1003",
		Baseline: Turn{
			ToolName: ToolQueryOrder,
			ToolArgs: `{"order_id":"ORD-1003"}`,
			Final:    GoodTrain03,
		},
		Optimized: Turn{
			ToolName: ToolQueryOrder,
			ToolArgs: `{"order_id":"ORD-1003"}`,
			Final:    GoodTrain03,
		},
	},
	{
		// train_04: the model picks the right tool but transposes the order id
		// digits, querying existing order ORD-1070 instead of ORD-1007. The
		// tool succeeds on the wrong record, so both the trajectory metric
		// (arguments mismatch) and the final response fail; attribution must
		// yield a tool_argument_error root cause with the final response
		// mismatch folded under it. The format directive cannot fix argument
		// extraction, so the optimized turn is identical (ineffective).
		Key: "ORD-1007",
		Baseline: Turn{
			ToolName: ToolQueryOrder,
			ToolArgs: `{"order_id":"ORD-1070"}`,
			Final:    BadTrain04,
		},
		Optimized: Turn{
			ToolName: ToolQueryOrder,
			ToolArgs: `{"order_id":"ORD-1070"}`,
			Final:    BadTrain04,
		},
	},
	{
		// val_01: under the baseline instruction the model answers vaguely
		// without querying anything (missing tool call + bad text). The
		// optimized directive forces it to fetch real order data, so it calls
		// query_order and produces the gold answer. This makes the validation
		// aggregate rise strictly (+FR +TT here vs -FR on val_02), so the
		// engine's inner score gate accepts the overfit candidate and only
		// the outer per-case gate can reject it.
		Key:      "ORD-1004",
		Baseline: Turn{Final: BadVal01},
		Optimized: Turn{
			ToolName: ToolQueryOrder,
			ToolArgs: `{"order_id":"ORD-1004"}`,
			Final:    GoodVal01,
		},
	},
	{
		Key:       "ORD-1005",
		Baseline:  Turn{Final: GoodVal02},
		Optimized: Turn{Final: RegressedVal02},
	},
	{
		Key:       "ORD-1006",
		Baseline:  Turn{Final: GoodVal03},
		Optimized: Turn{Final: GoodVal03},
	},
}

// fallbackFinal is returned when no behavior matches the user content.
const fallbackFinal = "抱歉，我暂时无法处理该请求。"

// Model is a deterministic scripted model.Model implementation.
type Model struct {
	name string
}

// NewModel creates a scripted candidate model.
func NewModel(name string) *Model {
	if name == "" {
		name = "fake-order-assistant"
	}
	return &Model{name: name}
}

// Info implements model.Model.
func (m *Model) Info() model.Info {
	return model.Info{Name: m.name}
}

// GenerateContent implements model.Model. Behavior depends only on the
// request messages: the system instruction selects baseline or optimized
// mode, the last user message selects the case, and the presence of a tool
// response message switches from the tool call turn to the final answer turn.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	_ = ctx
	turn := m.scriptTurn(request)
	response := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Model:  m.name,
		Done:   true,
	}
	if turn.ToolName != "" && !hasToolResponse(request) {
		response.Choices = []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{
						Type: "function",
						ID:   "call_" + turn.ToolName,
						Function: model.FunctionDefinitionParam{
							Name:      turn.ToolName,
							Arguments: []byte(turn.ToolArgs),
						},
					},
				},
			},
		}}
		response.Usage = deterministicUsage(request, turn.ToolArgs)
	} else {
		response.Choices = []model.Choice{{
			Message: model.NewAssistantMessage(turn.Final),
		}}
		response.Usage = deterministicUsage(request, turn.Final)
	}
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func (m *Model) scriptTurn(request *model.Request) Turn {
	if request == nil {
		return Turn{Final: fallbackFinal}
	}
	optimized := strings.Contains(systemContent(request), OptimizedMarker)
	userContent := lastUserContent(request)
	for _, behavior := range Behaviors {
		if !strings.Contains(userContent, behavior.Key) {
			continue
		}
		if optimized {
			return behavior.Optimized
		}
		return behavior.Baseline
	}
	return Turn{Final: fallbackFinal}
}

func systemContent(request *model.Request) string {
	var builder strings.Builder
	for _, message := range request.Messages {
		if message.Role == model.RoleSystem {
			builder.WriteString(message.Content)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func lastUserContent(request *model.Request) string {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == model.RoleUser {
			return request.Messages[i].Content
		}
	}
	return ""
}

func hasToolResponse(request *model.Request) bool {
	for _, message := range request.Messages {
		if message.Role == model.RoleTool {
			return true
		}
	}
	return false
}

// deterministicUsage fabricates stable token counts so cost accounting works
// identically in fake and real modes.
func deterministicUsage(request *model.Request, output string) *model.Usage {
	promptChars := 0
	for _, message := range request.Messages {
		promptChars += len(message.Content)
	}
	promptTokens := promptChars/4 + 1
	completionTokens := len(output)/4 + 1
	return &model.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
}

// ImprovedToolDescriptions maps tool IDs to the replacement descriptions the
// scripted optimizer proposes for tool surfaces.
var ImprovedToolDescriptions = map[string]string{
	ToolQueryOrder: "查询订单的当前状态、订单金额和预计送达时间。" +
		"用户询问订单状态、订单详情或订单进度时必须优先调用本工具。参数 order_id 必填。",
}

// Backwarder is a deterministic backwarder.Backwarder: it converts the
// incoming loss packets of one failed step into one gradient per allowed
// surface, without any model call.
type Backwarder struct{}

// NewBackwarder creates the scripted backwarder.
func NewBackwarder() *Backwarder {
	return &Backwarder{}
}

// Backward implements backwarder.Backwarder.
func (b *Backwarder) Backward(
	ctx context.Context,
	request *backwarder.Request,
) (*backwarder.Result, error) {
	_ = ctx
	if request == nil {
		return nil, errors.New("backward request is nil")
	}
	if len(request.AllowedGradientSurfaceIDs) == 0 {
		return &backwarder.Result{}, nil
	}
	gradientText, severity := summarizeIncoming(request.Incoming)
	result := &backwarder.Result{
		Gradients: make([]promptiter.SurfaceGradient, 0, len(request.AllowedGradientSurfaceIDs)),
	}
	for _, surfaceID := range request.AllowedGradientSurfaceIDs {
		result.Gradients = append(result.Gradients, promptiter.SurfaceGradient{
			EvalSetID:  request.EvalSetID,
			EvalCaseID: request.EvalCaseID,
			StepID:     request.StepID,
			SurfaceID:  surfaceID,
			Severity:   severity,
			Gradient:   gradientText + "（case " + request.EvalCaseID + "）",
		})
	}
	return result, nil
}

func summarizeIncoming(incoming []backwarder.GradientPacket) (string, promptiter.LossSeverity) {
	severity := promptiter.LossSeverityP1
	parts := make([]string, 0, len(incoming))
	for _, packet := range incoming {
		if strings.TrimSpace(packet.Gradient) != "" {
			parts = append(parts, strings.TrimSpace(packet.Gradient))
		}
		if packet.Severity == promptiter.LossSeverityP0 {
			severity = promptiter.LossSeverityP0
		}
	}
	if len(parts) == 0 {
		return "回复未满足评测要求，需要在指令中明确输出格式与必含信息", severity
	}
	return strings.Join(parts, "；"), severity
}

// Aggregator is a deterministic aggregator.Aggregator that merges sample
// gradients by simple pass-through, preserving order.
type Aggregator struct{}

// NewAggregator creates the scripted aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{}
}

// Aggregate implements aggregator.Aggregator.
func (a *Aggregator) Aggregate(
	ctx context.Context,
	request *aggregator.Request,
) (*aggregator.Result, error) {
	_ = ctx
	if request == nil {
		return nil, errors.New("aggregate request is nil")
	}
	return &aggregator.Result{
		Gradient: &promptiter.AggregatedSurfaceGradient{
			SurfaceID: request.SurfaceID,
			NodeID:    request.NodeID,
			Type:      request.Type,
			Gradients: request.Gradients,
		},
	}, nil
}

// Optimizer is a deterministic optimizer.Optimizer. For text surfaces it
// appends OptimizedDirective exactly once; for tool surfaces it replaces the
// description with the entry from ImprovedToolDescriptions. Both operations
// are idempotent so repeated rounds converge instead of oscillating.
type Optimizer struct{}

// NewOptimizer creates the scripted optimizer.
func NewOptimizer() *Optimizer {
	return &Optimizer{}
}

// Optimize implements optimizer.Optimizer.
func (o *Optimizer) Optimize(
	ctx context.Context,
	request *optimizer.Request,
) (*optimizer.Result, error) {
	_ = ctx
	if request == nil || request.Surface == nil {
		return nil, errors.New("optimize request surface is nil")
	}
	surface := request.Surface
	if surface.Type == astructure.SurfaceTypeTool {
		return optimizeToolSurface(surface)
	}
	return optimizeTextSurface(surface)
}

func optimizeTextSurface(surface *astructure.Surface) (*optimizer.Result, error) {
	current := ""
	if surface.Value.Text != nil {
		current = *surface.Value.Text
	}
	patched := current
	reason := "指令已包含优化标记，保持不变"
	if !strings.Contains(current, OptimizedMarker) {
		patched = strings.TrimRight(current, "\n") + "\n\n" + OptimizedDirective
		reason = "追加输出格式与必含信息约束，修复回复缺失订单状态、金额和送达时间的问题"
	}
	return &optimizer.Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: surface.SurfaceID,
			Value: astructure.SurfaceValue{
				Text: &patched,
			},
			Reason: reason,
		},
	}, nil
}

func optimizeToolSurface(surface *astructure.Surface) (*optimizer.Result, error) {
	if len(surface.Value.Tools) == 0 {
		return nil, errors.New("tool surface has no tool refs")
	}
	current := surface.Value.Tools[0]
	improved, ok := ImprovedToolDescriptions[current.ID]
	reason := "改写工具描述，强调用途与必填参数"
	if !ok || current.Description == improved {
		improved = current.Description
		reason = "工具描述无需调整，保持不变"
	}
	patchedTool := current
	patchedTool.Description = improved
	return &optimizer.Result{
		Patch: &promptiter.SurfacePatch{
			SurfaceID: surface.SurfaceID,
			Value: astructure.SurfaceValue{
				Tools: []astructure.ToolRef{patchedTool},
			},
			Reason: reason,
		},
	}, nil
}
