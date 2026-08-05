//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type countingOperator struct {
	*mockOperator
	searchCalls   int
	searchQueries []string
}

type invalidUpdatePolicyExtractor struct {
	*mockExtractor
}

func (*invalidUpdatePolicyExtractor) UpdatePolicy() extractor.UpdatePolicy {
	return extractor.UpdatePolicy("invalid")
}

type customUpdatePolicyExtractor struct {
	*mockExtractor
}

func (*customUpdatePolicyExtractor) UpdatePolicy() extractor.UpdatePolicy {
	return extractor.UpdatePolicyPreserveHistory
}

type countingModel struct {
	*mockModel
	calls int
}

type blockingCountingModel struct {
	*mockModel
	mu            sync.Mutex
	calls         int
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
}

func (m *blockingCountingModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	if call == 1 {
		close(m.firstStarted)
		select {
		case <-m.releaseFirst:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else if call == 2 {
		close(m.secondStarted)
	}
	return m.mockModel.GenerateContent(ctx, request)
}

func (m *blockingCountingModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type failingUpdateSessionService struct {
	session.Service
	err error
}

type stateOnlyReloadSessionService struct {
	session.Service
}

type failingGetSessionService struct {
	session.Service
}

func (s *failingGetSessionService) GetSession(
	context.Context,
	session.Key,
	...session.Option,
) (*session.Session, error) {
	return nil, assert.AnError
}

func (s *stateOnlyReloadSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	sess, err := s.Service.GetSession(ctx, key, opts...)
	if sess != nil {
		sess.Events = nil
	}
	return sess, err
}

func (s *failingUpdateSessionService) UpdateSessionState(
	context.Context,
	session.Key,
	session.StateMap,
) error {
	return s.err
}

func newPersistedAutoMemorySession(
	t *testing.T,
	service session.Service,
	ts time.Time,
	msg model.Message,
) (*session.Session, session.Key) {
	t.Helper()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "test-session"}
	sess, err := service.CreateSession(
		context.Background(), key, nil,
	)
	require.NoError(t, err)
	require.NoError(t, service.AppendEvent(
		context.Background(), sess, &event.Event{
			Timestamp: ts,
			Response:  &model.Response{Choices: []model.Choice{{Message: msg}}},
		},
	))
	loaded, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	return loaded, key
}

func (m *countingModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.calls++
	return m.mockModel.GenerateContent(ctx, request)
}

type failOnceAddOperator struct {
	*mockOperator
	failMemory string
	failed     bool
	attempted  []string
}

func (o *failOnceAddOperator) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryText string,
	topics []string,
	opts ...memory.AddOption,
) error {
	o.attempted = append(o.attempted, memoryText)
	if memoryText == o.failMemory && !o.failed {
		o.failed = true
		return assert.AnError
	}
	return o.mockOperator.AddMemory(ctx, userKey, memoryText, topics, opts...)
}

func (o *countingOperator) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	o.searchCalls++
	o.searchQueries = append(o.searchQueries, query)
	return o.mockOperator.SearchMemories(ctx, userKey, query, opts...)
}

func newExtractorWithOperation(
	t *testing.T,
	policy extractor.UpdatePolicy,
	op *extractor.Operation,
) extractor.MemoryExtractor {
	t.Helper()
	args := make(map[string]any)
	var toolName string
	switch op.Type {
	case extractor.OperationAdd:
		toolName = memory.AddToolName
		args["memory"] = op.Memory
	case extractor.OperationUpdate:
		toolName = memory.UpdateToolName
		args["memory_id"] = op.MemoryID
		args["memory"] = op.Memory
	case extractor.OperationDelete:
		toolName = memory.DeleteToolName
		args["memory_id"] = op.MemoryID
	case extractor.OperationClear:
		toolName = memory.ClearToolName
	default:
		t.Fatalf("unsupported operation type %q", op.Type)
	}
	if len(op.Topics) > 0 {
		args["topics"] = op.Topics
	}
	if op.MemoryKind != "" {
		args["memory_kind"] = string(op.MemoryKind)
	}
	if op.EventTime != nil {
		args["event_time"] = op.EventTime.Format(time.RFC3339Nano)
	}
	if len(op.Participants) > 0 {
		args["participants"] = op.Participants
	}
	if op.Location != "" {
		args["location"] = op.Location
	}
	payload, err := json.Marshal(args)
	require.NoError(t, err)
	return extractor.NewExtractor(
		newMockModelWithToolCalls([]model.ToolCall{{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      toolName,
				Arguments: payload,
			},
		}}),
		extractor.WithUpdatePolicy(policy),
	)
}

