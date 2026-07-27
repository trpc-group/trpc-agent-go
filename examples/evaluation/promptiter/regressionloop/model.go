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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type countedModel struct {
	role            string
	stage           string
	base            model.Model
	ledger          *ledger
	pricing         pricing
	latencyOverride *time.Duration
}

func newCountedModel(role, stage string, base model.Model, ledger *ledger, pricing pricing) *countedModel {
	return &countedModel{
		role:    role,
		stage:   stage,
		base:    base,
		ledger:  ledger,
		pricing: pricing,
	}
}

func (m *countedModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ticket := m.ledger.beginCall(m.stage, m.role, m.pricing, m.latencyOverride)
	responses, err := m.base.GenerateContent(ctx, request)
	if err != nil {
		ticket.finish(0, 0, false, err.Error())
		return nil, err
	}
	output := make(chan *model.Response)
	go func() {
		defer close(output)
		var finalUsage *model.Usage
		var responseErr string
		for response := range responses {
			if response != nil {
				if response.Done && response.Usage != nil {
					finalUsage = response.Usage
				}
				if response.Error != nil {
					responseErr = response.Error.Error()
				}
			}
			output <- response
		}
		if finalUsage == nil {
			ticket.finish(0, 0, false, responseErr)
			return
		}
		ticket.finish(finalUsage.PromptTokens, finalUsage.CompletionTokens, true, responseErr)
	}()
	return output, nil
}

func (m *countedModel) Info() model.Info {
	return m.base.Info()
}
