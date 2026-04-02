//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- Tests for refreshSessionTTL ---

func TestRefreshSessionTTL_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 10 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectExec("UPDATE .* SET updated_at").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.refreshSessionTTL(
		context.Background(), key,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshSessionTTL_DBError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 10 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectExec("UPDATE .* SET updated_at").
		WillReturnError(fmt.Errorf("db error"))

	err := s.refreshSessionTTL(
		context.Background(), key,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"refresh session TTL failed")
}

// --- Tests for addEvent ---

func TestAddEvent_SessionNotFound(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))
	mock.ExpectRollback()

	err := s.addEvent(
		context.Background(), key, &event.Event{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAddEvent_TransactionError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnError(fmt.Errorf("tx error"))
	mock.ExpectRollback()

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}

	err := s.addEvent(context.Background(), key, evt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "store event failed")
}

func TestAddEvent_QueryStateError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnError(fmt.Errorf("query error"))
	mock.ExpectRollback()

	err := s.addEvent(
		context.Background(), key, &event.Event{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"get session state failed")
}

// --- Tests for addTrackEvent ---

func TestAddTrackEvent_SessionNotFound(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		))
	mock.ExpectRollback()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAddTrackEvent_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnError(fmt.Errorf("db error"))
	mock.ExpectRollback()

	te := &session.TrackEvent{
		Track:     "track1",
		Timestamp: time.Now(),
	}
	err := s.addTrackEvent(
		context.Background(), key, te,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"get session state failed")
}

// --- Tests for getEventsList ---

func TestGetEventsList_EmptyKeys(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	result, err := s.getEventsList(
		context.Background(), nil, 0, time.Time{},
	)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetEventsList_Success(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	// Event must contain a valid user message so
	// ApplyEventFiltering does not discard it.
	evt := event.Event{
		InvocationID: "inv-1",
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}
	evtBytes, _ := json.Marshal(evt)

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess", evtBytes))

	result, err := s.getEventsList(
		context.Background(), keys, 0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0], 1)
	assert.Equal(t, "inv-1",
		result[0][0].InvocationID,
	)
}

func TestGetEventsList_WithLimitAndAfterTime(
	t *testing.T,
) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()
	s.opts.sessionEventLimit = 5
	s.opts.sessionTTL = 1 * time.Hour

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		))

	result, err := s.getEventsList(
		context.Background(), keys, 0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Empty(t, result[0])
}

func TestGetEventsList_NoEventsForSession(
	t *testing.T,
) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		))

	result, err := s.getEventsList(
		context.Background(), keys, 10, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Empty(t, result[0])
}

func TestGetEventsList_NilEventBytes(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess", nil))

	result, err := s.getEventsList(
		context.Background(), keys, 0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Empty(t, result[0])
}

func TestGetEventsList_InvalidEventJSON(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	mock.ExpectQuery("SELECT session_id, event").
		WillReturnRows(sqlmock.NewRows(
			[]string{"session_id", "event"},
		).AddRow("sess", []byte(`{invalid`)))

	_, err := s.getEventsList(
		context.Background(), keys, 0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"unmarshal event failed")
}

func TestGetEventsList_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT session_id, event").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.getEventsList(
		context.Background(), []session.Key{key},
		0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"query events failed")
}

// --- Tests for getSummariesList ---

func TestGetSummariesList_EmptyKeys(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	result, err := s.getSummariesList(
		context.Background(), nil,
	)
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestGetSummariesList_Success(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	sum := session.Summary{Summary: "test summary"}
	sumBytes, _ := json.Marshal(sum)

	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		).AddRow("sess", "all", sumBytes))

	result, err := s.getSummariesList(
		context.Background(), keys,
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.NotNil(t, result[0]["all"])
	assert.Equal(t, "test summary",
		result[0]["all"].Summary,
	)
}

func TestGetSummariesList_NoSummaries(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		))

	result, err := s.getSummariesList(
		context.Background(), keys,
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Empty(t, result[0])
}

func TestGetSummariesList_InvalidJSON(t *testing.T) {
	s, mock, db := newTestServiceWithSliceSupport(
		t, nil,
	)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "sess"},
	}

	mock.ExpectQuery("SELECT session_id, filter_key").
		WillReturnRows(sqlmock.NewRows(
			[]string{
				"session_id", "filter_key", "summary",
			},
		).AddRow("sess", "all", []byte(`{bad`)))

	_, err := s.getSummariesList(
		context.Background(), keys,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"unmarshal summary failed")
}

func TestGetSummariesList_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	mock.ExpectQuery("SELECT session_id, filter_key").
		WithArgs(
			sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.getSummariesList(
		context.Background(), []session.Key{key},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"query summaries failed")
}

// --- Tests for getTrackEvents ---

func TestGetTrackEvents_EmptyKeys(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	result, err := s.getTrackEvents(
		context.Background(), nil, nil, 0, time.Time{},
	)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetTrackEvents_MismatchedLengths(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user", SessionID: "s1"},
	}

	_, err := s.getTrackEvents(
		context.Background(), keys,
		[]*SessionState{}, // Mismatched length.
		0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "count mismatch")
}