func newCountingAddExtractor(
	t *testing.T,
	policy extractor.UpdatePolicy,
	memoryText string,
	checker extractor.Checker,
) (*countingModel, extractor.MemoryExtractor) {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"memory": memoryText,
		"topics": []string{"test"},
	})
	require.NoError(t, err)
	mdl := &countingModel{mockModel: newMockModelWithToolCalls([]model.ToolCall{{
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      memory.AddToolName,
			Arguments: args,
		},
	}})}
	opts := []extractor.Option{extractor.WithUpdatePolicy(policy)}
	if checker != nil {
		opts = append(opts, extractor.WithChecker(checker))
	}
	return mdl, extractor.NewExtractor(mdl, opts...)
}

func TestUpdatePolicyFor_UsesBuiltInExtractorPolicy(t *testing.T) {
	for _, policy := range []extractor.UpdatePolicy{
		extractor.UpdatePolicyMergeSimilar,
		extractor.UpdatePolicyPreserveHistory,
		extractor.UpdatePolicyAppendOnly,
	} {
		builtin := extractor.NewExtractor(
			nil,
			extractor.WithUpdatePolicy(policy),
		)
		assert.Equal(t, policy, updatePolicyFor(builtin))
	}
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(&mockExtractor{}))
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(
		&invalidUpdatePolicyExtractor{mockExtractor: &mockExtractor{}},
	))
	assert.Equal(t, extractor.UpdatePolicyMergeSimilar, updatePolicyFor(
		&customUpdatePolicyExtractor{mockExtractor: &mockExtractor{}},
	))
}

func TestPreserveHistoryPolicy_AliceTimeEnrichmentUpdates(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{{
		ID: "alice-visit",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob on December 1st, 2025.",
			Topics:    []string{"Alice", "Bob", "visit"},
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		Topics:     []string{"Alice", "Bob", "visit"},
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "alice-visit", out[0].MemoryID)
	assert.Equal(t, &newTime, out[0].EventTime)
}

func TestPreserveHistoryPolicy_ExactDuplicateIgnoresTopicDrift(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "same",
		Memory: &memory.Memory{
			Memory: "User likes coffee.",
			Topics: []string{"coffee"},
			Kind:   memory.KindFact,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "  USER likes coffee  ",
		Topics:     []string{"coffee", "preferences"},
		MemoryKind: memory.KindFact,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	assert.Empty(t, out)
}

func TestPreserveHistoryPolicy_CoalescesEquivalentBatchOperations(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	first := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "User likes coffee.",
		Topics:     []string{"coffee"},
		MemoryKind: memory.KindFact,
	}
	last := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     " user likes COFFEE ",
		Topics:     []string{"coffee", "preference"},
		MemoryKind: memory.KindFact,
	}
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{first, last}, nil, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, last, out[0])
	assert.Equal(t, []string{"coffee", "preference"}, out[0].Topics)
}

func TestPreserveHistoryPolicy_CoalescesStagedEnrichments(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	ops := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "Alice likes coffee."},
		{Type: extractor.OperationAdd, Memory: "Alice likes dark coffee."},
		{Type: extractor.OperationAdd, Memory: "Alice likes dark roast coffee."},
	}
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), ops, nil, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Equal(t, "Alice likes dark roast coffee.", out[0].Memory)
}

func TestPreserveHistoryPolicy_CoalescesStagedUpdates(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "alice-coffee",
		Memory: &memory.Memory{
			Memory: "Alice likes coffee.",
			Kind:   memory.KindFact,
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{
			{Type: extractor.OperationAdd, Memory: "Alice likes dark coffee."},
			{Type: extractor.OperationAdd, Memory: "Alice likes dark roast coffee."},
		}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "alice-coffee", out[0].MemoryID)
	assert.Equal(t, "Alice likes dark roast coffee.", out[0].Memory)
}

func TestPreserveHistoryPolicy_PreservesConflictingStagedUpdates(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "alice-job",
		Memory: &memory.Memory{
			Memory: "Alice works at Acme as an engineer.",
			Kind:   memory.KindFact,
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{
			{Type: extractor.OperationAdd, Memory: "Alice works at Acme as a senior engineer."},
			{Type: extractor.OperationAdd, Memory: "Alice works at Acme as a principal engineer."},
		}, existing, nil,
	)
	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, "alice-job", out[0].MemoryID)
	assert.Equal(t, extractor.OperationAdd, out[1].Type)
	assert.Empty(t, out[1].MemoryID)
}

func TestAppendOrReplaceEquivalentOperation_KeepsDistinctOperations(t *testing.T) {
	first := &extractor.Operation{
		Type:   extractor.OperationAdd,
		Memory: "User likes coffee.",
	}
	second := &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "memory-id",
		Memory:   "User likes coffee.",
	}
	out := appendOrReplaceEquivalentOperation(
		[]*extractor.Operation{first}, second,
	)
	require.Len(t, out, 2)
	assert.Same(t, first, out[0])
	assert.Same(t, second, out[1])
}

