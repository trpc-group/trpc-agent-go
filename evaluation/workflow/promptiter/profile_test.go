//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import (
	"testing"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type typedSchemaDefault struct {
	Regions []string          `json:"regions"`
	Labels  map[string]string `json:"labels"`
}

func TestCloneProfileDeepCopiesSurfaceValues(t *testing.T) {
	text := "original"
	defaultHeaders := map[string]string{"region": "west"}
	enumValues := []string{"standard", "priority"}
	typedDefault := &typedSchemaDefault{
		Regions: []string{"west"},
		Labels:  map[string]string{"tier": "standard"},
	}
	source := &Profile{
		StructureID: "structure",
		Overrides: []SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value: astructure.SurfaceValue{
				Text:    &text,
				Model:   &astructure.ModelRef{Headers: map[string]string{"authorization": "secret"}},
				FewShot: []astructure.FewShotExample{{Messages: []astructure.FewShotMessage{{Content: "example"}}}},
				Tools: []astructure.ToolRef{{
					ID: "lookup",
					InputSchema: &tool.Schema{
						Type: "object",
						Default: map[string]any{
							"headers": defaultHeaders,
							"typed":   typedDefault,
						},
						Enum: []any{enumValues},
					},
				}},
			},
		}},
	}
	cloned := CloneProfile(source)
	*source.Overrides[0].Value.Text = "changed"
	source.Overrides[0].Value.Model.Headers["authorization"] = "changed"
	source.Overrides[0].Value.FewShot[0].Messages[0].Content = "changed"
	defaultHeaders["region"] = "east"
	enumValues[0] = "mutated"
	typedDefault.Regions[0] = "east"
	typedDefault.Labels["tier"] = "priority"
	if got := *cloned.Overrides[0].Value.Text; got != "original" {
		t.Fatalf("text = %q", got)
	}
	if got := cloned.Overrides[0].Value.Model.Headers["authorization"]; got != "secret" {
		t.Fatalf("header = %q", got)
	}
	if got := cloned.Overrides[0].Value.FewShot[0].Messages[0].Content; got != "example" {
		t.Fatalf("few-shot content = %q", got)
	}
	clonedDefault, ok := cloned.Overrides[0].Value.Tools[0].InputSchema.Default.(map[string]any)
	if !ok {
		t.Fatalf("typed schema default was not copied: %#v", cloned.Overrides[0].Value.Tools[0].InputSchema.Default)
	}
	clonedHeaders, ok := clonedDefault["headers"].(map[string]string)
	if !ok || clonedHeaders["region"] != "west" {
		t.Fatalf("typed schema map was not copied: %#v", clonedDefault["headers"])
	}
	clonedTyped, ok := clonedDefault["typed"].(*typedSchemaDefault)
	if !ok || clonedTyped.Regions[0] != "west" || clonedTyped.Labels["tier"] != "standard" {
		t.Fatalf("schema pointer/struct was not copied: %#v", clonedDefault["typed"])
	}
	clonedEnum, ok := cloned.Overrides[0].Value.Tools[0].InputSchema.Enum[0].([]string)
	if !ok || clonedEnum[0] != "standard" {
		t.Fatalf("typed schema enum was not copied: %#v", cloned.Overrides[0].Value.Tools[0].InputSchema.Enum)
	}
}
