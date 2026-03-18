//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package surface

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestIsSupportedType(t *testing.T) {
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeInstruction))
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeGlobalInstruction))
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeFewShot))
	assert.True(t, IsSupportedType(promptiter.SurfaceTypeModel))
	assert.False(t, IsSupportedType(promptiter.SurfaceType("unknown")))
}

func TestValidateValue(t *testing.T) {
	text := "instruction"

	assert.NoError(t, ValidateValue(
		promptiter.SurfaceTypeInstruction,
		promptiter.SurfaceValue{Text: &text},
	))
	assert.NoError(t, ValidateValue(
		promptiter.SurfaceTypeFewShot,
		promptiter.SurfaceValue{Message: []promptiter.Messages{}},
	))
	assert.NoError(t, ValidateValue(
		promptiter.SurfaceTypeModel,
		promptiter.SurfaceValue{Model: &promptiter.Model{Provider: "p", Name: "m"}},
	))

	assert.Error(t, ValidateValue(
		promptiter.SurfaceTypeInstruction,
		promptiter.SurfaceValue{},
	))
	assert.Error(t, ValidateValue(
		promptiter.SurfaceTypeFewShot,
		promptiter.SurfaceValue{Text: &text},
	))
	assert.Error(t, ValidateValue(
		promptiter.SurfaceTypeModel,
		promptiter.SurfaceValue{Model: &promptiter.Model{}},
	))
}

func TestBuildIndex(t *testing.T) {
	text := "instruction"

	index, err := BuildIndex([]promptiter.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      promptiter.SurfaceTypeInstruction,
			Value: promptiter.SurfaceValue{
				Text: &text,
			},
		},
	})

	assert.NoError(t, err)
	assert.Len(t, index, 1)
	assert.Equal(t, "surf_1", index["surf_1"].SurfaceID)
}

func TestBuildIndexRejectsInvalidSurface(t *testing.T) {
	text := "instruction"

	_, err := BuildIndex([]promptiter.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      promptiter.SurfaceTypeInstruction,
			Value: promptiter.SurfaceValue{
				Text: &text,
			},
		},
		{
			SurfaceID: "surf_1",
			NodeID:    "node_2",
			Type:      promptiter.SurfaceTypeInstruction,
			Value: promptiter.SurfaceValue{
				Text: &text,
			},
		},
	})

	assert.Error(t, err)
}

func TestSanitizeValue(t *testing.T) {
	text := "instruction"
	emptyText := ""

	sanitizedInstruction, err := SanitizeValue(
		promptiter.SurfaceTypeInstruction,
		promptiter.SurfaceValue{
			Text:  &text,
			Model: &promptiter.Model{},
		},
	)
	assert.NoError(t, err)
	assert.Equal(t, &text, sanitizedInstruction.Text)
	assert.Nil(t, sanitizedInstruction.Model)

	sanitizedFewShot, err := SanitizeValue(
		promptiter.SurfaceTypeFewShot,
		promptiter.SurfaceValue{
			Text:    &emptyText,
			Message: []promptiter.Messages{},
			Model:   &promptiter.Model{},
		},
	)
	assert.NoError(t, err)
	assert.Nil(t, sanitizedFewShot.Text)
	assert.Nil(t, sanitizedFewShot.Model)
	assert.NotNil(t, sanitizedFewShot.Message)

	sanitizedModel, err := SanitizeValue(
		promptiter.SurfaceTypeModel,
		promptiter.SurfaceValue{
			Text:  &emptyText,
			Model: &promptiter.Model{Provider: "p", Name: "m"},
		},
	)
	assert.NoError(t, err)
	assert.Nil(t, sanitizedModel.Text)
	assert.Equal(t, &promptiter.Model{Provider: "p", Name: "m"}, sanitizedModel.Model)
}

func TestSanitizeValueRejectsInvalidInput(t *testing.T) {
	text := "instruction"

	_, err := SanitizeValue(
		promptiter.SurfaceTypeInstruction,
		promptiter.SurfaceValue{},
	)
	assert.Error(t, err)

	_, err = SanitizeValue(
		promptiter.SurfaceTypeFewShot,
		promptiter.SurfaceValue{Text: &text},
	)
	assert.Error(t, err)

	_, err = SanitizeValue(
		promptiter.SurfaceTypeModel,
		promptiter.SurfaceValue{Model: &promptiter.Model{}},
	)
	assert.Error(t, err)
}