func TestPreserveHistoryPolicy_ChangesRemainAdditive(t *testing.T) {
	tests := []struct {
		name    string
		oldText string
		newText string
		oldTime *time.Time
		newTime *time.Time
	}{
		{
			name:    "changed employer",
			oldText: "User works at Acme as an engineer.",
			newText: "User now works at Globex as an engineer.",
		},
		{
			name:    "single letter employer",
			oldText: "User works at A.",
			newText: "User works at B.",
		},
		{
			name:    "new negation",
			oldText: "User drinks coffee every morning.",
			newText: "User does not drink coffee every morning.",
		},
		{
			name: "single attribute replaced in long text",
			oldText: "Alice keeps the important family travel folder in the green cabinet " +
				"beside the upstairs bedroom window for future trips.",
			newText: "Alice keeps the important family travel folder in the red cabinet " +
				"beside the upstairs bedroom window for future trips.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := []*memory.Entry{{
				ID: "stored",
				Memory: &memory.Memory{
					Memory:    test.oldText,
					Kind:      memory.KindFact,
					EventTime: test.oldTime,
				},
			}}
			op := &extractor.Operation{
				Type:       extractor.OperationAdd,
				Memory:     test.newText,
				MemoryKind: memory.KindFact,
				EventTime:  test.newTime,
			}
			worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
			out := worker.reconcilePreserveHistoryOps(
				context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
			)
			require.Len(t, out, 1)
			assert.Equal(t, extractor.OperationAdd, out[0].Type)
		})
	}
}

func TestPreserveHistoryPolicy_DifferentEventDateRemainsAdditive(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 2, 16, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{{
		ID: "visit-one",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob at 4pm on December 1st, 2025.",
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 2nd, 2025.",
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
}

func TestPreserveHistoryPolicy_UnsafeModelUpdateBecomesAdd(t *testing.T) {
	existing := []*memory.Entry{{
		ID: "job",
		Memory: &memory.Memory{
			Memory: "User works at Acme.",
			Kind:   memory.KindFact,
		},
	}}
	op := &extractor.Operation{
		Type:       extractor.OperationUpdate,
		MemoryID:   "job",
		Memory:     "User now works at Globex.",
		MemoryKind: memory.KindFact,
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	out := worker.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, existing, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestAppendOnlyPolicy_ConvertsUpdateToAdd(t *testing.T) {
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())
	op := &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "old",
		Memory:   "new content",
	}
	out := worker.applyAppendOnlyPolicy(
		context.Background(), reconcileUserKey(), []*extractor.Operation{op}, nil,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestMergeSimilarPolicy_PreservesEveryOperationType(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyMergeSimilar)
	ops := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "new memory"},
		{Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "updated memory"},
		{Type: extractor.OperationDelete, MemoryID: "stored"},
		{Type: extractor.OperationClear},
	}
	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, nil, nil,
	)
	require.Len(t, out, len(ops))
	for index, op := range ops {
		assert.Equal(t, op.Type, out[index].Type)
	}
}

func TestPreserveHistoryPolicy_OperationContract(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyPreserveHistory)
	ops := []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "new memory"},
		{Type: extractor.OperationUpdate, MemoryID: "missing", Memory: "updated memory"},
		{Type: extractor.OperationDelete, MemoryID: "stored"},
		{Type: extractor.OperationClear},
	}
	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, nil,
		[]model.Message{model.NewUserMessage("I have changed my preferences.")},
	)
	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Equal(t, extractor.OperationAdd, out[1].Type)

	out = worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops[2:], nil,
		[]model.Message{model.NewUserMessage("Please forget everything about me.")},
	)
	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationDelete, out[0].Type)
	assert.Equal(t, extractor.OperationClear, out[1].Type)
}

func TestAppendOnlyPolicy_OperationContract(t *testing.T) {
	worker := newPolicyWorker(extractor.UpdatePolicyAppendOnly)
	existing := []*memory.Entry{{
		ID: "stored",
		Memory: &memory.Memory{
			Memory: "existing memory",
			Kind:   memory.KindFact,
		},
	}}
	ops := []*extractor.Operation{
		nil,
		{Type: extractor.OperationAdd, Memory: "existing memory"},
		{Type: extractor.OperationAdd, Memory: "new memory"},
		{Type: extractor.OperationAdd, Memory: " NEW memory! "},
		{Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "existing memory"},
		{Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "updated memory"},
		{Type: extractor.OperationDelete, MemoryID: "stored"},
		{Type: extractor.OperationClear},
		{Type: extractor.OperationType("unknown")},
	}
	out := worker.applyUpdatePolicy(
		context.Background(), reconcileUserKey(), ops, existing, nil,
	)
	require.Len(t, out, 2)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Equal(t, "new memory", out[0].Memory)
	assert.Equal(t, extractor.OperationAdd, out[1].Type)
	assert.Equal(t, "updated memory", out[1].Memory)
	assert.Empty(t, out[1].MemoryID)
}

