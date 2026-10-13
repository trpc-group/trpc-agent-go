//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	"context"
	"encoding/json"
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTranslateUnifiedProgress(t *testing.T) {
	for _, object := range []string{model.ObjectTypePreprocessingStatus, graph.ObjectTypeGraphNodeCustom, "app.status"} {
		t.Run(object, func(t *testing.T) {
			tr := newTranslatorForTest(t)
			e := event.New("child", "worker", event.WithObject(object))
			e.ParentInvocationID = "parent"
			require.NoError(t, event.SetExtension(e, event.ProgressExtensionKey, event.Progress{
				OperationID: "attempt", Kind: "work", Status: event.ProgressStatusFailed, Message: "retrying",
			}))
			if object == graph.ObjectTypeGraphNodeCustom {
				meta, err := json.Marshal(graph.NodeCustomEventMetadata{
					Category: graph.NodeCustomEventCategoryProgress, Progress: 25, Message: "legacy",
				})
				require.NoError(t, err)
				e.StateDelta = map[string][]byte{graph.MetadataKeyNodeCustom: meta}
			}
			translated, err := tr.Translate(context.Background(), e)
			require.NoError(t, err)
			require.Len(t, translated, 1, "one notification, no duplicate legacy progress or RunError")
			custom, ok := translated[0].(*aguievents.CustomEvent)
			require.True(t, ok)
			require.Equal(t, "progress", custom.Name)
			data, err := json.Marshal(custom.Value)
			require.NoError(t, err)
			var value map[string]any
			require.NoError(t, json.Unmarshal(data, &value))
			require.Equal(t, "attempt", value["operation_id"])
			require.Equal(t, "failed", value["status"])
			require.Equal(t, "work", value["kind"])
			require.Equal(t, "retrying", value["message"])
			require.Equal(t, e.ID, value["event_id"])
			require.Equal(t, "child", value["invocation_id"])
			require.Equal(t, "parent", value["parent_invocation_id"])
			require.NotContains(t, value, "percent")
		})
	}
}

func TestTranslateMalformedProgressKeepsLegacy(t *testing.T) {
	for _, raw := range []string{`null`, `{`, `{"operation_id":"a"}`} {
		tr := newTranslatorForTest(t)
		e := event.New("inv", "node", event.WithObject(graph.ObjectTypeGraphNodeCustom))
		e.Extensions = map[string]json.RawMessage{event.ProgressExtensionKey: json.RawMessage(raw)}
		meta, err := json.Marshal(graph.NodeCustomEventMetadata{
			EventType: "progress", Category: graph.NodeCustomEventCategoryProgress, NodeID: "node", Progress: 25,
		})
		require.NoError(t, err)
		e.StateDelta = map[string][]byte{graph.MetadataKeyNodeCustom: meta}
		translated, err := tr.Translate(context.Background(), e)
		require.NoError(t, err)
		require.Len(t, translated, 1)
		custom := translated[0].(*aguievents.CustomEvent)
		require.Equal(t, "progress", custom.Name)
		require.Equal(t, "node", custom.Value.(map[string]any)["nodeId"])
		require.Equal(t, 25.0, custom.Value.(map[string]any)["progress"])
	}
}
