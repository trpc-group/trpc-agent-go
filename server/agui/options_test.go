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
	"trpc.group/trpc-go/trpc-agent-go/runner"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.Equal(t, "", opts.path)
	assert.Nil(t, opts.service)
	assert.Empty(t, opts.runnerOptions)
	assert.Empty(t, opts.aguiRunnerOptions)
}

func TestOptionMutators(t *testing.T) {
	handler := http.NewServeMux()
	svc := &stubService{handler: handler}
	var runnerOpt runner.Option
	var aguiOpt aguirunner.Option

	opts := newOptions(
		WithPath("/custom"),
		WithService(svc),
		WithRunnerOptions(runnerOpt),
		WithAGUIRunnerOptions(aguiOpt),
	)

	assert.Equal(t, "/custom", opts.path)
	assert.Same(t, svc, opts.service)
	assert.Equal(t, []runner.Option{runnerOpt}, opts.runnerOptions)
	assert.Equal(t, []aguirunner.Option{aguiOpt}, opts.aguiRunnerOptions)
}

func TestOptionAppends(t *testing.T) {
	var (
		runOpt1  runner.Option
		runOpt2  runner.Option
		aguiOpt1 aguirunner.Option
		aguiOpt2 aguirunner.Option
	)
	opts := newOptions()

	WithRunnerOptions(runOpt1)(opts)
	WithRunnerOptions(runOpt2)(opts)
	WithAGUIRunnerOptions(aguiOpt1)(opts)
	WithAGUIRunnerOptions(aguiOpt2)(opts)

	assert.Equal(t, []runner.Option{runOpt1, runOpt2}, opts.runnerOptions)
	assert.Equal(t, []aguirunner.Option{aguiOpt1, aguiOpt2}, opts.aguiRunnerOptions)
}