func newPolicyWorker(policy extractor.UpdatePolicy) *AutoMemoryWorker {
	return NewAutoMemoryWorker(AutoMemoryConfig{
		Extractor: extractor.NewExtractor(
			nil,
			extractor.WithUpdatePolicy(policy),
		),
	}, newMockOperator())
}

func TestPreserveHistoryPolicy_DoesNotSearchPerOperation(t *testing.T) {
	existing := &memory.Entry{
		ID:      "alice-visit",
		AppName: "app",
		UserID:  "u1",
		Memory: &memory.Memory{
			Memory: "Alice visited Bob on December 1st, 2025.",
			Kind:   memory.KindFact,
		},
	}
	baseOperator := newMockOperator()
	baseOperator.searchResults = []*memory.Entry{existing}
	operator := &countingOperator{mockOperator: baseOperator}
	ext := newExtractorWithOperation(t, extractor.UpdatePolicyPreserveHistory, &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		MemoryKind: memory.KindFact,
	})
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	require.NoError(t, worker.createAutoMemory(context.Background(), reconcileUserKey(), []model.Message{
		model.NewUserMessage("Alice visited Bob at 4pm on December 1st, 2025."),
	}))
	assert.Equal(t, 1, operator.searchCalls)
	assert.Equal(t, 1, operator.updateCalls)
}

func TestUpdatePolicies_PreserveMergeSimilarSearchBehavior(t *testing.T) {
	existing := &memory.Entry{
		ID: "stored",
		Memory: &memory.Memory{
			Memory: "User likes tea.",
			Kind:   memory.KindFact,
		},
	}
	tests := []struct {
		name        string
		policy      extractor.UpdatePolicy
		operation   *extractor.Operation
		searchCalls int
		addCalls    int
	}{
		{
			name:   "mergeSimilar keeps per-add reconciliation",
			policy: extractor.UpdatePolicyMergeSimilar,
			operation: &extractor.Operation{
				Type: extractor.OperationAdd, Memory: "User likes tea.",
			},
			searchCalls: 2,
		},
		{
			name:   "append-only converts update without reconciliation",
			policy: extractor.UpdatePolicyAppendOnly,
			operation: &extractor.Operation{
				Type: extractor.OperationUpdate, MemoryID: "stored", Memory: "User likes coffee.",
			},
			searchCalls: 1,
			addCalls:    1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseOperator := newMockOperator()
			baseOperator.searchResults = []*memory.Entry{existing}
			operator := &countingOperator{mockOperator: baseOperator}
			ext := newExtractorWithOperation(t, test.policy, test.operation)
			worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
			require.NoError(t, worker.createAutoMemory(
				context.Background(),
				reconcileUserKey(),
				[]model.Message{
					model.NewUserMessage("I like tea."),
					model.NewAssistantMessage("Assistant-only detail."),
				},
			))
			assert.Equal(t, test.searchCalls, operator.searchCalls)
			assert.Equal(t, test.addCalls, operator.addCalls)
			require.NotEmpty(t, operator.searchQueries)
			assert.Equal(t, "I like tea.", operator.searchQueries[0])
		})
	}
}

func TestPreserveHistoryPolicy_ToolGatesAndUnknownOperations(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	existing := &memory.Entry{
		ID: "visit",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob on December 1st, 2025.",
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}
	add := &extractor.Operation{
		Type:       extractor.OperationAdd,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}

	allDisabled := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{},
	}, newMockOperator())
	out := appendPreserveHistoryAdd(
		context.Background(), allDisabled, reconcileUserKey(), nil, add,
		[]*memory.Entry{existing},
	)
	require.Equal(t, []*extractor.Operation{add}, out)

	appendOnly := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{memory.AddToolName: {}},
	}, newMockOperator())
	out = appendPreserveHistoryAdd(
		context.Background(), appendOnly, reconcileUserKey(), nil, add,
		[]*memory.Entry{existing},
	)
	require.Equal(t, []*extractor.Operation{add}, out)

	unknown := &extractor.Operation{Type: extractor.OperationType("unknown")}
	out = allDisabled.reconcilePreserveHistoryOps(
		context.Background(), reconcileUserKey(),
		[]*extractor.Operation{nil, unknown},
		[]*memory.Entry{nil, {}, {ID: "missing-memory"}}, nil,
	)
	require.Equal(t, []*extractor.Operation{unknown}, out)
}

