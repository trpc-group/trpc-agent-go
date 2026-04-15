//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package profile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestCloneNilProfile(t *testing.T) {
	assert.Nil(t, Clone(nil))
}

func TestClonePreservesNilOverrides(t *testing.T) {
	profile := &promptiter.Profile{StructureID: "structure_1"}
	cloned := Clone(profile)
	assert.NotNil(t, cloned)
	if cloned == nil {
		return
	}
	assert.Equal(t, "structure_1", cloned.StructureID)
	assert.Nil(t, cloned.Overrides)
}

func TestCloneDeepCopiesOverrides(t *testing.T) {
	text := "prompt"
	profile := &promptiter.Profile{
		StructureID: "structure_1",
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: "candidate#instruction",
				Value: astructure.SurfaceValue{
					Text: &text,
					FewShot: []astructure.FewShotExample{
						{
							Messages: []astructure.FewShotMessage{
								{
									Role:    "user",
									Content: "question",
								},
							},
						},
					},
					Model: &astructure.ModelRef{
						Provider: "provider",
						Name:     "model",
						Headers: map[string]string{
							"X-Test": "value",
						},
					},
				},
			},
		},
	}
	cloned := Clone(profile)
	assert.NotNil(t, cloned)
	if cloned == nil {
		return
	}
	assert.Equal(t, profile.StructureID, cloned.StructureID)
	if assert.Len(t, cloned.Overrides, 1) {
		assert.Equal(t, profile.Overrides[0].SurfaceID, cloned.Overrides[0].SurfaceID)
		if assert.NotNil(t, cloned.Overrides[0].Value.Text) {
			assert.Equal(t, "prompt", *cloned.Overrides[0].Value.Text)
			assert.NotSame(t, profile.Overrides[0].Value.Text, cloned.Overrides[0].Value.Text)
			*cloned.Overrides[0].Value.Text = "mutated"
			assert.Equal(t, "prompt", *profile.Overrides[0].Value.Text)
		}
		if assert.Len(t, cloned.Overrides[0].Value.FewShot, 1) {
			cloned.Overrides[0].Value.FewShot[0].Messages[0].Content = "mutated"
			assert.Equal(t, "question", profile.Overrides[0].Value.FewShot[0].Messages[0].Content)
		}
		if assert.NotNil(t, cloned.Overrides[0].Value.Model) {
			assert.NotSame(t, profile.Overrides[0].Value.Model, cloned.Overrides[0].Value.Model)
			cloned.Overrides[0].Value.Model.Headers["X-Test"] = "mutated"
			assert.Equal(t, "value", profile.Overrides[0].Value.Model.Headers["X-Test"])
		}
	}
}
