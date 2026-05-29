//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeast

import "testing"

func TestParseConcurrency(t *testing.T) {
	tests := []struct {
		name string
		opts []ParseOption
		want int
	}{
		{
			name: "no options returns zero",
			opts: nil,
			want: 0,
		},
		{
			name: "single option returns value",
			opts: []ParseOption{WithParseConcurrency(4)},
			want: 4,
		},
		{
			name: "last option wins",
			opts: []ParseOption{WithParseConcurrency(2), WithParseConcurrency(8)},
			want: 8,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseConcurrency(tc.opts)
			if got != tc.want {
				t.Fatalf("ParseConcurrency() = %d, want %d", got, tc.want)
			}
		})
	}
}

type stubParser struct{}

func (stubParser) ParseDirectory(string, ...ParseOption) (*Result, error) { return nil, nil }

func TestDirectoryParserRegistry(t *testing.T) {
	tests := []struct {
		name     string
		register bool
		fileType string
		wantOK   bool
	}{
		{
			name:     "unregistered type returns false",
			register: false,
			fileType: "unknown_type_for_test",
			wantOK:   false,
		},
		{
			name:     "registered parser is returned",
			register: true,
			fileType: "test_lang",
			wantOK:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.register {
				RegisterDirectoryParser(tc.fileType, stubParser{})
			}
			p, ok := GetDirectoryParser(tc.fileType)
			if ok != tc.wantOK {
				t.Fatalf("GetDirectoryParser(%q) ok = %v, want %v", tc.fileType, ok, tc.wantOK)
			}
			if tc.wantOK && p == nil {
				t.Fatalf("GetDirectoryParser(%q) returned nil parser", tc.fileType)
			}
		})
	}
}