func TestPreserveHistoryPolicy_ModelUpdateDecisions(t *testing.T) {
	oldTime := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	existing := &memory.Entry{
		ID: "visit",
		Memory: &memory.Memory{
			Memory:    "Alice visited Bob on December 1st, 2025.",
			Kind:      memory.KindEpisode,
			EventTime: &oldTime,
		},
	}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, newMockOperator())

	duplicate := &extractor.Operation{
		Type:       extractor.OperationUpdate,
		MemoryID:   existing.ID,
		Memory:     existing.Memory.Memory,
		MemoryKind: memory.KindEpisode,
		EventTime:  &oldTime,
	}
	assert.Empty(t, appendPreserveHistoryUpdate(
		context.Background(), worker, reconcileUserKey(), nil, duplicate, existing,
	))

	enrichment := &extractor.Operation{
		Type:       extractor.OperationUpdate,
		MemoryID:   existing.ID,
		Memory:     "Alice visited Bob at 4pm on December 1st, 2025.",
		MemoryKind: memory.KindEpisode,
		EventTime:  &newTime,
	}
	out := appendPreserveHistoryUpdate(
		context.Background(), worker, reconcileUserKey(), nil, enrichment, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationUpdate, out[0].Type)
	assert.Equal(t, existing.ID, out[0].MemoryID)

	updateDisabled := NewAutoMemoryWorker(AutoMemoryConfig{
		EnabledTools: map[string]struct{}{memory.AddToolName: {}},
	}, newMockOperator())
	out = appendPreserveHistoryUpdate(
		context.Background(), updateDisabled, reconcileUserKey(), nil, enrichment, existing,
	)
	require.Len(t, out, 1)
	assert.Equal(t, extractor.OperationAdd, out[0].Type)
	assert.Empty(t, out[0].MemoryID)
}

func TestPreserveHistoryCandidateLess(t *testing.T) {
	entry := func(score float64) *memory.Entry {
		return &memory.Entry{Score: score}
	}
	tests := []struct {
		name  string
		left  *preserveHistoryCandidate
		right *preserveHistoryCandidate
		want  bool
	}{
		{
			name:  "duplicate wins",
			left:  &preserveHistoryCandidate{entry: entry(1)},
			right: &preserveHistoryCandidate{entry: entry(0), duplicate: true},
			want:  true,
		},
		{
			name:  "higher old coverage wins",
			left:  &preserveHistoryCandidate{entry: entry(1), oldCoverage: 0.95},
			right: &preserveHistoryCandidate{entry: entry(0), oldCoverage: 1},
			want:  true,
		},
		{
			name: "higher new coverage wins",
			left: &preserveHistoryCandidate{
				entry: entry(1), oldCoverage: 1, newCoverage: 0.8,
			},
			right: &preserveHistoryCandidate{
				entry: entry(0), oldCoverage: 1, newCoverage: 0.9,
			},
			want: true,
		},
		{
			name: "higher score wins",
			left: &preserveHistoryCandidate{
				entry: entry(0.7), oldCoverage: 1, newCoverage: 1,
			},
			right: &preserveHistoryCandidate{
				entry: entry(0.8), oldCoverage: 1, newCoverage: 1,
			},
			want: true,
		},
		{
			name:  "weaker candidate loses",
			left:  &preserveHistoryCandidate{entry: entry(1), duplicate: true},
			right: &preserveHistoryCandidate{entry: entry(0)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, preserveHistoryCandidateLess(test.left, test.right))
		})
	}
}

func TestExactMemoryDuplicate_MetadataContract(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	otherTime := eventTime.Add(time.Hour)
	stored := &memory.Memory{
		Memory:       "Alice visited Bob.",
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice", "Bob"},
		Location:     "Paris",
	}
	base := extractor.Operation{
		Memory:       " alice VISITED bob ",
		MemoryKind:   memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"bob", "alice"},
		Location:     " paris ",
	}
	assert.True(t, exactMemoryDuplicate(&base, stored))

	tests := []struct {
		name   string
		mutate func(*extractor.Operation)
	}{
		{name: "text", mutate: func(op *extractor.Operation) { op.Memory = "different" }},
		{name: "kind", mutate: func(op *extractor.Operation) { op.MemoryKind = memory.KindFact }},
		{name: "time", mutate: func(op *extractor.Operation) { op.EventTime = &otherTime }},
		{name: "participants", mutate: func(op *extractor.Operation) { op.Participants = []string{"Alice"} }},
		{name: "location", mutate: func(op *extractor.Operation) { op.Location = "London" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			op := base
			test.mutate(&op)
			assert.False(t, exactMemoryDuplicate(&op, stored))
		})
	}
}

func TestMetadataIdentityCompatible(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	otherDay := eventTime.Add(24 * time.Hour)
	stored := &memory.Memory{
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice"},
		Location:     "Paris",
	}
	base := extractor.Operation{
		MemoryKind:   memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice", "Bob"},
		Location:     " paris ",
	}
	assert.True(t, metadataIdentityCompatible(&base, stored))

	tests := []struct {
		name   string
		mutate func(*extractor.Operation)
	}{
		{name: "kind", mutate: func(op *extractor.Operation) { op.MemoryKind = memory.KindFact }},
		{name: "event date", mutate: func(op *extractor.Operation) { op.EventTime = &otherDay }},
		{name: "participants", mutate: func(op *extractor.Operation) { op.Participants = []string{"Bob"} }},
		{name: "location", mutate: func(op *extractor.Operation) { op.Location = "London" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			op := base
			test.mutate(&op)
			assert.False(t, metadataIdentityCompatible(&op, stored))
		})
	}
}

