//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package externalization

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestWrapStateInitializationInterfacePreservesOptionalCombinations(
	t *testing.T,
) {
	base := &Service{}
	initializer := stateInitializationStub{}
	tests := []struct {
		name     string
		wrapped  session.Service
		expected session.Service
	}{
		{name: "base", wrapped: base, expected: &stateInitializingService{}},
		{
			name:     "searchable",
			wrapped:  &searchableService{Service: base},
			expected: &stateInitializingSearchableService{},
		},
		{
			name:     "window",
			wrapped:  &windowService{Service: base},
			expected: &stateInitializingWindowService{},
		},
		{
			name:     "track",
			wrapped:  &trackService{Service: base},
			expected: &stateInitializingTrackService{},
		},
		{
			name:     "searchable window",
			wrapped:  &searchableWindowService{Service: base},
			expected: &stateInitializingSearchableWindowService{},
		},
		{
			name:     "searchable track",
			wrapped:  &searchableTrackService{Service: base},
			expected: &stateInitializingSearchableTrackService{},
		},
		{
			name:     "window track",
			wrapped:  &windowTrackService{Service: base},
			expected: &stateInitializingWindowTrackService{},
		},
		{
			name:     "track reader",
			wrapped:  &trackReaderService{Service: base},
			expected: &stateInitializingTrackReaderService{},
		},
		{
			name:     "searchable window track",
			wrapped:  &searchableWindowTrackService{Service: base},
			expected: &stateInitializingSearchableWindowTrackService{},
		},
		{
			name:     "searchable window track reader",
			wrapped:  &searchableWindowTrackReaderService{Service: base},
			expected: &stateInitializingSearchableWindowTrackReaderService{},
		},
		{
			name:     "searchable track reader",
			wrapped:  &searchableTrackReaderService{Service: base},
			expected: &stateInitializingSearchableTrackReaderService{},
		},
		{
			name:     "window track reader",
			wrapped:  &windowTrackReaderService{Service: base},
			expected: &stateInitializingWindowTrackReaderService{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := wrapStateInitializationInterface(test.wrapped, initializer)
			require.Equal(t, reflect.TypeOf(test.expected), reflect.TypeOf(wrapped))
			stateInitializer, ok := wrapped.(session.StateInitializationService)
			require.True(t, ok)
			value, didInitialize, err := stateInitializer.LoadOrInitializeSessionState(
				context.Background(),
				session.Key{},
				"state",
				nil,
				nil,
			)
			require.NoError(t, err)
			require.True(t, didInitialize)
			require.Equal(t, "value", string(value))
		})
	}
}

func TestWrapStateInitializationInterfaceLeavesUnknownWrapper(t *testing.T) {
	wrapped := &unknownOptionalService{Service: &Service{}}
	require.Same(
		t,
		wrapped,
		wrapStateInitializationInterface(wrapped, stateInitializationStub{}),
	)
}

func TestWrapOptionalInterfacesOmitsUnavailableStateInitialization(t *testing.T) {
	base := &Service{}
	inner := &unavailableStateInitializationService{
		Service: base,
	}
	wrapped := wrapOptionalInterfaces(base, inner)
	_, ok := wrapped.(session.StateInitializationService)
	require.False(t, ok)
}

type stateInitializationStub struct{}

func (stateInitializationStub) LoadOrInitializeSessionState(
	context.Context,
	session.Key,
	string,
	func([]byte) bool,
	func(context.Context) ([]byte, error),
	...session.StateInitializationProjection,
) ([]byte, bool, error) {
	return []byte("value"), true, nil
}

type unknownOptionalService struct {
	session.Service
}

type unavailableStateInitializationService struct {
	session.Service
}

func (unavailableStateInitializationService) StateInitializationAvailable() bool {
	return false
}

func (unavailableStateInitializationService) LoadOrInitializeSessionState(
	context.Context,
	session.Key,
	string,
	func([]byte) bool,
	func(context.Context) ([]byte, error),
	...session.StateInitializationProjection,
) ([]byte, bool, error) {
	return []byte("unexpected"), true, nil
}
