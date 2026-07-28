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
	"errors"
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
	ticket, err := m.ledger.beginCall(m.stage, m.role, m.pricing, m.latencyOverride)
	if err != nil {
		return nil, err
	}
	responses, err := m.base.GenerateContent(ctx, request)
	if err != nil {
		ticket.finish(0, 0, false, err.Error())
		return nil, err
	}
	output := make(chan *model.Response)
	go func() {
		defer close(output)
		var latestUsage *model.Usage
		var responseErr error
		finish := func() {
			if latestUsage == nil {
				ticket.finish(0, 0, false, modelErrorString(responseErr))
				return
			}
			ticket.finish(
				latestUsage.PromptTokens,
				latestUsage.CompletionTokens,
				true,
				modelErrorString(responseErr),
			)
		}
		defer finish()
		for {
			var response *model.Response
			var ok bool
			select {
			case <-ctx.Done():
				responseErr = errors.Join(responseErr, ctx.Err())
				return
			case response, ok = <-responses:
				if !ok {
					return
				}
			}
			if response != nil {
				if response.Usage != nil {
					latestUsage = response.Usage
				}
				if response.Error != nil {
					responseErr = errors.Join(responseErr, response.Error)
				}
			}
			select {
			case output <- response:
			case <-ctx.Done():
				responseErr = errors.Join(responseErr, ctx.Err())
				return
			}
		}
	}()
	return output, nil
}

func modelErrorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *countedModel) Info() model.Info {
	return m.base.Info()
}