func TestClassifyPreserveHistoryCandidate_RejectsSemanticConflicts(t *testing.T) {
	entry := func(text string) *memory.Entry {
		return &memory.Entry{
			ID: "candidate",
			Memory: &memory.Memory{
				Memory: text,
				Kind:   memory.KindFact,
			},
		}
	}
	op := func(text string) *extractor.Operation {
		return &extractor.Operation{
			Type:       extractor.OperationAdd,
			Memory:     text,
			MemoryKind: memory.KindFact,
		}
	}
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "critical value format changed",
			old:  "Alice records the detailed family appointment at 4:00 in the shared calendar for everyone to review before the weekly planning meeting.",
			new:  "Alice records the detailed family appointment in the shared calendar at 4 00 for everyone to review before the weekly planning meeting.",
		},
		{
			name: "negation count changed",
			old:  "Alice is not available for the detailed family planning meeting in the shared office calendar this week.",
			new:  "Alice is not not available for the detailed family planning meeting in the shared office calendar this week.",
		},
		{
			name: "new state change marker",
			old:  "Alice stores the detailed family travel plans in the shared office cabinet for everyone to review before each meeting.",
			new:  "Alice now stores the detailed family travel plans in the shared office cabinet for everyone to review before each meeting.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Nil(t, classifyPreserveHistoryCandidate(op(test.new), entry(test.old)))
		})
	}
}

func TestPolicyComparisonHelpers(t *testing.T) {
	eventTime := time.Date(2025, 12, 1, 16, 0, 0, 0, time.UTC)
	sameTime := eventTime
	otherTime := eventTime.Add(time.Hour)
	assert.True(t, equalOptionalTime(nil, nil))
	assert.False(t, equalOptionalTime(nil, &eventTime))
	assert.True(t, equalOptionalTime(&eventTime, &sameTime))
	assert.False(t, equalOptionalTime(&eventTime, &otherTime))

	assert.True(t, equalStringSet([]string{" Alice ", "BOB"}, []string{"bob", "alice"}))
	assert.False(t, equalStringSet([]string{"Alice"}, []string{"Alice", "Bob"}))
	assert.False(t, equalStringSet([]string{"Alice", "Bob"}, []string{"Alice", "Carol"}))
	assert.True(t, isStringSubset([]string{"Alice"}, []string{"Bob", "alice"}))
	assert.False(t, isStringSubset([]string{"Alice"}, []string{"Bob"}))

	oldCoverage, newCoverage := directionalTokenCoverage("", "new memory")
	assert.Zero(t, oldCoverage)
	assert.Zero(t, newCoverage)
	assert.True(t, criticalValuesPreserved("Meeting at 4:00 pm", "Meeting at 4:00 pm today"))
	assert.False(t, criticalValuesPreserved("Meeting at 4:00 pm", "Meeting today"))
	assert.Equal(t, "not|not", negationSignature("Not ready and NOT available"))

}

func TestExecuteOperation_ReturnsPersistenceErrors(t *testing.T) {
	tests := []struct {
		name      string
		op        *extractor.Operation
		configure func(*mockOperator)
		want      string
	}{
		{
			name: "add",
			op:   &extractor.Operation{Type: extractor.OperationAdd, Memory: "memory"},
			configure: func(operator *mockOperator) {
				operator.addErr = assert.AnError
			},
			want: "add memory",
		},
		{
			name: "update",
			op: &extractor.Operation{
				Type: extractor.OperationUpdate, MemoryID: "memory-id", Memory: "memory",
			},
			configure: func(operator *mockOperator) {
				operator.updateErr = assert.AnError
			},
			want: "update memory",
		},
		{
			name: "delete",
			op:   &extractor.Operation{Type: extractor.OperationDelete, MemoryID: "memory-id"},
			configure: func(operator *mockOperator) {
				operator.deleteErr = assert.AnError
			},
			want: "delete memory",
		},
		{
			name: "clear",
			op:   &extractor.Operation{Type: extractor.OperationClear},
			configure: func(operator *mockOperator) {
				operator.clearErr = assert.AnError
			},
			want: "clear memories",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operator := newMockOperator()
			test.configure(operator)
			worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
			err := worker.executeOperation(context.Background(), reconcileUserKey(), test.op)
			assert.ErrorContains(t, err, test.want)
			assert.ErrorIs(t, err, assert.AnError)
		})
	}
}

func TestExecuteOperation_DeleteRetryIsIdempotent(t *testing.T) {
	operator := newMockOperator()
	operator.deleteErr = errors.New("memory with id memory-id not found")
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
	err := worker.executeOperation(
		context.Background(),
		reconcileUserKey(),
		&extractor.Operation{Type: extractor.OperationDelete, MemoryID: "memory-id"},
	)
	assert.NoError(t, err)
}

