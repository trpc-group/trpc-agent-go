//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestDiffFilesPreservesNilSlicesAndDeepCopies(t *testing.T) {
	if DiffFiles(nil) != nil {
		t.Fatal("DiffFiles(nil) returned non-nil slice")
	}
	in := []review.DiffFile{{
		OldPath: "pkg/token=supersecretvalue.go",
		NewPath: "pkg/config.go",
		Hunks:   nil,
	}, {
		NewPath: "pkg/key.go",
		Hunks: []review.DiffHunk{{
			Lines: nil,
		}, {
			Lines: []review.DiffLine{{
				Kind:    "add",
				NewLine: 1,
				Content: "token=anothersecret",
			}},
		}},
	}}

	out := DiffFiles(in)
	if out[0].Hunks != nil {
		t.Fatalf("out[0].Hunks = %#v, want nil", out[0].Hunks)
	}
	if out[1].Hunks[0].Lines != nil {
		t.Fatalf("out[1].Hunks[0].Lines = %#v, want nil", out[1].Hunks[0].Lines)
	}
	if strings.Contains(out[0].OldPath, "supersecretvalue") || strings.Contains(out[1].Hunks[1].Lines[0].Content, "anothersecret") {
		t.Fatalf("DiffFiles leaked secret-like content: %#v", out)
	}

	in[1].Hunks[1].Lines[0].Content = "mutated"
	if out[1].Hunks[1].Lines[0].Content == "mutated" {
		t.Fatal("DiffFiles output shares line storage with input")
	}
	out[1].Hunks[1].Lines[0].Content = "changed"
	if in[1].Hunks[1].Lines[0].Content == "changed" {
		t.Fatal("DiffFiles input shares line storage with output")
	}
}

func TestDiffFilesRedactsMultilinePrivateKeysAcrossLines(t *testing.T) {
	in := []review.DiffFile{{
		NewPath: "pkg/key.go",
		Hunks: []review.DiffHunk{{
			Lines: []review.DiffLine{
				{Kind: "add", NewLine: 1, Content: "const key = \"-----BEGIN PRIVATE KEY-----"},
				{Kind: "add", NewLine: 2, Content: "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC"},
				{Kind: "add", NewLine: 3, Content: "-----END PRIVATE KEY-----\""},
			},
		}},
	}}

	out := DiffFiles(in)
	for _, line := range out[0].Hunks[0].Lines {
		if strings.Contains(line.Content, "PRIVATE KEY") || strings.Contains(line.Content, "MIIEvQ") {
			t.Fatalf("line leaked private key material: %#v", line)
		}
	}
}
