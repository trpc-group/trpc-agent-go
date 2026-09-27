//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestApplySessionStateTimestamps(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	jsonCreatedAt := time.Date(2026, 8, 25, 17, 0, 0, 0, location)
	jsonUpdatedAt := jsonCreatedAt.Add(time.Minute)
	databaseCreatedAt := time.Date(2026, 8, 25, 17, 0, 0, 0, time.UTC)
	databaseUpdatedAt := databaseCreatedAt.Add(time.Minute)

	tests := []struct {
		name          string
		state         SessionState
		wantCreatedAt time.Time
		wantUpdatedAt time.Time
	}{
		{
			name: "prefer JSON timestamps",
			state: SessionState{
				CreatedAt: jsonCreatedAt,
				UpdatedAt: jsonUpdatedAt,
			},
			wantCreatedAt: jsonCreatedAt,
			wantUpdatedAt: jsonUpdatedAt,
		},
		{
			name:          "fall back for legacy payload",
			state:         SessionState{},
			wantCreatedAt: databaseCreatedAt,
			wantUpdatedAt: databaseUpdatedAt,
		},
		{
			name: "fall back only for missing creation time",
			state: SessionState{
				UpdatedAt: jsonUpdatedAt,
			},
			wantCreatedAt: databaseCreatedAt,
			wantUpdatedAt: jsonUpdatedAt,
		},
		{
			name: "fall back only for missing update time",
			state: SessionState{
				CreatedAt: jsonCreatedAt,
			},
			wantCreatedAt: jsonCreatedAt,
			wantUpdatedAt: databaseUpdatedAt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applySessionStateTimestamps(
				&tt.state,
				databaseCreatedAt,
				databaseUpdatedAt,
			)
			assert.True(t, tt.state.CreatedAt.Equal(tt.wantCreatedAt))
			assert.True(t, tt.state.UpdatedAt.Equal(tt.wantUpdatedAt))
		})
	}
}

func TestGetTrackEventsByTrackLists_EmptyAndMismatch(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db)
	got, err := s.getTrackEventsByTrackLists(context.Background(), nil, nil, 0, time.Time{})
	require.NoError(t, err)
	assert.Nil(t, got)
	_, err = s.getTrackEventsByTrackLists(
		context.Background(),
		[]session.Key{{AppName: "app", UserID: "user", SessionID: "sess"}},
		nil,
		0,
		time.Time{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "track lists count mismatch")
}