func TestAutoMemoryWorker_MergeSimilarPersistenceFailureRemainsBestEffort(t *testing.T) {
	operator := newMockOperator()
	operator.addErr = assert.AnError
	ext := &mockExtractor{ops: []*extractor.Operation{
		{
			Type:   extractor.OperationAdd,
			Memory: "User likes tea.",
		},
		{
			Type:     extractor.OperationUpdate,
			MemoryID: "existing-memory",
			Memory:   "User likes green tea.",
		},
	}}
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	sess := newTestSession("app", "u1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("I like tea."))

	require.NoError(t, worker.EnqueueJob(context.Background(), sess))
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.True(t, ok)
	_, ok = sess.GetState(pendingAutoMemoryBatchStateKey)
	assert.False(t, ok)
	assert.Equal(t, 1, operator.updateCalls)
}

func TestAutoMemoryWorker_PreserveHistoryPersistenceFailureDoesNotAdvanceWatermark(t *testing.T) {
	operator := newMockOperator()
	operator.addErr = assert.AnError
	ext := newExtractorWithOperation(t, extractor.UpdatePolicyPreserveHistory, &extractor.Operation{
		Type:   extractor.OperationAdd,
		Memory: "User likes tea.",
	})
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	sess := newTestSession("app", "u1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("I like tea."))

	err := worker.EnqueueJob(context.Background(), sess)
	require.Error(t, err)
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.False(t, ok)

	operator.addErr = nil
	require.NoError(t, worker.EnqueueJob(context.Background(), sess))
	_, ok = sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.True(t, ok)
}

func TestAutoMemoryWorker_PreserveHistoryCompletesNewOperationBatch(t *testing.T) {
	_, ext := newCountingAddExtractor(
		t, extractor.UpdatePolicyPreserveHistory, "User likes tea.", nil,
	)
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	sess := newTestSession("app", "u1")
	eventTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	appendSessionMessage(sess, eventTime, model.NewUserMessage("I like tea."))

	require.NoError(t, worker.EnqueueJob(context.Background(), sess))
	assert.Equal(t, 1, operator.addCalls)
	assert.True(t, readLastExtractAt(sess).Equal(eventTime))
	_, ok := sess.GetState(pendingAutoMemoryBatchStateKey)
	assert.False(t, ok)
}

func TestAutoMemoryWorker_PreserveHistoryHandlesNewDeltaAfterPendingBatch(t *testing.T) {
	for _, test := range []struct {
		name             string
		shouldExtract    bool
		wantAddCalls     int
		wantModelCalls   int
		wantWatermarkLag bool
	}{
		{
			name:           "extract new delta",
			shouldExtract:  true,
			wantAddCalls:   2,
			wantModelCalls: 1,
		},
		{
			name:             "checker skips new delta",
			shouldExtract:    false,
			wantAddCalls:     1,
			wantModelCalls:   0,
			wantWatermarkLag: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkerCalls := 0
			mdl, ext := newCountingAddExtractor(
				t,
				extractor.UpdatePolicyPreserveHistory,
				"User likes fresh tea.",
				func(*extractor.ExtractionContext) bool {
					checkerCalls++
					return test.shouldExtract
				},
			)
			operator := newMockOperator()
			worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
			sess := newTestSession("app", "u1")
			pendingTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
			latestTime := pendingTime.Add(time.Minute)
			appendSessionMessage(sess, pendingTime, model.NewUserMessage("I like tea."))
			appendSessionMessage(sess, latestTime, model.NewUserMessage("I like fresh tea."))
			require.NoError(t, persistPendingAutoMemoryBatch(
				context.Background(), sess, &pendingAutoMemoryBatch{
					Version:  pendingAutoMemoryBatchVersion,
					LatestTs: pendingTime,
					Operations: []*extractor.Operation{{
						Type:   extractor.OperationAdd,
						Memory: "User likes tea.",
					}},
				},
			))

			require.NoError(t, worker.EnqueueJob(context.Background(), sess))
			assert.Equal(t, 1, checkerCalls)
			assert.Equal(t, test.wantAddCalls, operator.addCalls)
			assert.Equal(t, test.wantModelCalls, mdl.calls)
			_, ok := sess.GetState(pendingAutoMemoryBatchStateKey)
			assert.False(t, ok)
			if test.wantWatermarkLag {
				assert.True(t, readLastExtractAt(sess).Equal(pendingTime))
				return
			}
			assert.True(t, readLastExtractAt(sess).Equal(latestTime))
		})
	}
}

func TestAutoMemoryWorker_PreserveHistoryStalePendingBatchHonorsChecker(t *testing.T) {
	checkerCalls := 0
	mdl, ext := newCountingAddExtractor(
		t,
		extractor.UpdatePolicyPreserveHistory,
		"User likes coffee.",
		func(*extractor.ExtractionContext) bool {
			checkerCalls++
			return false
		},
	)
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, operator)
	sess := newTestSession("app", "u1")
	watermark := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	writeLastExtractAt(sess, watermark)
	appendSessionMessage(sess, watermark.Add(time.Minute), model.NewUserMessage("I like coffee."))
	require.NoError(t, persistPendingAutoMemoryBatch(
		context.Background(), sess, &pendingAutoMemoryBatch{
			Version:  pendingAutoMemoryBatchVersion,
			LatestTs: watermark,
			Operations: []*extractor.Operation{{
				Type:   extractor.OperationAdd,
				Memory: "stale operation",
			}},
		},
	))

	require.NoError(t, worker.EnqueueJob(context.Background(), sess))
	assert.Equal(t, 1, checkerCalls)
	assert.Zero(t, mdl.calls)
	assert.Zero(t, operator.addCalls)
	assert.True(t, readLastExtractAt(sess).Equal(watermark))
	_, ok := sess.GetState(pendingAutoMemoryBatchStateKey)
	assert.False(t, ok)
}

