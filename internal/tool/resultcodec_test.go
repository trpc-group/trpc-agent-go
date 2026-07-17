//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
)

type rcFakeCallable struct {
	name        string
	longRunning bool
}

func (f *rcFakeCallable) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name}
}
func (f *rcFakeCallable) Call(context.Context, []byte) (any, error) { return nil, nil }
func (f *rcFakeCallable) LongRunning() bool                         { return f.longRunning }

func TestResolveResultCodec_FromWrap(t *testing.T) {
	codec := resultcodec.JSON()
	wrapped := resultcodec.Wrap(&rcFakeCallable{name: "b"}, codec)
	if got := ResolveResultCodec(wrapped); got != codec {
		t.Fatalf("expected codec from Wrap, got %v", got)
	}
}

func TestResolveResultCodec_NoCodec(t *testing.T) {
	if got := ResolveResultCodec(&rcFakeCallable{name: "b"}); got != nil {
		t.Fatalf("expected nil codec, got %v", got)
	}
}

func TestResolveResultCodec_Nil(t *testing.T) {
	if got := ResolveResultCodec(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestResolveResultCodec_ThroughNamedTool(t *testing.T) {
	codec := resultcodec.XML()
	wrapped := resultcodec.Wrap(&rcFakeCallable{name: "b"}, codec)
	named := NewUnprefixedNamedTool(wrapped)
	if got := ResolveResultCodec(named); got != codec {
		t.Fatalf("expected codec through NamedTool, got %v", got)
	}
}

func TestResolveSemantic_SeesThroughResultCodecWrap(t *testing.T) {
	wrapped := resultcodec.Wrap(
		&rcFakeCallable{name: "b", longRunning: true},
		resultcodec.JSON(),
	)
	lr, ok := ResolveSemantic(wrapped).(interface{ LongRunning() bool })
	if !ok {
		t.Fatal("ResolveSemantic should see through Wrap to LongRunner")
	}
	if !lr.LongRunning() {
		t.Fatal("LongRunning should be true through the wrapper")
	}
}

func TestResolveDeclaration_SeesThroughResultCodecWrap(t *testing.T) {
	wrapped := resultcodec.Wrap(
		&rcFakeCallable{name: "b", longRunning: true},
		resultcodec.JSON(),
	)
	lr, ok := ResolveDeclaration(wrapped).(interface{ LongRunning() bool })
	if !ok {
		t.Fatal("ResolveDeclaration should see through Wrap to LongRunner")
	}
	if !lr.LongRunning() {
		t.Fatal("LongRunning should be true through the wrapper")
	}
}

// rcSelfUnwrap returns itself from Unwrap, forming a cycle.
type rcSelfUnwrap struct {
	name string
}

func (s *rcSelfUnwrap) Declaration() *tool.Declaration { return &tool.Declaration{Name: s.name} }
func (s *rcSelfUnwrap) Unwrap() tool.Tool              { return s }

func TestResolvers_CyclicUnwrapTerminate(t *testing.T) {
	// A cyclic Unwrap() chain must not cause unbounded recursion; the
	// depth-bounded traversals return instead of hanging or overflowing.
	s := &rcSelfUnwrap{name: "cyclic"}
	if got := ResolveSemantic(s); got == nil {
		t.Fatal("ResolveSemantic should return a tool, not nil")
	}
	if got := ResolveDeclaration(s); got == nil {
		t.Fatal("ResolveDeclaration should return a tool, not nil")
	}
	if got := ResolveResultCodec(s); got != nil {
		t.Fatal("ResolveResultCodec should return nil when no codec is present")
	}
}
