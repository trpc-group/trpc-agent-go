//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// candidateAgentName is the node ID that owns the optimized surfaces.
const candidateAgentName = "candidate"

// Baseline tool descriptions. query_order stays intentionally vague so the
// tool-description surface has real optimization headroom.
const (
	baselineQueryOrderDescription     = "查询订单信息。"
	baselineQueryLogisticsDescription = "查询物流轨迹信息。"
)

// orderRecord is the canned backend state served by the query_order tool.
type orderRecord struct {
	Status string `json:"status"`
	Amount string `json:"amount"`
	ETA    string `json:"eta"`
}

// orderData is deterministic and consistent with the gold answers in the
// eval sets.
var orderData = map[string]orderRecord{
	"ORD-1001": {Status: "已发货", Amount: "299.00", ETA: "2026-07-08"},
	"ORD-1002": {Status: "待发货", Amount: "58.50", ETA: "2026-07-10"},
	"ORD-1003": {Status: "已签收", Amount: "1299.00", ETA: "2026-07-03"},
	"ORD-1004": {Status: "运输中", Amount: "449.00", ETA: "2026-07-09"},
	"ORD-1005": {Status: "已取消", Amount: "199.00", ETA: ""},
	"ORD-1006": {Status: "已发货", Amount: "88.00", ETA: "2026-07-11"},
	"ORD-1007": {Status: "待发货", Amount: "129.00", ETA: "2026-07-14"},
	// ORD-1070 exists so the wrong-argument case (train_04 queries it instead
	// of ORD-1007) still gets a successful tool response built on wrong data.
	"ORD-1070": {Status: "已发货", Amount: "66.00", ETA: "2026-07-12"},
}

// NewAgent builds the candidate order assistant. The instruction is
// the surface under optimization; the model decides fake or real sourcing.
func NewAgent(candidateModel model.Model, instruction string) agent.Agent {
	queryOrderTool := function.NewFunctionTool(
		queryOrder,
		function.WithName(ToolQueryOrder),
		function.WithDescription(baselineQueryOrderDescription),
	)
	queryLogisticsTool := function.NewFunctionTool(
		queryLogistics,
		function.WithName(ToolQueryLogistics),
		function.WithDescription(baselineQueryLogisticsDescription),
	)
	maxTokens := 1024
	temperature := 0.0
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(candidateModel),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("电商订单客服助手，回答订单状态、金额和送达时间问题。"),
		llmagent.WithTools([]tool.Tool{queryOrderTool, queryLogisticsTool}),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temperature,
			Stream:      false,
		}),
	)
}

type orderQueryArgs struct {
	OrderID string `json:"order_id"`
}

type orderQueryResult struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
	Amount  string `json:"amount"`
	ETA     string `json:"eta,omitempty"`
}

func queryOrder(_ context.Context, args orderQueryArgs) (orderQueryResult, error) {
	record, ok := orderData[args.OrderID]
	if !ok {
		return orderQueryResult{}, fmt.Errorf("order %q not found", args.OrderID)
	}
	return orderQueryResult{
		OrderID: args.OrderID,
		Status:  record.Status,
		Amount:  record.Amount,
		ETA:     record.ETA,
	}, nil
}

type logisticsQueryResult struct {
	OrderID        string `json:"order_id"`
	LastCheckpoint string `json:"last_checkpoint"`
	Note           string `json:"note"`
}

func queryLogistics(_ context.Context, args orderQueryArgs) (logisticsQueryResult, error) {
	return logisticsQueryResult{
		OrderID:        args.OrderID,
		LastCheckpoint: "转运中心",
		Note:           "物流信息仅覆盖运输节点，不含订单状态、金额与预计送达时间",
	}, nil
}
