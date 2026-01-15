//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides agent implementations for human-in-the-loop scenarios.
package main

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func reimburse(_ context.Context, _ reimburseInput) (reimburseOutput, error) {
	return reimburseOutput{
		Status: "ok",
	}, nil
}

func askForApproval(_ context.Context, i askForApprovalInput) (askForApprovalOutput, error) {
	return askForApprovalOutput{
		Status:   "pending",
		Amount:   i.Amount,
		TicketID: "reimbursement-ticket-001",
	}, nil
}

type reimburseInput struct {
	Purpose string `json:"purpose"`
	Amount  int    `json:"amount"`
}

type reimburseOutput struct {
	Status string `json:"status"`
}

type askForApprovalInput struct {
	Purpose string `json:"purpose"`
	Amount  int    `json:"amount"`
}

type askForApprovalOutput struct {
	Status   string `json:"status"`
	Amount   int    `json:"amount"`
	TicketID string `json:"ticket_id"`
}

func newLLMAgent(modelName string, streaming bool) *llmagent.LLMAgent {
	return llmagent.New(
		"reimbursement_agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithDescription("A helpful AI agent for reimbursement"),
		llmagent.WithInstruction(`
You are an agent whose job is to handle the reimbursement process for the employees. 
If the amount is less than $100, you will automatically approve the reimbursement.
If the amount is greater than $100, you will ask for approval from the manager. 
If the manager approves, you will call reimburse() to reimburse the amount to the employee.
If the manager rejects, you will inform the employee of the rejection.
`),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxCompletionTokens: intPtr(2000),
			Temperature:         floatPtr(0.7),
			Stream:              streaming, // Enable or disable streaming.
		}),
		llmagent.WithTools([]tool.Tool{
			function.NewFunctionTool(
				reimburse,
				function.WithName("reimburse"),
				function.WithDescription("Reimburse the amount of money to the employee."),
			),
			function.NewFunctionTool(
				askForApproval,
				function.WithLongRunning(true),
				function.WithName("ask_for_approval"),
				function.WithDescription("Ask for approval for the reimbursement."),
			)}),
	)
}
