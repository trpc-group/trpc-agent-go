//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// plainTool publishes no metadata at all, like almost every tool in a toolset.
type plainTool struct{}

func (plainTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: "plain"} }

// unsafeTool objects to running beside its siblings.
type unsafeTool struct{}

func (unsafeTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: "unsafe"} }
func (unsafeTool) IsConcurrencySafe() bool        { return false }

// guaranteeingTool promises it can share a turn with anything.
type guaranteeingTool struct{}

func (guaranteeingTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: "safe"} }
func (guaranteeingTool) IsConcurrencySafe() bool        { return true }

// declaresConcurrency reports which of the three ConcurrencyAware states a tool
// is in when asked directly: (answer, declared).
func declaresConcurrency(t tool.Tool) (bool, bool) {
	aware, ok := t.(tool.ConcurrencyAware)
	if !ok {
		return false, false
	}
	return aware.IsConcurrencySafe(), true
}

// Wrapping preserves all three concurrency states, because the wrapper does not
// answer for the tool: it is resolved to it.
//
// NamedTool wraps every tool from a toolset, so whatever it answered would be
// the answer for the file and shell toolsets wholesale. Reading
// MetadataOf(...).ConcurrencySafe had them all object — a measured run did
// exactly that: 45 turns, 45 tool calls, one call per turn — and reading the
// admission default had them all guarantee reentrancy they never claimed. Not
// answering is the only reading that changes nothing.
func TestNamedToolPreservesConcurrencyState(t *testing.T) {
	tests := []struct {
		name string
		tool tool.Tool
		// wantDeclared is whether the resolved tool declares anything;
		// wantSafe is its answer when it does.
		wantDeclared bool
		wantSafe     bool
	}{
		{"declares nothing", plainTool{}, false, false},
		{"objects", unsafeTool{}, true, false},
		{"guarantees", guaranteeingTool{}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := NewUnprefixedNamedTool(tt.tool)

			if _, declared := declaresConcurrency(wrapped); declared {
				t.Error("the wrapper must not answer ConcurrencyAware itself")
			}
			gotSafe, gotDeclared := declaresConcurrency(ResolveSemantic(wrapped))
			if gotDeclared != tt.wantDeclared || (gotDeclared && gotSafe != tt.wantSafe) {
				t.Errorf("resolved state = (%v, declared %v), want (%v, declared %v)",
					gotSafe, gotDeclared, tt.wantSafe, tt.wantDeclared)
			}

			// Admission: only an objection is false.
			wantAdmitted := !tt.wantDeclared || tt.wantSafe
			if got := IsConcurrencySafe(wrapped); got != wantAdmitted {
				t.Errorf("IsConcurrencySafe(wrapped) = %v, want %v", got, wantAdmitted)
			}
			if got := tool.IsConcurrencySafe(wrapped); got != wantAdmitted {
				t.Errorf("tool.IsConcurrencySafe(wrapped) = %v, want %v", got, wantAdmitted)
			}

			// Description: MetadataOf synthesizes ConcurrencySafe only from a
			// declared guarantee, and the wrapper delegates rather than
			// answering, so it reports exactly what the tool would.
			if got, want := tool.MetadataOf(wrapped), tool.MetadataOf(tt.tool); got != want {
				t.Errorf("MetadataOf(wrapped) = %+v, want %+v", got, want)
			}
		})
	}
}

// A declaration overlay changes only what the model is shown, so it must not
// change how the call is scheduled.
//
// The overlay exposes none of the wrapped tool's optional interfaces — not even
// Original() — which is why schedulers resolve through this package rather than
// calling tool.IsConcurrencySafe on what they are handed: asked directly, the
// overlay declares nothing and a patched description restores the parallel path.
func TestApplyDeclarationsPreservesConcurrencyObjection(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{unsafeTool{}},
		[]tool.Declaration{{Name: "unsafe", Description: "patched"}},
	)[0]

	if got := patched.Declaration().Description; got != "patched" {
		t.Fatalf("declaration overlay was not applied: description = %q", got)
	}
	if IsConcurrencySafe(patched) {
		t.Error("a patched declaration must not discard the tool's objection")
	}
	// The wrapper stays opaque; that isolation is what ApplyDeclarations is for,
	// and why the resolving helper exists.
	if !tool.IsConcurrencySafe(patched) {
		t.Error("the overlay wrapper must not publish the objection itself")
	}
	if got := tool.MetadataOf(patched); got != (tool.ToolMetadata{}) {
		t.Errorf("the overlay wrapper must publish no metadata, got %+v", got)
	}
}

// The same overlay must not manufacture an objection either.
func TestApplyDeclarationsPreservesConcurrencyDefault(t *testing.T) {
	patched := ApplyDeclarations(
		[]tool.Tool{plainTool{}},
		[]tool.Declaration{{Name: "plain", Description: "patched"}},
	)[0]

	if !IsConcurrencySafe(patched) {
		t.Error("a patched declaration must leave an unobjecting tool admissible")
	}
}