func TestGetTrackEvents_NoTracks(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user", SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1", State: session.StateMap{}},
	}

	result, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Empty(t, result[0])
}

// --- Tests for addEvent partial event (no insert) ---

func TestAddEvent_PartialEvent_NoInsert(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	// Transaction with only state update (no event
	// insert for partial).
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	evt := &event.Event{
		Response: &model.Response{
			IsPartial: true,
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "partial",
				}},
			},
		},
	}

	err := s.addEvent(context.Background(), key, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Test for addEvent with TTL ---

func TestAddEvent_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.sessionTTL = 30 * time.Minute

	key := session.Key{
		AppName: "app", UserID: "user", SessionID: "sess",
	}

	sessState := SessionState{
		ID:    "sess",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT state, expires_at FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"state", "expires_at"},
		).AddRow(stateBytes, nil))
	mock.ExpectExec("UPDATE .* SET state").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	evt := &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				}},
			},
		},
	}

	err := s.addEvent(context.Background(), key, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Tests for getTrackEvents with track data ---

func tracksState(
	t *testing.T, tracks []session.Track,
) session.StateMap {
	t.Helper()
	encoded, err := json.Marshal(tracks)
	require.NoError(t, err)
	return session.StateMap{"tracks": encoded}
}

func TestGetTrackEvents_WithTracks_NoLimit(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1",
			State: tracksState(t,
				[]session.Track{"alpha"}),
		},
	}

	te := session.TrackEvent{
		Track:   "alpha",
		Payload: json.RawMessage(`"data"`),
	}
	teBytes, _ := json.Marshal(te)

	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow(teBytes))

	result, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0], 1)
	assert.Len(t, result[0]["alpha"], 1)
}

func TestGetTrackEvents_WithTracks_WithLimit(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1",
			State: tracksState(t,
				[]session.Track{"beta"}),
		},
	}

	te := session.TrackEvent{
		Track:   "beta",
		Payload: json.RawMessage(`"data"`),
	}
	teBytes, _ := json.Marshal(te)

	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow(teBytes))

	result, err := s.getTrackEvents(
		context.Background(), keys, states,
		5, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0]["beta"], 1)
}

func TestGetTrackEvents_QueryError(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1",
			State: tracksState(t,
				[]session.Track{"alpha"}),
		},
	}

	mock.ExpectQuery("SELECT event FROM").
		WillReturnError(fmt.Errorf("db error"))

	_, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"query track events failed")
}

func TestGetTrackEvents_InvalidJSON(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1",
			State: tracksState(t,
				[]session.Track{"alpha"}),
		},
	}

	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow([]byte(`{bad`)))

	_, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"unmarshal track event failed")
}

func TestGetTrackEvents_MultipleTracks(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1",
			State: tracksState(t,
				[]session.Track{"a", "b"}),
		},
	}

	te1 := session.TrackEvent{
		Track:   "a",
		Payload: json.RawMessage(`"d1"`),
	}
	te1Bytes, _ := json.Marshal(te1)
	te2 := session.TrackEvent{
		Track:   "b",
		Payload: json.RawMessage(`"d2"`),
	}
	te2Bytes, _ := json.Marshal(te2)

	// Two queries, one per track.
	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow(te1Bytes))
	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow(te2Bytes))

	result, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0]["a"], 1)
	assert.Len(t, result[0]["b"], 1)
}

func TestGetTrackEvents_InvalidTrackState(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1",
			State: session.StateMap{
				"tracks": []byte("{bad json"),
			},
		},
	}

	_, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"get track list failed")
}

func TestGetTrackEvents_MultipleEventsReversed(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	keys := []session.Key{
		{AppName: "app", UserID: "user",
			SessionID: "s1"},
	}
	states := []*SessionState{
		{ID: "s1",
			State: tracksState(t,
				[]session.Track{"alpha"}),
		},
	}

	te1 := session.TrackEvent{
		Track:   "alpha",
		Payload: json.RawMessage(`"second"`),
	}
	te2 := session.TrackEvent{
		Track:   "alpha",
		Payload: json.RawMessage(`"first"`),
	}
	te1Bytes, _ := json.Marshal(te1)
	te2Bytes, _ := json.Marshal(te2)

	// DB returns DESC order: second, first.
	mock.ExpectQuery("SELECT event FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"event"},
		).AddRow(te1Bytes).AddRow(te2Bytes))

	result, err := s.getTrackEvents(
		context.Background(), keys, states,
		0, time.Time{},
	)
	require.NoError(t, err)
	require.Len(t, result[0]["alpha"], 2)
	// Should be reversed to ascending order.
	assert.Equal(t,
		json.RawMessage(`"first"`),
		result[0]["alpha"][0].Payload,
	)
	assert.Equal(t,
		json.RawMessage(`"second"`),
		result[0]["alpha"][1].Payload,
	)
}
