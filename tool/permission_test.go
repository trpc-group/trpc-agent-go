//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

const (
	testToolName      = "test_tool"
	testReason        = "requires approval"
	testMaxResultSize = 1024
	testInvalidAction = "bad"
)

type metadataTool struct {
	metadata ToolMetadata
}

func (m *metadataTool) Declaration() *Declaration {
	return &Declaration{Name: testToolName}
}

func (m *metadataTool) ToolMetadata() ToolMetadata {
	return m.metadata
}

type concurrencyTool struct {
	safe bool
}

func (c *concurrencyTool) Declaration() *Declaration {
	return &Declaration{Name: testToolName}
}

func (c *concurrencyTool) IsConcurrencySafe() bool {
	return c.safe
}

type deferredTool struct {
	deferTool bool
}

type resultSanitizer struct{}

func (resultSanitizer) SanitizeToolResult(
	_ context.Context,
	args *AfterToolArgs,
) (any, error) {
	return args.Result, nil
}

type suffixResultSanitizer string

func (s suffixResultSanitizer) SanitizeToolResult(
	_ context.Context,
	args *AfterToolArgs,
) (any, error) {
	return args.Result.(string) + string(s), nil
}

type suffixErrorSanitizer string

func (s suffixErrorSanitizer) SanitizeToolError(
	_ context.Context,
	args *AfterToolArgs,
) (error, error) {
	return fmt.Errorf("%s%s", args.Error, s), nil
}

type resultSanitizerFunc func(context.Context, *AfterToolArgs) (any, error)

func (f resultSanitizerFunc) SanitizeToolResult(
	ctx context.Context,
	args *AfterToolArgs,
) (any, error) {
	return f(ctx, args)
}

type errorSanitizerFunc func(context.Context, *AfterToolArgs) (error, error)

func (f errorSanitizerFunc) SanitizeToolError(
	ctx context.Context,
	args *AfterToolArgs,
) (error, error) {
	return f(ctx, args)
}

func (d *deferredTool) Declaration() *Declaration {
	return &Declaration{Name: testToolName}
}

func (d *deferredTool) ShouldDefer(context.Context) bool {
	return d.deferTool
}

func TestMetadataOf_DefaultsToZeroValue(t *testing.T) {
	meta := MetadataOf(newMockTool(testToolName))
	if meta != (ToolMetadata{}) {
		t.Fatalf("expected zero metadata, got %+v", meta)
	}
}

func TestMetadataHelpers_NilTool(t *testing.T) {
	if got := MetadataOf(nil); got != (ToolMetadata{}) {
		t.Fatalf("expected nil tool metadata to be zero, got %+v", got)
	}
	if ShouldDefer(context.Background(), nil) {
		t.Fatalf("expected nil tool not to ask for deferral")
	}
}

func TestMetadataOf_UsesProvider(t *testing.T) {
	want := ToolMetadata{
		ReadOnly:        true,
		Destructive:     false,
		ConcurrencySafe: true,
		SearchOrRead:    true,
		OpenWorld:       true,
		MaxResultSize:   testMaxResultSize,
	}
	got := MetadataOf(&metadataTool{metadata: want})
	if got != want {
		t.Fatalf("expected metadata %+v, got %+v", want, got)
	}
}

func TestMetadataOf_UsesConcurrencyAwareFallback(t *testing.T) {
	got := MetadataOf(&concurrencyTool{safe: true})
	if !got.ConcurrencySafe {
		t.Fatalf("expected concurrency-aware tool to be marked safe")
	}
}

func TestShouldDefer(t *testing.T) {
	if !ShouldDefer(context.Background(), &deferredTool{deferTool: true}) {
		t.Fatalf("expected deferred tool to ask for deferral")
	}
	if ShouldDefer(context.Background(), newMockTool(testToolName)) {
		t.Fatalf("expected plain tool not to ask for deferral")
	}
}

