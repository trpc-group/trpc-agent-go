//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultcodec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type customBashResult struct {
	ExitCode int
	Output   string
}

func TestCustom_TypedEncoder(t *testing.T) {
	c := Custom(func(_ context.Context, r customBashResult) (string, error) {
		return "exit=" + itoa(r.ExitCode) + " out=" + r.Output, nil
	})
	got, err := c.Encode(context.Background(), customBashResult{ExitCode: 0, Output: "done"})
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if got != "exit=0 out=done" {
		t.Errorf("unexpected output: %q", got)
	}
}

func TestCustom_TypeMismatchReturnsError(t *testing.T) {
	c := Custom(func(_ context.Context, r customBashResult) (string, error) {
		return "should not run", nil
	})
	_, err := c.Encode(context.Background(), "a string, not a struct")
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("error should describe the type mismatch: %v", err)
	}
}

func TestCustom_EncoderErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	c := Custom(func(_ context.Context, _ customBashResult) (string, error) {
		return "", sentinel
	})
	_, err := c.Encode(context.Background(), customBashResult{})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

func TestCustom_PanicConvertedToError(t *testing.T) {
	c := Custom(func(_ context.Context, _ customBashResult) (string, error) {
		panic("kaboom")
	})
	_, err := c.Encode(context.Background(), customBashResult{})
	if err == nil {
		t.Fatal("expected panic to be converted to an error")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error should mention panic: %v", err)
	}
}

func TestCustom_NilEncoderReturnsError(t *testing.T) {
	c := Custom[customBashResult](nil)
	if _, err := c.Encode(context.Background(), customBashResult{}); err == nil {
		t.Fatal("expected error for nil encoder")
	}
}

func TestCustom_InterfaceTypeParameterAcceptsNil(t *testing.T) {
	c := Custom(func(_ context.Context, v any) (string, error) {
		if v == nil {
			return "nil", nil
		}
		return "not-nil", nil
	})
	got, err := c.Encode(context.Background(), nil)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if got != "nil" {
		t.Errorf("got %q, want %q", got, "nil")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
