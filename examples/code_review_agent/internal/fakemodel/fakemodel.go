//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package fakemodel provides deterministic model behavior for offline review
// acceptance tests.
package fakemodel

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// fakeModel generates deterministic responses, used for running in fake model mode
// for the code review agent example.
type fakeModel struct {
}

func newFakeModel() *fakeModel {
	return &fakeModel{}
}

func (f *fakeModel) GenerateContent(ctx context.Context, request *model.Request) (responses <-chan *model.Response, err error) {
	panic("not implemented")
}

func (f *fakeModel) Info() model.Info {
	panic("not implemented")
}
