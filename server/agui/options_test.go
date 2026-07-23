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
	"context"
	"net/http"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/service"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.Equal(t, "/", opts.path)
	assert.Equal(t, "/cancel", opts.cancelPath)
	assert.False(t, opts.cancelEnabled)
	assert.NotNil(t, opts.serviceFactory)
	assert.NotNil(t, opts.runnerFactory)
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
	assert.IsType(t, fakeService{}, svc)
}

func TestWithRunnerFactory(t *testing.T) {
	var invoked bool
	customFactory := func(_ runner.Runner, _ ...aguirunner.Option) (aguirunner.Runner, error) {
		invoked = true
		return optionTestRunner{}, nil
	}
	opts := newOptions(WithRunnerFactory(customFactory))
	r, err := opts.runnerFactory(nil)
	assert.NoError(t, err)
	assert.True(t, invoked)
	assert.IsType(t, optionTestRunner{}, r)
}

func TestWithTimeout(t *testing.T) {
	opts := newOptions(WithTimeout(2 * time.Second))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.Equal(t, 2*time.Second, ro.Timeout)
}

func TestWithFlushInterval(t *testing.T) {
	opts := newOptions(WithFlushInterval(2 * time.Second))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.Equal(t, 2*time.Second, ro.FlushInterval)
}

func TestWithRunHook(t *testing.T) {
	called := false
	hook := func(context.Context, *aguirunner.Run) error {
		called = true
		return nil
	}
	opts := newOptions(WithRunHook(hook))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	require.Len(t, ro.RunHooks, 1)
	assert.NoError(t, ro.RunHooks[0](context.Background(), nil))
	assert.True(t, called)
}

func TestWithPostRunFinalizationTimeout(t *testing.T) {
	opts := newOptions(WithPostRunFinalizationTimeout(2 * time.Second))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.Equal(t, 2*time.Second, ro.PostRunFinalizationTimeout)
}

func TestWithTrackPersistenceTimeout(t *testing.T) {
	opts := newOptions(WithTrackPersistenceTimeout(2 * time.Second))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.Equal(t, 2*time.Second, ro.TrackPersistenceTimeout)
}

func TestWithHeartbeatInterval(t *testing.T) {
	opts := newOptions(WithHeartbeatInterval(2 * time.Second))
	assert.Equal(t, 2*time.Second, opts.heartbeatInterval)
}

func TestWithGraphNodeLifecycleActivityEnabled(t *testing.T) {
	opts := newOptions(WithGraphNodeLifecycleActivityEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.GraphNodeLifecycleActivityEnabled)
}

func TestWithGraphNodeInterruptActivityEnabled(t *testing.T) {
	opts := newOptions(WithGraphNodeInterruptActivityEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.GraphNodeInterruptActivityEnabled)
}

func TestWithGraphNodeInterruptActivityTopLevelOnly(t *testing.T) {
	opts := newOptions(WithGraphNodeInterruptActivityTopLevelOnly(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.GraphNodeInterruptActivityTopLevelOnly)
}

func TestWithReasoningContentEnabled(t *testing.T) {
	opts := newOptions(WithReasoningContentEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.ReasoningContentEnabled)
}

func TestWithEventSourceMetadataEnabled(t *testing.T) {
	opts := newOptions(WithEventSourceMetadataEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.EventSourceMetadataEnabled)
}

func TestWithToolResultInputTranslationEnabled(t *testing.T) {
	opts := newOptions(WithToolResultInputTranslationEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.ToolResultInputTranslationEnabled)
}

func TestWithToolCallDeltaStreamingEnabled(t *testing.T) {
	opts := newOptions(WithToolCallDeltaStreamingEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.ToolCallDeltaStreamingEnabled)
}

func TestWithStreamingToolResultActivityEnabled(t *testing.T) {
	opts := newOptions(WithStreamingToolResultActivityEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.StreamingToolResultActivityEnabled)
}

func TestWithConcurrentMessageStreamsEnabled(t *testing.T) {
	opts := newOptions(WithConcurrentMessageStreamsEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.ConcurrentMessageStreamsEnabled)
}

func TestWithMessagesSnapshotFollowEnabled(t *testing.T) {
	opts := newOptions(WithMessagesSnapshotFollowEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.MessagesSnapshotFollowEnabled)
}

func TestWithMessagesSnapshotFollowMaxDuration(t *testing.T) {
	opts := newOptions(WithMessagesSnapshotFollowMaxDuration(2 * time.Second))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.Equal(t, 2*time.Second, ro.MessagesSnapshotFollowMaxDuration)
}

func TestWithMessagesSnapshotRunLifecycleEventsEnabled(t *testing.T) {
	opts := newOptions(WithMessagesSnapshotRunLifecycleEventsEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.MessagesSnapshotRunLifecycleEventsEnabled)
}

func TestWithCancelEnabled(t *testing.T) {
	opts := newOptions(WithCancelEnabled(true))
	assert.True(t, opts.cancelEnabled)
}

func TestWithCancelOnContextDoneEnabled(t *testing.T) {
	opts := newOptions(WithCancelOnContextDoneEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.CancelOnContextDoneEnabled)
}

func TestWithDistributedCancelEnabled(t *testing.T) {
	opts := newOptions(WithDistributedCancelEnabled(true))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.True(t, ro.DistributedCancelEnabled)
}

func TestWithDistributedCancelPollInterval(t *testing.T) {
	opts := newOptions(WithDistributedCancelPollInterval(2 * time.Second))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.Equal(t, 2*time.Second, ro.DistributedCancelPollInterval)
}

func TestWithAppNameResolver(t *testing.T) {
	called := false
	resolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
		called = true
		return "custom-app", nil
	}
	opts := newOptions(WithAppNameResolver(resolver))
	ro := aguirunner.NewOptions(opts.aguiRunnerOptions...)
	assert.NotNil(t, ro.AppNameResolver)
	appName, err := ro.AppNameResolver(context.Background(), nil)
	assert.NoError(t, err)
	assert.Equal(t, "custom-app", appName)
	assert.True(t, called)
}

type optionTestRunner struct{}

func (optionTestRunner) Run(context.Context, *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	events := make(chan aguievents.Event)
	close(events)
	return events, nil
}
