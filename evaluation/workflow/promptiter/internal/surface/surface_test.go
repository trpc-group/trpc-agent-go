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
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceValue{Text: &text},
	))
	assert.NoError(t, ValidateValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{FewShot: []astructure.FewShotExample{}},
	))
	assert.NoError(t, ValidateValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{Model: &astructure.ModelRef{
			Provider: "openai",
			Name:     "m",
			Headers:  map[string]string{"X-Test": "1"},
		}},
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

	assert.ErrorContains(t, err, `duplicate surface id "surf_1"`)
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
	sanitizedGlobalInstruction, err := SanitizeValue(
		astructure.SurfaceTypeGlobalInstruction,
		astructure.SurfaceValue{
			Text:  &text,
			Model: &astructure.ModelRef{},
		},
	)
	assert.NoError(t, err)
	assert.Equal(t, &text, sanitizedGlobalInstruction.Text)
	assert.Nil(t, sanitizedGlobalInstruction.Model)

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

	modelRef := &astructure.ModelRef{
		Provider: "openai",
		Name:     "m",
		Headers:  map[string]string{"X-Test": "1"},
	}
	sanitizedModel, err := SanitizeValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{
			Text:  &emptyText,
			Model: modelRef,
		},
	)
	assert.NoError(t, err)
	assert.Nil(t, sanitizedModel.Text)
	assert.Equal(t, &astructure.ModelRef{
		Provider: "openai",
		Name:     "m",
		Headers:  map[string]string{"X-Test": "1"},
	}, sanitizedModel.Model)
	sanitizedModel.Model.Headers["X-Test"] = "2"
	assert.Equal(t, "1", modelRef.Headers["X-Test"])
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

func TestBuildIndexRejectsSpecificInvalidInputs(t *testing.T) {
	text := "instruction"
	_, err := BuildIndex([]astructure.Surface{
		{
			NodeID: "node_1",
			Type:   astructure.SurfaceTypeInstruction,
			Value:  astructure.SurfaceValue{Text: &text},
		},
	})
	assert.EqualError(t, err, "surface id is empty")
	_, err = BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value:     astructure.SurfaceValue{Text: &text},
		},
	})
	assert.EqualError(t, err, "surface node id is empty")
	_, err = BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceType("unknown"),
			Value:     astructure.SurfaceValue{Text: &text},
		},
	})
	assert.EqualError(t, err, `surface type "unknown" is invalid`)
	_, err = BuildIndex([]astructure.Surface{
		{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
		},
	})
	assert.ErrorContains(t, err, `surface "surf_1" value is invalid`)
}

func TestValidateValueRejectsExtraFields(t *testing.T) {
	text := "instruction"
	assert.EqualError(t, ValidateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:    &text,
			FewShot: []astructure.FewShotExample{{}},
		},
	), "messages are not empty")
	assert.EqualError(t, ValidateValue(
		astructure.SurfaceTypeInstruction,
		astructure.SurfaceValue{
			Text:  &text,
			Model: &astructure.ModelRef{Name: "m"},
		},
	), "model is not nil")
	assert.EqualError(t, ValidateValue(
		astructure.SurfaceTypeFewShot,
		astructure.SurfaceValue{
			FewShot: []astructure.FewShotExample{},
			Model:   &astructure.ModelRef{Name: "m"},
		},
	), "model is not nil")
	assert.EqualError(t, ValidateValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{
			Model: &astructure.ModelRef{Name: "m"},
			Text:  &text,
		},
	), "text is not nil")
	assert.EqualError(t, ValidateValue(
		astructure.SurfaceTypeModel,
		astructure.SurfaceValue{
			Model:   &astructure.ModelRef{Name: "m"},
			FewShot: []astructure.FewShotExample{{}},
		},
	), "messages are not empty")
}

func TestCloneValueDeepCopiesAllFields(t *testing.T) {
	text := "instruction"
	value := astructure.SurfaceValue{
		Text: &text,
		FewShot: []astructure.FewShotExample{
			{
				Messages: []astructure.FewShotMessage{
					{Role: "user", Content: "hi"},
				},
			},
		},
		Model: &astructure.ModelRef{
			Provider: " openai ",
			Name:     " gpt ",
			Headers:  map[string]string{"X-Test": "1"},
		},
	}
	cloned := CloneValue(value)
	assert.NotNil(t, cloned.Text)
	assert.NotNil(t, cloned.Model)
	assert.Equal(t, "openai", cloned.Model.Provider)
	assert.Equal(t, "gpt", cloned.Model.Name)
	cloned.FewShot[0].Messages[0].Content = "changed"
	*cloned.Text = "changed"
	cloned.Model.Headers["X-Test"] = "2"
	assert.Equal(t, "instruction", *value.Text)
	assert.Equal(t, "hi", value.FewShot[0].Messages[0].Content)
	assert.Equal(t, " openai ", value.Model.Provider)
	assert.Equal(t, " gpt ", value.Model.Name)
	assert.Equal(t, "1", value.Model.Headers["X-Test"])
}
