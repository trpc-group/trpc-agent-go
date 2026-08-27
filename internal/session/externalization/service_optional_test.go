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
		{
			name:     "track page",
			wrapped:  &trackPageService{trackService: &trackService{Service: base}},
			expected: &stateInitializingTrackPageService{},
		},
		{
			name: "searchable track page",
			wrapped: &searchableTrackPageService{
				searchableTrackService: &searchableTrackService{Service: base},
			},
			expected: &stateInitializingSearchableTrackPageService{},
		},
		{
			name: "window track page",
			wrapped: &windowTrackPageService{
				windowTrackService: &windowTrackService{Service: base},
			},
			expected: &stateInitializingWindowTrackPageService{},
		},
		{
			name: "track reader page",
			wrapped: &trackReaderPageService{
				trackReaderService: &trackReaderService{Service: base},
			},
			expected: &stateInitializingTrackReaderPageService{},
		},
		{
			name: "searchable window track page",
			wrapped: &searchableWindowTrackPageService{
				searchableWindowTrackService: &searchableWindowTrackService{Service: base},
			},
			expected: &stateInitializingSearchableWindowTrackPageService{},
		},
		{
			name: "searchable track reader page",
			wrapped: &searchableTrackReaderPageService{
				searchableTrackReaderService: &searchableTrackReaderService{Service: base},
			},
			expected: &stateInitializingSearchableTrackReaderPageService{},
		},
		{
			name: "window track reader page",
			wrapped: &windowTrackReaderPageService{
				windowTrackReaderService: &windowTrackReaderService{Service: base},
			},
			expected: &stateInitializingWindowTrackReaderPageService{},
		},
		{
			name: "searchable window track reader page",
			wrapped: &searchableWindowTrackReaderPageService{
				searchableWindowTrackReaderService: &searchableWindowTrackReaderService{Service: base},
			},
			expected: &stateInitializingSearchableWindowTrackReaderPageService{},
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

func TestWrapTrackOptionalInterfacesPreservesOptionalCombinations(t *testing.T) {
	base := &Service{}
	searchable := searchableStub{}
	window := windowStub{}
	track := trackStub{}
	reader := readerStub{}
	pager := pagerStub{}
	tests := []struct {
		name      string
		hasSearch bool
		hasWindow bool
		hasReader bool
		hasPager  bool
		expected  session.Service
	}{
		{
			name:      "searchable window reader page",
			hasSearch: true,
			hasWindow: true,
			hasReader: true,
			hasPager:  true,
			expected:  &searchableWindowTrackReaderPageService{},
		},
		{
			name:      "searchable window reader",
			hasSearch: true,
			hasWindow: true,
			hasReader: true,
			expected:  &searchableWindowTrackReaderService{},
		},
		{
			name:      "searchable window page",
			hasSearch: true,
			hasWindow: true,
			hasPager:  true,
			expected:  &searchableWindowTrackPageService{},
		},
		{
			name:      "searchable window",
			hasSearch: true,
			hasWindow: true,
			expected:  &searchableWindowTrackService{},
		},
		{
			name:      "searchable reader page",
			hasSearch: true,
			hasReader: true,
			hasPager:  true,
			expected:  &searchableTrackReaderPageService{},
		},
		{
			name:      "searchable reader",
			hasSearch: true,
			hasReader: true,
			expected:  &searchableTrackReaderService{},
		},
		{
			name:      "searchable page",
			hasSearch: true,
			hasPager:  true,
			expected:  &searchableTrackPageService{},
		},
		{
			name:      "searchable",
			hasSearch: true,
			expected:  &searchableTrackService{},
		},
		{
			name:      "window reader page",
			hasWindow: true,
			hasReader: true,
			hasPager:  true,
			expected:  &windowTrackReaderPageService{},
		},
		{
			name:      "window reader",
			hasWindow: true,
			hasReader: true,
			expected:  &windowTrackReaderService{},
		},
		{
			name:      "window page",
			hasWindow: true,
			hasPager:  true,
			expected:  &windowTrackPageService{},
		},
		{
			name:      "window",
			hasWindow: true,
			expected:  &windowTrackService{},
		},
		{
			name:      "reader page",
			hasReader: true,
			hasPager:  true,
			expected:  &trackReaderPageService{},
		},
		{
			name:      "reader",
			hasReader: true,
			expected:  &trackReaderService{},
		},
		{
			name:     "page",
			hasPager: true,
			expected: &trackPageService{},
		},
		{
			name:     "track",
			expected: &trackService{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := wrapTrackOptionalInterfaces(
				base,
				searchable,
				test.hasSearch,
				window,
				test.hasWindow,
				track,
				reader,
				test.hasReader,
				pager,
				test.hasPager,
			)
			require.Equal(t, reflect.TypeOf(test.expected), reflect.TypeOf(wrapped))
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

type searchableStub struct{}

func (searchableStub) SearchEvents(
	context.Context,
	session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return nil, nil
}

type windowStub struct{}

func (windowStub) GetEventWindow(
	context.Context,
	session.EventWindowRequest,
) (*session.EventWindow, error) {
	return nil, nil
}

type trackStub struct{}

func (trackStub) AppendTrackEvent(
	context.Context,
	*session.Session,
	*session.TrackEvent,
	...session.Option,
) error {
	return nil
}

type readerStub struct{}

func (readerStub) GetTrackEvents(
	context.Context,
	session.Key,
	session.Track,
	...session.Option,
) (*session.TrackEvents, error) {
	return nil, nil
}

type pagerStub struct{}

func (pagerStub) GetTrackEventPage(
	context.Context,
	session.TrackEventPageRequest,
) (*session.TrackEventPage, error) {
	return nil, nil
}
