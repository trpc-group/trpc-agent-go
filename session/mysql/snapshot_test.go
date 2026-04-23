//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type extraFieldsValueHolder struct {
	Values []string
}

type extraFieldsPointerHolder struct {
	Value string
}

func TestSnapshotExtraFields_PreservesTypesAndDeepCopies(t *testing.T) {
	currentTime := time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC)
	structValue := extraFieldsValueHolder{Values: []string{"before"}}
	structPointer := &extraFieldsPointerHolder{Value: "before"}

	extraFields := map[string]any{
		"int64":      int64(7),
		"time":       currentTime,
		"slice":      []int{1, 2},
		"array":      [2]string{"a", "b"},
		"nested_map": map[string]any{"count": int64(1)},
		"struct":     structValue,
		"struct_ptr": structPointer,
		"raw":        json.RawMessage(`{"ok":true}`),
	}

	snap := snapshotExtraFields(extraFields)

	extraFields["slice"].([]int)[0] = 9
	extraFields["nested_map"].(map[string]any)["count"] = int64(2)
	extraFields["struct"].(extraFieldsValueHolder).Values[0] = "after"
	extraFields["struct_ptr"].(*extraFieldsPointerHolder).Value = "after"
	extraFields["raw"].(json.RawMessage)[2] = 'x'

	require.NotNil(t, snap)
	assert.IsType(t, int64(0), snap["int64"])
	assert.Equal(t, int64(7), snap["int64"])
	assert.IsType(t, time.Time{}, snap["time"])
	assert.Equal(t, currentTime, snap["time"])
	assert.Equal(t, []int{1, 2}, snap["slice"])
	assert.Equal(t, [2]string{"a", "b"}, snap["array"])
	assert.Equal(t, int64(1), snap["nested_map"].(map[string]any)["count"])
	assert.Equal(t, []string{"before"}, snap["struct"].(extraFieldsValueHolder).Values)
	assert.Equal(t, "before", snap["struct_ptr"].(*extraFieldsPointerHolder).Value)
	assert.NotSame(t, structPointer, snap["struct_ptr"])
	assert.Equal(t, json.RawMessage(`{"ok":true}`), snap["raw"])
}

func TestSnapshotContentPart_ClonesBinaryPayloads(t *testing.T) {
	imageData := []byte{1, 2, 3}
	audioData := []byte{4, 5, 6}
	fileData := []byte("before-file")

	imagePart := model.ContentPart{
		Type: model.ContentTypeImage,
		Image: &model.Image{
			URL:    "https://example.com/image.png",
			Data:   imageData,
			Detail: "high",
			Format: "png",
		},
	}
	audioPart := model.ContentPart{
		Type: model.ContentTypeAudio,
		Audio: &model.Audio{
			Data:   audioData,
			Format: "wav",
		},
	}
	filePart := model.ContentPart{
		Type: model.ContentTypeFile,
		File: &model.File{
			Name:     "payload.json",
			Data:     fileData,
			FileID:   "file-1",
			MimeType: "application/json",
		},
	}

	imageSnap := snapshotContentPart(imagePart)
	audioSnap := snapshotContentPart(audioPart)
	fileSnap := snapshotContentPart(filePart)

	imagePart.Image.Data[0] = 9
	audioPart.Audio.Data[0] = 8
	filePart.File.Data[0] = 'A'

	require.NotNil(t, imageSnap.Image)
	require.NotNil(t, audioSnap.Audio)
	require.NotNil(t, fileSnap.File)
	assert.Equal(t, []byte{1, 2, 3}, imageSnap.Image.Data)
	assert.Equal(t, []byte{4, 5, 6}, audioSnap.Audio.Data)
	assert.Equal(t, []byte("before-file"), fileSnap.File.Data)
}

func TestSnapshotEvent_DeepCopiesAndClearsInMemoryFields(t *testing.T) {
	text := "before-text"
	evt := event.New("inv-1", "author")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role: model.RoleAssistant,
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeText,
							Text: &text,
						},
					},
				},
			},
		},
	}
	evt.LongRunningToolIDs = map[string]struct{}{"tool-1": {}}
	evt.StateDelta = map[string][]byte{"state": []byte("before"), "nil": nil}
	evt.Extensions = map[string]json.RawMessage{
		"meta": json.RawMessage(`{"key":"before"}`),
		"nil":  nil,
	}
	evt.Actions = &event.EventActions{SkipSummarization: true}
	evt.StructuredOutput = map[string]string{"drop": "me"}
	evt.ExecutionTrace = &trace.Trace{
		RootAgentName:    "root",
		RootInvocationID: "inv-1",
		SessionID:        "sess-1",
	}

	snap := snapshotEvent(evt)

	require.NotNil(t, snap)
	evt.LongRunningToolIDs["tool-2"] = struct{}{}
	evt.StateDelta["state"][0] = 'A'
	evt.Extensions["meta"][8] = 'A'
	*evt.Response.Choices[0].Message.ContentParts[0].Text = "after-text"

	assert.Nil(t, snap.StructuredOutput)
	assert.Nil(t, snap.ExecutionTrace)
	assert.Len(t, snap.LongRunningToolIDs, 1)
	assert.Equal(t, []byte("before"), snap.StateDelta["state"])
	assert.Nil(t, snap.StateDelta["nil"])
	assert.Equal(t, json.RawMessage(`{"key":"before"}`), snap.Extensions["meta"])
	assert.Nil(t, snap.Extensions["nil"])
	require.NotNil(t, snap.Actions)
	assert.True(t, snap.Actions.SkipSummarization)
	require.Len(t, snap.Response.Choices, 1)
	require.Len(t, snap.Response.Choices[0].Message.ContentParts, 1)
	require.NotNil(t, snap.Response.Choices[0].Message.ContentParts[0].Text)
	assert.Equal(t, "before-text", *snap.Response.Choices[0].Message.ContentParts[0].Text)
}

func TestSnapshotHelpers_HandleNilAndEmptyValues(t *testing.T) {
	assert.Nil(t, snapshotEvent(nil))
	assert.Nil(t, snapshotResponse(nil))
	assert.Nil(t, snapshotTrackEvent(nil))

	emptyResponse := &model.Response{ID: "rsp-1"}
	responseSnap := snapshotResponse(emptyResponse)
	require.NotNil(t, responseSnap)
	assert.Empty(t, responseSnap.Choices)

	trackEvent := &session.TrackEvent{
		Track:     "agui",
		Timestamp: time.Now(),
	}
	trackSnap := snapshotTrackEvent(trackEvent)
	require.NotNil(t, trackSnap)
	assert.Nil(t, trackSnap.Payload)
}
