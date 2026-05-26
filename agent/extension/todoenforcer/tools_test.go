//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todoenforcer

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// newDeclareBlockerTool falls back to package defaults whenever
// the caller passes empty strings. New() always supplies non-empty
// values via defaultOptions, so without these tests the fallback
// branches stay dark — exactly the kind of "would never break in
// practice but invisible if regressed" branch coverage demands.

func TestNewDeclareBlockerTool_EmptyNameAndDescription_FallBackToDefaults(t *testing.T) {
	tl := newDeclareBlockerTool("", "", nil)
	require.NotNil(t, tl)
	assert.Equal(t, DefaultDeclareBlockerToolName, tl.name,
		"empty name must default to DefaultDeclareBlockerToolName")
	assert.Equal(t, DefaultDeclareBlockerToolDescription, tl.description,
		"empty description must default to DefaultDeclareBlockerToolDescription")
	assert.Nil(t, tl.enforcer,
		"nil enforcer must round-trip unchanged — Call must defend against this at use time")
}

func TestNewDeclareBlockerTool_ExplicitValuesAreUsedVerbatim(t *testing.T) {
	tl := newDeclareBlockerTool("custom_name", "custom desc", nil)
	assert.Equal(t, "custom_name", tl.name)
	assert.Equal(t, "custom desc", tl.description)
}

// decodeDeclareBlockerInput is the schema-error fence between
// JSON wire format and our Go types. The Call hot path is fairly
// well covered already, but the two failure modes — empty payload
// and malformed JSON — are not exercised because Call's "happy"
// tests always pass a valid object. We test the parser directly.
func TestDecodeDeclareBlockerInput_RejectsEmptyArgs(t *testing.T) {
	_, err := decodeDeclareBlockerInput(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty arguments",
		"nil payload must produce the documented \"empty arguments\" error")

	_, err = decodeDeclareBlockerInput([]byte{})
	require.Error(t, err, "zero-length slice is observationally identical to nil")
}

func TestDecodeDeclareBlockerInput_RejectsMalformedJSON(t *testing.T) {
	_, err := decodeDeclareBlockerInput([]byte("not a json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode arguments",
		"unmarshal failures must be wrapped with the decode-arguments prefix so callers can match")
}

func TestDecodeDeclareBlockerInput_AcceptsWellFormedJSON(t *testing.T) {
	in, err := decodeDeclareBlockerInput([]byte(`{"reason":"missing access"}`))
	require.NoError(t, err)
	assert.Equal(t, "missing access", in.Reason,
		"well-formed JSON must round-trip the reason field")

	// Extra unknown fields must be silently ignored — this is the
	// jsonschema contract we advertise via "type:object" without
	// "additionalProperties:false" in Declaration(). A future
	// schema tightening should add a positive test here, not here
	// (i.e. don't surprise downstream callers with an error path
	// they previously did not see).
	in, err = decodeDeclareBlockerInput([]byte(`{"reason":"x","unknown":42}`))
	require.NoError(t, err)
	assert.Equal(t, "x", in.Reason)
}

// TestCall_RejectsMalformedAndEmptyReason makes the Call-side
// error paths explicit (Call → decodeDeclareBlockerInput) and
// also pins the documented "do NOT mark the invocation as
// declared on a malformed call" behaviour: the latch must stay
// off so the model can retry.
func TestCall_RejectsMalformedAndEmptyReason(t *testing.T) {
	_, inv, _ := newTestInvocation(t, "agent-A")
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := newDeclareBlockerTool("", "", nil)

	_, err := tl.Call(ctx, []byte(`not-json`))
	require.Error(t, err)
	assert.False(t, blockerDeclared(inv),
		"malformed call must not flip the blocker-declared latch")

	_, err = tl.Call(ctx, []byte(`{"reason":"   "}`))
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "reason is required",
		"whitespace-only reason must produce the documented \"reason is required\" error")
	assert.False(t, blockerDeclared(inv),
		"empty-reason rejection must not flip the latch either")
}

// TestCall_HappyPath_NoEnforcer_NoNotify defends the documented
// "tools constructed without an enforcer still record the
// declaration" semantics. The notification is a no-op (no
// observer to call), but the on-invocation latch must still flip
// — otherwise the enforcer-less unit tests would silently break
// any user that built the tool standalone for migration purposes.
func TestCall_HappyPath_NoEnforcer_NoNotify(t *testing.T) {
	_, inv, _ := newTestInvocation(t, "agent-A")
	ctx := agent.NewInvocationContext(context.Background(), inv)

	tl := newDeclareBlockerTool("", "", nil)
	out, err := tl.Call(ctx, []byte(`{"reason":"missing creds"}`))
	require.NoError(t, err)
	res, ok := out.(declareBlockerOutput)
	require.True(t, ok, "Call must return declareBlockerOutput on success, got %T", out)
	assert.True(t, res.OK)
	assert.Equal(t, "missing creds", res.Reason,
		"the output must echo the reason — the wire schema declares both ok and reason as required")
	assert.True(t, blockerDeclared(inv))
	assert.Equal(t, "missing creds", blockerReason(inv))
}
