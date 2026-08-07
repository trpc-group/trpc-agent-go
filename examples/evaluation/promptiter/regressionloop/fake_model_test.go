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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestScriptedModelIsDeterministicAndReportsUsage(t *testing.T) {
	modelInstance := newScriptedModel("candidate")
	request := &model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "case-a"}}}
	first := readOneResponse(t, modelInstance, request)
	second := readOneResponse(t, modelInstance, request)
	assert.Equal(t, first.Choices[0].Message.Content, second.Choices[0].Message.Content)
	assert.NotNil(t, first.Usage)
}

func readOneResponse(t *testing.T, instance model.Model, request *model.Request) *model.Response {
	t.Helper()
	responses, err := instance.GenerateContent(context.Background(), request)
	require.NoError(t, err)
	response := <-responses
	require.NotNil(t, response)
	return response
}
