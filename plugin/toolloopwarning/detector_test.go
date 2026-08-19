//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestFingerprintRoundIgnoresToolIDsAndCanonicalizesArguments(t *testing.T) {
	responseA := toolResponse("search", []byte(`{"query":"x","limit":1}`))
	responseB := toolResponse("search", []byte(` { "limit": 1, "query": "x" } `))
	resultA := []model.Message{model.NewToolMessage("call-a", "search", "same")}
	resultB := []model.Message{model.NewToolMessage("call-b", "search", "same")}

	fingerprintA, ok := fingerprintRound(responseA, resultA)
	if !ok {
		t.Fatal("fingerprintRound returned false")
	}
	fingerprintB, ok := fingerprintRound(responseB, resultB)
	if !ok {
		t.Fatal("fingerprintRound returned false")
	}
	if fingerprintA != fingerprintB {
		t.Fatalf("fingerprints differ: %q != %q", fingerprintA, fingerprintB)
	}
}

func TestFingerprintRoundPreservesInvalidArgumentWhitespaceAndResultFields(t *testing.T) {
	response := toolResponse("search", []byte(`{"query":"x"}`))
	first := []model.Message{model.NewToolMessage("call-a", "search", "same")}
	second := []model.Message{model.NewToolMessage("call-b", "other", "same")}

	fingerprintFirst, ok := fingerprintRound(response, first)
	if !ok {
		t.Fatal("fingerprintRound returned false")
	}
	fingerprintSecond, ok := fingerprintRound(response, second)
	if !ok {
		t.Fatal("fingerprintRound returned false")
	}
	if fingerprintFirst == fingerprintSecond {
		t.Fatal("different model-visible result fields produced the same fingerprint")
	}

	invalidA := canonicalArguments([]byte(`{"query":"a  b",}`))
	invalidB := canonicalArguments([]byte(`{"query":"a b",}`))
	if invalidA == invalidB {
		t.Fatal("argument canonicalization collapsed meaningful whitespace")
	}
}

func TestDetectorWarnsOnceUntilRoundChanges(t *testing.T) {
	d := detector{}
	response := toolResponse("search", []byte(`{"query":"x"}`))
	results := []model.Message{model.NewToolMessage("call", "search", "same")}

	state := d.observe(detectorState{}, response, results, true)
	if state.Pending {
		t.Fatal("first round should not be pending")
	}
	state = d.observe(state, response, results, true)
	if !state.Pending {
		t.Fatal("repeated round should be pending")
	}
	state.Pending = false
	state = d.observe(state, response, results, true)
	if state.Pending {
		t.Fatal("same loop should not warn repeatedly")
	}

	changed := toolResponse("search", []byte(`{"query":"y"}`))
	state = d.observe(state, changed, results, true)
	state = d.observe(state, changed, results, true)
	if !state.Pending {
		t.Fatal("a new repeated round should warn again")
	}
}

func TestDetectorIncompleteRoundClearsState(t *testing.T) {
	d := detector{}
	response := toolResponse("search", []byte(`{"query":"x"}`))
	results := []model.Message{model.NewToolMessage("call", "search", "same")}

	state := d.observe(detectorState{}, response, results, true)
	state = d.observe(state, response, results, false)
	if state != (detectorState{}) {
		t.Fatalf("incomplete round left state: %+v", state)
	}
}

func toolResponse(name string, arguments []byte) *model.Response {
	return &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				ToolCalls: []model.ToolCall{{
					Function: model.FunctionDefinitionParam{
						Name:      name,
						Arguments: arguments,
					},
				}},
			},
		}},
	}
}
