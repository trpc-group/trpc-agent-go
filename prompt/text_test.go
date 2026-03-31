//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package prompt

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTextRender_ReplacesKnownVarsAndKeepsUnknown(t *testing.T) {
	tpl := Text{
		Template: "hello {name} from {city} and {user:name}",
	}

	rendered := tpl.Render(Vars{
		"name": "alice",
	})

	require.Equal(t, "hello alice from {city} and {user:name}", rendered)
}

func TestTextRender_PreservesUnmatchedComplexBraceFormsInTemplate(t *testing.T) {
	tpl := Text{
		Template: "summary {conversation_text} state {user:name}",
	}

	rendered := tpl.Render(Vars{
		"conversation_text": "done",
	})

	require.Equal(t, "summary done state {user:name}", rendered)
}

func TestTextRender_SubstitutedValuesMayContainSimpleBraces(t *testing.T) {
	tpl := Text{
		Template: "msg: {conversation_text}",
	}

	rendered := tpl.Render(Vars{
		"conversation_text": `user said {name} and {city}`,
	})

	require.Equal(t, "msg: user said {name} and {city}", rendered)
}

func TestTextValidateRequired(t *testing.T) {
	tpl := Text{
		Template: "summary {conversation_text} limit {max_summary_words}",
	}

	require.NoError(t, tpl.ValidateRequired("conversation_text"))
	require.NoError(t, tpl.ValidateRequired("conversation_text", "max_summary_words"))

	err := tpl.ValidateRequired("conversation_text", "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "{missing}")
}
