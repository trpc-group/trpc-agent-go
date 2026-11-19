//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestChatInputTermination(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "EOF"},
		{name: "input exceeds scanner limit", input: strings.Repeat("x", 128*1024), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "input")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString(tt.input); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			stdin := os.Stdin
			os.Stdin = f
			defer func() { os.Stdin = stdin }()

			err = chat(context.Background(), nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("chat() error = %v, want error: %t", err, tt.wantErr)
			}
		})
	}
}

func TestCalculatorExpressions(t *testing.T) {
	for _, tt := range []struct {
		expression string
		want       string
	}{
		{"1e-3*2", "0.002"},
		{"1E+3/2", "500"},
		{"2+1e-3", "2.001"},
		{"-2*-3", "6"},
		{"7-2", "5"},
	} {
		t.Run(tt.expression, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"expression": tt.expression})
			if err != nil {
				t.Fatal(err)
			}
			got, err := calculatorTool().Call(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			result, ok := got.(struct {
				Result string `json:"result"`
			})
			if !ok || result.Result != tt.want {
				t.Fatalf("calculator result = %v, want %s", got, tt.want)
			}
		})
	}
}

func TestCalculatorRejectsInvalidOperands(t *testing.T) {
	for _, expression := range []string{"12abc+3", "2+3abc", "2+3*4"} {
		t.Run(expression, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"expression": expression})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := calculatorTool().Call(context.Background(), args); err == nil {
				t.Fatal("calculator accepted an invalid operand")
			}
		})
	}
}