func TestNormalizePermissionDecision(t *testing.T) {
	decision, err := NormalizePermissionDecision(PermissionDecision{})
	if err != nil {
		t.Fatalf("zero decision should be valid: %v", err)
	}
	if decision.Action != PermissionActionAllow {
		t.Fatalf("expected zero decision to normalize to allow, got %q", decision.Action)
	}

	_, err = NormalizePermissionDecision(PermissionDecision{Action: PermissionAction(testInvalidAction)})
	if err == nil {
		t.Fatalf("expected invalid permission action to fail")
	}
}

func TestMostRestrictivePermissionDecision(t *testing.T) {
	decision, err := MostRestrictivePermissionDecision(
		AllowPermission(), AskPermission("ask"), DenyPermission("deny"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != PermissionActionDeny || decision.Reason != "deny" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestPermissionPolicyFunc_NilAllows(t *testing.T) {
	var fn PermissionPolicyFunc
	decision, err := fn.CheckToolPermission(context.Background(), &PermissionRequest{})
	if err != nil {
		t.Fatalf("nil policy func returned error: %v", err)
	}
	if decision.Action != PermissionActionAllow {
		t.Fatalf("expected nil policy func to allow, got %q", decision.Action)
	}
}

func TestPermissionResultFor(t *testing.T) {
	denied := PermissionResultFor(testToolName, DenyPermission(testReason))
	if denied.Status != PermissionResultStatusDenied ||
		denied.Tool != testToolName ||
		denied.Reason != testReason {
		t.Fatalf("unexpected deny result: %+v", denied)
	}

	ask := PermissionResultFor(testToolName, AskPermission(testReason))
	if ask.Status != PermissionResultStatusApprovalRequired ||
		ask.Tool != testToolName ||
		ask.Reason != testReason {
		t.Fatalf("unexpected ask result: %+v", ask)
	}
}

func TestToolResultSanitizerContract(t *testing.T) {
	var sanitizer ToolResultSanitizer = resultSanitizer{}
	want := map[string]any{"safe": true}
	got, err := sanitizer.SanitizeToolResult(
		context.Background(),
		&AfterToolArgs{Result: want},
	)
	if err != nil {
		t.Fatalf("sanitize result: %v", err)
	}
	if gotMap, ok := got.(map[string]any); !ok || gotMap["safe"] != true {
		t.Fatalf("unexpected sanitized result: %#v", got)
	}
}

func TestComposeToolSanitizersPreservesOrder(t *testing.T) {
	resultSanitizer := ComposeToolResultSanitizers(
		suffixResultSanitizer("-first"), suffixResultSanitizer("-second"),
	)
	result, err := resultSanitizer.SanitizeToolResult(
		context.Background(), &AfterToolArgs{Result: "value"},
	)
	if err != nil || result != "value-first-second" {
		t.Fatalf("composed result = %#v, %v", result, err)
	}
	errorSanitizer := ComposeToolErrorSanitizers(
		suffixErrorSanitizer("-first"), suffixErrorSanitizer("-second"),
	)
	safeErr, err := errorSanitizer.SanitizeToolError(
		context.Background(), &AfterToolArgs{Error: errors.New("value")},
	)
	if err != nil || safeErr.Error() != "value-first-second" {
		t.Fatalf("composed error = %v, %v", safeErr, err)
	}
}

func TestComposeToolResultSanitizersFailsClosed(t *testing.T) {
	wantErr := errors.New("result sanitizer unavailable")
	var calls []string
	sanitizer := ComposeToolResultSanitizers(
		nil,
		resultSanitizerFunc(func(_ context.Context, args *AfterToolArgs) (any, error) {
			calls = append(calls, "first")
			if args.Result != "raw" {
				t.Fatalf("first result = %#v", args.Result)
			}
			return "first-safe", nil
		}),
		resultSanitizerFunc(func(_ context.Context, args *AfterToolArgs) (any, error) {
			calls = append(calls, "second")
			if args.Result != "first-safe" {
				t.Fatalf("second result = %#v", args.Result)
			}
			return "must-not-escape", wantErr
		}),
		resultSanitizerFunc(func(_ context.Context, _ *AfterToolArgs) (any, error) {
			calls = append(calls, "third")
			return "unexpected", nil
		}),
	)
	args := &AfterToolArgs{ToolName: "sensitive", Result: "raw"}
	got, err := sanitizer.SanitizeToolResult(context.Background(), args)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("failed chain returned %#v, want nil", got)
	}
	if fmt.Sprint(calls) != "[first second]" {
		t.Fatalf("calls = %v", calls)
	}
	if args.Result != "raw" {
		t.Fatalf("composition mutated caller args: %#v", args.Result)
	}
}

func TestComposeToolErrorSanitizersFailsClosed(t *testing.T) {
	rawErr := errors.New("password=raw")
	firstSafeErr := errors.New("password=first-safe")
	wantErr := errors.New("error sanitizer unavailable")
	var calls []string
	sanitizer := ComposeToolErrorSanitizers(
		nil,
		errorSanitizerFunc(func(_ context.Context, args *AfterToolArgs) (error, error) {
			calls = append(calls, "first")
			if !errors.Is(args.Error, rawErr) {
				t.Fatalf("first error = %v", args.Error)
			}
			return firstSafeErr, nil
		}),
		errorSanitizerFunc(func(_ context.Context, args *AfterToolArgs) (error, error) {
			calls = append(calls, "second")
			if !errors.Is(args.Error, firstSafeErr) {
				t.Fatalf("second error = %v", args.Error)
			}
			return errors.New("must-not-escape"), wantErr
		}),
		errorSanitizerFunc(func(_ context.Context, _ *AfterToolArgs) (error, error) {
			calls = append(calls, "third")
			return errors.New("unexpected"), nil
		}),
	)
	args := &AfterToolArgs{ToolName: "sensitive", Error: rawErr}
	got, err := sanitizer.SanitizeToolError(context.Background(), args)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("failed chain returned %v, want nil", got)
	}
	if fmt.Sprint(calls) != "[first second]" {
		t.Fatalf("calls = %v", calls)
	}
	if !errors.Is(args.Error, rawErr) {
		t.Fatalf("composition mutated caller args: %v", args.Error)
	}
}

func TestComposeToolSanitizersEmptyAndNilArguments(t *testing.T) {
	if ComposeToolResultSanitizers(nil) != nil {
		t.Fatal("empty result sanitizer chain must be nil")
	}
	if ComposeToolErrorSanitizers(nil) != nil {
		t.Fatal("empty error sanitizer chain must be nil")
	}
	_, err := ComposeToolResultSanitizers(
		resultSanitizer{},
	).SanitizeToolResult(context.Background(), nil)
	if err == nil {
		t.Fatal("nil result sanitizer arguments must fail closed")
	}
	_, err = ComposeToolErrorSanitizers(
		suffixErrorSanitizer("-one"),
	).SanitizeToolError(context.Background(), nil)
	if err == nil {
		t.Fatal("nil error sanitizer arguments must fail closed")
	}
}

func TestComposeSingleToolSanitizerIsolatesArguments(t *testing.T) {
	args := &AfterToolArgs{ToolName: "original", Result: "raw", Error: errors.New("raw")}
	resultSanitizer := ComposeToolResultSanitizers(resultSanitizerFunc(
		func(_ context.Context, next *AfterToolArgs) (any, error) {
			next.ToolName = "mutated"
			return "safe", nil
		},
	))
	if got, err := resultSanitizer.SanitizeToolResult(context.Background(), args); err != nil || got != "safe" {
		t.Fatalf("single result sanitizer = %#v, %v", got, err)
	}
	errorSanitizer := ComposeToolErrorSanitizers(errorSanitizerFunc(
		func(_ context.Context, next *AfterToolArgs) (error, error) {
			next.ToolName = "mutated"
			return errors.New("safe"), nil
		},
	))
	if got, err := errorSanitizer.SanitizeToolError(context.Background(), args); err != nil || got.Error() != "safe" {
		t.Fatalf("single error sanitizer = %v, %v", got, err)
	}
	if args.ToolName != "original" {
		t.Fatalf("single sanitizer mutated caller arguments: %+v", args)
	}
}