// A NamedTool from a toolset is the other wrapper schedulers see, so the helper
// must resolve through it too.
func TestIsConcurrencySafeResolvesNamedTools(t *testing.T) {
	if IsConcurrencySafe(NewUnprefixedNamedTool(unsafeTool{})) {
		t.Error("a named wrapper must not hide the objection")
	}
	if !IsConcurrencySafe(NewUnprefixedNamedTool(plainTool{})) {
		t.Error("a named wrapper must not manufacture an objection")
	}
	if !IsConcurrencySafe(nil) {
		t.Error("a nil tool cannot object")
	}
}

// Wrappers nest: a toolset's tool is a NamedTool, and patching its declaration
// wraps that in an overlay — or the reverse, when the patch lands first. The
// resolver must reach the tool through both orders, and the state it finds
// there must be the tool's own.
func TestResolveSemanticReachesThroughNestedWrappers(t *testing.T) {
	tests := []struct {
		name string
		tool tool.Tool
		// wrap builds the nested shape around the tool.
		wrap func(tool.Tool) tool.Tool
	}{
		{"named over overlay, objecting", unsafeTool{}, func(t tool.Tool) tool.Tool {
			return NewUnprefixedNamedTool(overlay(t))
		}},
		{"overlay over named, objecting", unsafeTool{}, func(t tool.Tool) tool.Tool {
			return overlay(NewUnprefixedNamedTool(t))
		}},
		{"named over overlay, nothing declared", plainTool{}, func(t tool.Tool) tool.Tool {
			return NewUnprefixedNamedTool(overlay(t))
		}},
		{"overlay over named, guaranteeing", guaranteeingTool{}, func(t tool.Tool) tool.Tool {
			return overlay(NewUnprefixedNamedTool(t))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := tt.wrap(tt.tool)
			if got := resolveConcurrencyOwner(wrapped); got != tt.tool {
				t.Fatalf("resolveConcurrencyOwner() = %T, want the wrapped %T", got, tt.tool)
			}
			if got, want := IsConcurrencySafe(wrapped), tool.IsConcurrencySafe(tt.tool); got != want {
				t.Errorf("IsConcurrencySafe(nested) = %v, want the tool's own %v", got, want)
			}
		})
	}
}

// originalOnlyWrapper is a wrapper that is not a NamedTool but exposes what it
// wraps through Original(), the shape ToolPipe and toolsearch use.
type originalOnlyWrapper struct {
	plainTool
	inner tool.Tool
}

func (w *originalOnlyWrapper) Original() tool.Tool { return w.inner }

// The concurrency resolver follows any Original() chain, interleaved with
// overlays, while ResolveSemantic keeps its narrower contract: a wrapper that is
// not a NamedTool still stands in for the tool's other capabilities.
func TestConcurrencyResolutionFollowsOriginalChains(t *testing.T) {
	wrapped := overlay(&originalOnlyWrapper{inner: overlay(NewUnprefixedNamedTool(unsafeTool{}))})

	if got := resolveConcurrencyOwner(wrapped); got != tool.Tool(unsafeTool{}) {
		t.Fatalf("resolveConcurrencyOwner() = %T, want the innermost tool", got)
	}
	if IsConcurrencySafe(wrapped) {
		t.Error("the objection must be found through every wrapper in the chain")
	}
	if _, ok := ResolveSemantic(wrapped).(*originalOnlyWrapper); !ok {
		t.Errorf("ResolveSemantic must stop at a wrapper that is not a NamedTool, got %T",
			ResolveSemantic(wrapped))
	}
}

// overlay patches a tool's description, producing a declaration wrapper.
func overlay(t tool.Tool) tool.Tool {
	return ApplyDeclarations(
		[]tool.Tool{t},
		[]tool.Declaration{{Name: t.Declaration().Name, Description: "patched"}},
	)[0]
}

// selfWrapper is a wrapper whose Original() points back at itself, the shape
// that would otherwise loop.
type selfWrapper struct{ plainTool }

func (s *selfWrapper) Original() tool.Tool { return s }

// nilWrapper is a wrapper whose Original() is nil.
type nilWrapper struct{ plainTool }

func (*nilWrapper) Original() tool.Tool { return nil }

// A wrapper returning itself or nil ends the chain at the wrapper rather than
// looping or resolving to nothing.
func TestConcurrencyResolutionStopsAtDegenerateWrappers(t *testing.T) {
	self := &selfWrapper{}
	if got := resolveConcurrencyOwner(self); got != tool.Tool(self) {
		t.Errorf("a self-referential wrapper must resolve to itself, got %T", got)
	}
	empty := &nilWrapper{}
	if got := resolveConcurrencyOwner(empty); got != tool.Tool(empty) {
		t.Errorf("a wrapper with no original must resolve to itself, got %T", got)
	}
	if !IsConcurrencySafe(self) || !IsConcurrencySafe(empty) {
		t.Error("degenerate wrappers declare nothing and are admitted")
	}
}
