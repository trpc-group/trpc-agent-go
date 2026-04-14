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

type mapResolver map[string]string

func (r mapResolver) Resolve(ref Ref) (string, bool, error) {
	value, ok := r[ref.Name]
	return value, ok, nil
}

func TestTextRender_ReplacesKnownVarsAndKeepsUnknown(t *testing.T) {
	tpl := Text{
		Template: "hello {name} from {city} and {user:name}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"name": "alice",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "hello alice from {city} and {user:name}", rendered)
}

func TestTextRender_UsesResolverForBareAndNamespacedPlaceholders(t *testing.T) {
	tpl := Text{
		Template: "summary {conversation_text} state {user:name} topic {research_topics}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"conversation_text": "done",
		},
		Resolver: mapResolver{
			"user:name":       "alice",
			"research_topics": "ai",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "summary done state alice topic ai", rendered)
}

func TestTextRender_VarsTakePrecedenceOverResolverFallback(t *testing.T) {
	tpl := Text{
		Template: "name {name}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"name": "alice",
		},
		Resolver: mapResolver{
			"name": "bob",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "name alice", rendered)
}

func TestTextRender_VarsMatchRawExtractedPlaceholderName(t *testing.T) {
	tpl := Text{
		Template: "user {user:name}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"user:name": "alice",
		},
		Resolver: mapResolver{
			"user:name": "bob",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "user alice", rendered)
}

func TestTextRender_OptionalMissingCollapsesToEmpty(t *testing.T) {
	tpl := Text{
		Template: "summary {conversation_text} {user:name?} {app:banner?}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"conversation_text": "done",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "summary done  ", rendered)
}

func TestTextRender_SubstitutedValuesMayContainSimpleBraces(t *testing.T) {
	tpl := Text{
		Template: "msg: {conversation_text}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"conversation_text": `user said {name} and {city}`,
		},
	})

	require.NoError(t, err)
	require.Equal(t, "msg: user said {name} and {city}", rendered)
}

func TestTextRender_SingleBraceWhitespaceStaysLiteral(t *testing.T) {
	tpl := Text{
		Template: "hello { name } from { user:name ? }",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"name": "alice",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "hello { name } from { user:name ? }", rendered)
}

func TestTextRender_SingleBraceLeavesDoubleCurlyUntouched(t *testing.T) {
	tpl := Text{
		Template: "hello {{ name }} from {{user:city?}}",
		Syntax:   SyntaxSingleBrace,
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"name": "alice",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "hello {{ name }} from {{user:city?}}", rendered)
}

func TestTextRender_DefaultMixedRecognizesBothDelimiters(t *testing.T) {
	tpl := Text{
		Template: "hello {name} from {{city}}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"name": "alice",
			"city": "paris",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "hello alice from paris", rendered)
}

func TestTextRender_DefaultMixedPreservesUnresolvedDoubleBrace(t *testing.T) {
	tpl := Text{
		Template: "hello {{name}} from {{city}}",
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"name": "alice",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "hello alice from {{city}}", rendered)
}

func TestTextRender_StrictUnknownPreservesDoubleBraceInOutputAndError(t *testing.T) {
	tpl := Text{
		Template: "hello {{name}} from {{city}}",
	}

	rendered, err := tpl.Render(
		RenderEnv{
			Vars: Vars{
				"name": "alice",
			},
		},
		WithUnknownBehavior(ErrorOnUnknown),
	)

	require.Equal(t, "hello alice from {{city}}", rendered)
	require.Error(t, err)
	require.Contains(t, err.Error(), "{{city}}")
}

func TestTextRender_DoubleBraceSyntax(t *testing.T) {
	tpl := Text{
		Template: "hello {{ name }} from {{ city }} and {{ user:name? }}",
		Syntax:   SyntaxDoubleBrace,
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"name": "alice",
			"city": "paris",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "hello alice from paris and ", rendered)
}

func TestTextRender_DoubleBraceUsesSameNameFormatAsSingleBrace(t *testing.T) {
	tpl := Text{
		Template: "file {{artifact.file.txt}} slug {{invalid-name}}",
		Syntax:   SyntaxDoubleBrace,
	}

	rendered, err := tpl.Render(RenderEnv{
		Vars: Vars{
			"artifact.file.txt": "report.md",
			"invalid-name":      "release-1",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "file report.md slug release-1", rendered)
}

func TestTextValidateRequired_SingleBraceIgnoresDoubleCurly(t *testing.T) {
	tpl := Text{
		Template: "hello {{name}}",
		Syntax:   SyntaxSingleBrace,
	}

	err := tpl.ValidateRequired("name")
	require.Error(t, err)
	require.Contains(t, err.Error(), "{name}")
}

func TestTextValidateRequired_DefaultMixedRecognizesDoubleCurly(t *testing.T) {
	tpl := Text{
		Template: "hello {{name}}",
	}

	require.NoError(t, tpl.ValidateRequired("name"))
}

func TestTextValidateRequired_DoubleBraceSyntax(t *testing.T) {
	tpl := Text{
		Template: "hello {{name}}",
		Syntax:   SyntaxDoubleBrace,
	}

	require.NoError(t, tpl.ValidateRequired("name"))
}

func TestTextRender_StrictUnknownReturnsError(t *testing.T) {
	tpl := Text{
		Template: "hello {name} from {city}",
	}

	rendered, err := tpl.Render(
		RenderEnv{
			Vars: Vars{
				"name": "alice",
			},
		},
		WithUnknownBehavior(ErrorOnUnknown),
	)

	require.Equal(t, "hello alice from {city}", rendered)
	require.Error(t, err)
	require.Contains(t, err.Error(), "{city}")
}

func TestTextRender_LeavesOpaquePlaceholdersUntouched(t *testing.T) {
	tpl := Text{
		Template: "Content {artifact.file.txt} optional {artifact.file.txt?}",
	}

	rendered, err := tpl.Render(RenderEnv{})

	require.NoError(t, err)
	require.Equal(t, "Content {artifact.file.txt} optional ", rendered)
}

func TestTextValidateRequired(t *testing.T) {
	tpl := Text{
		Template: "summary {conversation_text} limit {max_summary_words} state {user:name?}",
	}

	require.NoError(t, tpl.ValidateRequired("conversation_text"))
	require.NoError(t, tpl.ValidateRequired("conversation_text", "max_summary_words"))
	require.NoError(t, tpl.ValidateRequired("user:name"))

	err := tpl.ValidateRequired("conversation_text", "missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "{missing}")
}
