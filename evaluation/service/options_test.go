package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()

	assert.NotNil(t, opts.EvalSetManager)
	assert.NotNil(t, opts.EvaluatorRegistry)
	assert.NotNil(t, opts.SessionIDSupplier)

	sessionID := opts.SessionIDSupplier(context.Background())
	assert.NotEmpty(t, sessionID)
}

func TestWithEvalSetManager(t *testing.T) {
	custom := evalsetinmemory.New()
	opts := NewOptions(WithEvalSetManager(custom))

	assert.Equal(t, custom, opts.EvalSetManager)
}

func TestWithEvaluatorRegistry(t *testing.T) {
	custom := registry.New()
	opts := NewOptions(WithEvaluatorRegistry(custom))

	assert.Equal(t, custom, opts.EvaluatorRegistry)
}

func TestWithSessionIDSupplier(t *testing.T) {
	called := false
	supplier := func(ctx context.Context) string {
		called = true
		return "session-custom"
	}

	opts := NewOptions(WithSessionIDSupplier(supplier))
	assert.Equal(t, "session-custom", opts.SessionIDSupplier(context.Background()))
	assert.True(t, called)
}
