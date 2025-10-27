//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agui

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.Equal(t, "/", opts.path)
	assert.NotNil(t, opts.serviceFactory)
	assert.Empty(t, opts.aguiRunnerOptions)
}

func TestOptionMutators(t *testing.T) {
	var aguiOpt aguirunner.Option

	opts := newOptions(
		WithPath("/custom"),
		WithAGUIRunnerOptions(aguiOpt),
	)

	assert.Equal(t, "/custom", opts.path)
	assert.Equal(t, []aguirunner.Option{aguiOpt}, opts.aguiRunnerOptions)
}

func TestOptionAppends(t *testing.T) {
	var (
		aguiOpt1 aguirunner.Option
		aguiOpt2 aguirunner.Option
	)
	opts := newOptions()

	WithAGUIRunnerOptions(aguiOpt1)(opts)
	WithAGUIRunnerOptions(aguiOpt2)(opts)

	assert.Equal(t, []aguirunner.Option{aguiOpt1, aguiOpt2}, opts.aguiRunnerOptions)
}

type fakeService struct{}

func (fakeService) Handler() http.Handler { return http.NewServeMux() }

var _ service.Service = fakeService{}

func TestWithServiceFactory(t *testing.T) {
	var invoked bool
	customFactory := func(_ aguirunner.Runner, _ ...service.Option) service.Service {
		invoked = true
		return fakeService{}
	}

	opts := newOptions(WithServiceFactory(customFactory))

	svc := opts.serviceFactory(nil)
	assert.NotNil(t, svc)
	assert.True(t, invoked)
	if _, ok := svc.(fakeService); !ok {
		t.Fatal("expected fakeService instance")
	}
}
