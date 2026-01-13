//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
//

package knowledge_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	itransform "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/transform"
)

// MockTransformer is a mock implementation of Transformer interface
type MockTransformer struct {
	mock.Mock
}

func (m *MockTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	args := m.Called(docs)
	return args.Get(0).([]*document.Document), args.Error(1)
}

func (m *MockTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	args := m.Called(docs)
	return args.Get(0).([]*document.Document), args.Error(1)
}

func (m *MockTransformer) Name() string {
	args := m.Called()
	return args.String(0)
}

func TestApplyPreprocess(t *testing.T) {
	doc := &document.Document{Content: "test"}
	docs := []*document.Document{doc}

	t.Run("Empty input", func(t *testing.T) {
		res, err := itransform.ApplyPreprocess(nil)
		assert.NoError(t, err)
		assert.Nil(t, res)
	})

	t.Run("No transformers", func(t *testing.T) {
		res, err := itransform.ApplyPreprocess(docs)
		assert.NoError(t, err)
		assert.Equal(t, docs, res)
	})

	t.Run("Success", func(t *testing.T) {
		mockT := new(MockTransformer)
		mockT.On("Preprocess", docs).Return(docs, nil)

		res, err := itransform.ApplyPreprocess(docs, mockT)
		assert.NoError(t, err)
		assert.Equal(t, docs, res)
		mockT.AssertExpectations(t)
	})

	t.Run("Error", func(t *testing.T) {
		mockT := new(MockTransformer)
		expectedErr := errors.New("transform error")
		mockT.On("Preprocess", docs).Return([]*document.Document(nil), expectedErr)

		res, err := itransform.ApplyPreprocess(docs, mockT)
		assert.ErrorIs(t, err, expectedErr)
		assert.Nil(t, res)
		mockT.AssertExpectations(t)
	})

	t.Run("Nil Transformer", func(t *testing.T) {
		res, err := itransform.ApplyPreprocess(docs, nil)
		assert.NoError(t, err)
		assert.Equal(t, docs, res)
	})
}

func TestApplyPostprocess(t *testing.T) {
	doc := &document.Document{Content: "test"}
	docs := []*document.Document{doc}

	t.Run("Empty input", func(t *testing.T) {
		res, err := itransform.ApplyPostprocess(nil)
		assert.NoError(t, err)
		assert.Nil(t, res)
	})

	t.Run("No transformers", func(t *testing.T) {
		res, err := itransform.ApplyPostprocess(docs)
		assert.NoError(t, err)
		assert.Equal(t, docs, res)
	})

	t.Run("Success", func(t *testing.T) {
		mockT := new(MockTransformer)
		mockT.On("Postprocess", docs).Return(docs, nil)

		res, err := itransform.ApplyPostprocess(docs, mockT)
		assert.NoError(t, err)
		assert.Equal(t, docs, res)
		mockT.AssertExpectations(t)
	})

	t.Run("Error", func(t *testing.T) {
		mockT := new(MockTransformer)
		expectedErr := errors.New("transform error")
		mockT.On("Postprocess", docs).Return([]*document.Document(nil), expectedErr)

		res, err := itransform.ApplyPostprocess(docs, mockT)
		assert.ErrorIs(t, err, expectedErr)
		assert.Nil(t, res)
		mockT.AssertExpectations(t)
	})

	t.Run("Nil Transformer", func(t *testing.T) {
		res, err := itransform.ApplyPostprocess(docs, nil)
		assert.NoError(t, err)
		assert.Equal(t, docs, res)
	})
}
