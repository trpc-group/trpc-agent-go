package filter

import (
	"context"
	"testing"
)

// mockAgentContext implements AgentContext for testing.
type mockAgentContext struct {
	context.Context
}

// mockFilter is a simple filter for testing.
type mockFilter struct {
	types      []InterceptionPoint
	preCalled  int
	postCalled int
	preReturn  bool
}

func (f *mockFilter) Type() []InterceptionPoint {
	return f.types
}

func (f *mockFilter) PreInvoke(ctx AgentContext, point InterceptionPoint) (bool, error) {
	f.preCalled++
	return f.preReturn, nil
}

func (f *mockFilter) PostInvoke(ctx AgentContext, point InterceptionPoint) (bool, error) {
	f.postCalled++
	return true, nil
}

func TestRegisterAndExecute_Success(t *testing.T) {
	fm := NewFilterManager()
	filter := &mockFilter{
		types:     []InterceptionPoint{PreLLMInvoke, PostLLMInvoke},
		preReturn: true,
	}
	err := fm.Register(filter)
	if err != nil {
		t.Fatalf("expected register success, got error: %v", err)
	}

	ctx := &mockAgentContext{context.Background()}
	ok := fm.Execute(PreLLMInvoke, ctx)
	if !ok {
		t.Errorf("expected pre-invoke to return true")
	}
	if filter.preCalled != 1 {
		t.Errorf("expected preCalled=1, got %d", filter.preCalled)
	}

	fm.Execute(PostLLMInvoke, ctx)
	if filter.postCalled != 1 {
		t.Errorf("expected postCalled=1, got %d", filter.postCalled)
	}
}

func TestRegister_Failure(t *testing.T) {
	// Test invalid point.
	fm := NewFilterManager()
	filter := &mockFilter{
		types:     []InterceptionPoint{"invalid_point"},
		preReturn: true,
	}
	if err := fm.Register(filter); err == nil {
		t.Fatal("expected error for invalid interception point, got nil")
	}

	// Test unpaired pre/post points.
	fm = NewFilterManager()
	filter = &mockFilter{
		types:     []InterceptionPoint{PreLLMInvoke},
		preReturn: true,
	}
	if err := fm.Register(filter); err == nil {
		t.Fatal("expected error for unpaired pre/post points, got nil")
	}

	// Test no interception points.
	fm = NewFilterManager()
	filter = &mockFilter{}
	if err := fm.Register(filter); err == nil {
		t.Fatal("expected error for no interception points, got nil")
	}
}

func TestExecute_PreInvokeInterruptsChain(t *testing.T) {
	fm := NewFilterManager()
	f1 := &mockFilter{
		types:     []InterceptionPoint{PreLLMInvoke, PostLLMInvoke},
		preReturn: false,
	}
	f2 := &mockFilter{
		types:     []InterceptionPoint{PreLLMInvoke, PostLLMInvoke},
		preReturn: true,
	}
	_ = fm.Register(f1)
	_ = fm.Register(f2)
	ctx := &mockAgentContext{context.Background()}
	ok := fm.Execute(PreLLMInvoke, ctx)
	if ok {
		t.Errorf("expected pre-invoke to return false due to first filter")
	}
	if f1.preCalled != 1 {
		t.Errorf("expected f1.preCalled=1, got %d", f1.preCalled)
	}
	if f2.preCalled != 0 {
		t.Errorf("expected f2.preCalled=0, got %d", f2.preCalled)
	}
}
