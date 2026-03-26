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
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

func TestIsSupportedType(t *testing.T) {
	assert.True(t, IsSupportedType(astructure.SurfaceTypeInstruction))
	assert.True(t, IsSupportedType(astructure.SurfaceTypeGlobalInstruction))
	assert.True(t, IsSupportedType(astructure.SurfaceTypeFewShot))
	assert.True(t, IsSupportedType(astructure.SurfaceTypeModel))
	assert.False(t, IsSupportedType(astructure.SurfaceType("unknown")))
}

func TestValidateValue(t *testing.T) {
	text := "instruction"

	assert.NoError(t, ValidateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{Text: &text},
	))
	assert.NoError(t, ValidateValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{FewShot: []astructure.FewShotExample{}},
	))
	assert.NoError(t, ValidateValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{Model: &astructure.ModelRef{Name: "m"}},
	))

	assert.Error(t, ValidateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{},
	))
	assert.Error(t, ValidateValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{Text: &text},
	))
	assert.Error(t, ValidateValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{Model: &astructure.ModelRef{}},
	))
}

func TestBuildIndex(t *testing.T) {
	text := "instruction"

	index, err := BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
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

	_, err := BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &text,
			},
		},
		{
			SurfaceID: "surf_1",
			NodeID:    "node_2",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
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
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:  &text,
			Model: &astructure.ModelRef{},
		},
	)
	assert.NoError(t, err)
	assert.Equal(t, &text, sanitizedInstruction.Text)
	assert.Nil(t, sanitizedInstruction.Model)

	sanitizedFewShot, err := SanitizeValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{
			Text:    &emptyText,
			FewShot: []astructure.FewShotExample{},
			Model:   &astructure.ModelRef{},
		},
	)
	assert.NoError(t, err)
	assert.Nil(t, sanitizedFewShot.Text)
	assert.Nil(t, sanitizedFewShot.Model)
	assert.NotNil(t, sanitizedFewShot.FewShot)

	sanitizedModel, err := SanitizeValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{
			Text:  &emptyText,
			Model: &astructure.ModelRef{Name: "m"},
		},
	)
	assert.NoError(t, err)
	assert.Nil(t, sanitizedModel.Text)
	assert.Equal(t, &astructure.ModelRef{Name: "m"}, sanitizedModel.Model)
}

func TestSanitizeValueRejectsInvalidInput(t *testing.T) {
	text := "instruction"

	_, err := SanitizeValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{},
	)
	assert.Error(t, err)

	_, err = SanitizeValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{Text: &text},
	)
	assert.Error(t, err)

	_, err = SanitizeValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{Model: &astructure.ModelRef{}},
	)
	assert.Error(t, err)
}