func TestProcessAutoMemoryDelta_HandlesPreparationOutcomes(t *testing.T) {
	userKey := reconcileUserKey()
	latestTime := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	messages := []model.Message{model.NewUserMessage("I like tea.")}

	t.Run("pending state error", func(t *testing.T) {
		_, ext := newCountingAddExtractor(
			t, extractor.UpdatePolicyPreserveHistory, "User likes tea.", nil,
		)
		worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, newMockOperator())
		sess := newTestSession("app", "u1")
		sess.SetState(pendingAutoMemoryBatchStateKey, []byte(`{`))
		err := worker.processAutoMemoryDelta(
			context.Background(), userKey, sess, latestTime, messages,
		)
		assert.ErrorContains(t, err, "decode pending operation batch")
	})

	t.Run("extract error", func(t *testing.T) {
		ext := extractor.NewExtractor(
			&mockModel{err: assert.AnError},
			extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
		)
		worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, newMockOperator())
		sess := newTestSession("app", "u1")
		appendSessionMessage(sess, latestTime, messages[0])
		err := worker.processAutoMemoryDelta(
			context.Background(), userKey, sess, latestTime, messages,
		)
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("empty operation batch", func(t *testing.T) {
		ext := extractor.NewExtractor(
			newMockModelWithToolCalls(nil),
			extractor.WithUpdatePolicy(extractor.UpdatePolicyPreserveHistory),
		)
		worker := NewAutoMemoryWorker(AutoMemoryConfig{Extractor: ext}, newMockOperator())
		sess := newTestSession("app", "u1")
		appendSessionMessage(sess, latestTime, messages[0])
		require.NoError(t, worker.processAutoMemoryDelta(
			context.Background(), userKey, sess, latestTime, messages,
		))
		assert.True(t, readLastExtractAt(sess).Equal(latestTime))
		_, ok := sess.GetState(pendingAutoMemoryBatchStateKey)
		assert.False(t, ok)
	})
}

func TestExecuteAutoMemoryOperations_NonDefaultPolicyReturnsError(t *testing.T) {
	operator := newMockOperator()
	operator.addErr = assert.AnError
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
	err := worker.executeAutoMemoryOperations(
		context.Background(),
		reconcileUserKey(),
		[]*extractor.Operation{{
			Type:   extractor.OperationAdd,
			Memory: "User likes tea.",
		}},
		extractor.UpdatePolicyPreserveHistory,
	)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestPersistPendingAutoMemoryBatch_RejectsUnencodableOperation(t *testing.T) {
	sess := newTestSession("app", "u1")
	invalidTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	err := persistPendingAutoMemoryBatch(context.Background(), sess, &pendingAutoMemoryBatch{
		Version:  pendingAutoMemoryBatchVersion,
		LatestTs: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC),
		Operations: []*extractor.Operation{{
			Type:      extractor.OperationAdd,
			Memory:    "User likes tea.",
			EventTime: &invalidTime,
		}},
	})
	assert.ErrorContains(t, err, "encode pending operation batch")
}

func TestExecutePendingAutoMemoryBatch_ReturnsCheckpointError(t *testing.T) {
	operator := newMockOperator()
	worker := NewAutoMemoryWorker(AutoMemoryConfig{}, operator)
	sess := newTestSession("app", "u1")
	invalidTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	pending := &pendingAutoMemoryBatch{
		Version:  pendingAutoMemoryBatchVersion,
		LatestTs: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC),
		Operations: []*extractor.Operation{{
			Type:      extractor.OperationAdd,
			Memory:    "User likes tea.",
			EventTime: &invalidTime,
		}},
	}

	err := worker.executePendingAutoMemoryBatch(
		context.Background(), reconcileUserKey(), sess, pending,
	)
	assert.ErrorContains(t, err, "encode pending operation batch")
	assert.Equal(t, 1, operator.addCalls)
	assert.Equal(t, 1, pending.Next)
}
