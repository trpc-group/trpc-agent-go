//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package pgvector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestRankResultsByFocusedPassageFindsNestedCategory(t *testing.T) {
	t.Parallel()
	resource := focusedPassageEntry(
		"Assistant result: Recommended resources for learning front-end and " +
			"back-end development. Front-end: Code Academy and Web Camp. " +
			"Back-end: Server School and Data Course.",
	)
	advice := focusedPassageEntry(
		"Assistant result: Full-stack advice - (1) Master HTML and CSS. " +
			"(2) Learn a back-end language such as Ruby, Python, or PHP. " +
			"(3) Build complete projects.",
	)
	profile := focusedPassageEntry("Wants to become a full-stack developer.")

	got := rankResultsByFocusedPassage(
		"I wanted to follow up on our previous conversation about front-end "+
			"and back-end development. Can you remind me of the specific "+
			"back-end programming languages you recommended I learn?",
		[]*memory.Entry{resource, advice, profile},
	)

	require.Len(t, got, 1)
	assert.Same(t, advice, got[0])
}

func TestRankResultsByFocusedPassagePrefersRequestedRelation(t *testing.T) {
	t.Parallel()
	advice := focusedPassageEntry(
		"Assistant result: Full-stack advice - (1) Learn front-end basics. " +
			"(2) Learn a back-end language. (3) Build projects.",
	)
	resources := focusedPassageEntry(
		"Assistant result: Recommended front-end and back-end development " +
			"resources: Code Academy, Web Camp, and Server School.",
	)

	got := rankResultsByFocusedPassage(
		"Which front-end and back-end development resources did you recommend?",
		[]*memory.Entry{advice, resources},
	)

	require.NotEmpty(t, got)
	assert.Same(t, resources, got[0])
}

func TestRankResultsByFocusedPassageSkipsWeakFocus(t *testing.T) {
	t.Parallel()
	entry := focusedPassageEntry("Visited Kyoto during spring.")

	assert.Nil(t, rankResultsByFocusedPassage("Kyoto", []*memory.Entry{entry}))
	assert.Nil(t, rankResultsByFocusedPassage(
		"Can you remind me?", []*memory.Entry{entry},
	))
}

func TestFocusedQueryTermsUsesLastSubstantivePassage(t *testing.T) {
	t.Parallel()
	terms := focusedQueryTerms(
		"We discussed general web development. " +
			"Which back-end programming languages did you recommend?",
	)

	assert.Contains(t, terms, "backend")
	assert.Contains(t, terms, "program")
	assert.Contains(t, terms, "language")
	assert.NotContains(t, terms, "development")
}

func focusedPassageEntry(text string) *memory.Entry {
	return &memory.Entry{Memory: &memory.Memory{Memory: text}}
}
