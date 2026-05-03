//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-go/packages/ssestream"
	"github.com/stretchr/testify/require"
)

func TestTolerantEventStreamDecoderSkipsNonJSONAndTrimsPayload(t *testing.T) {
	body := strings.Join([]string{
		": keep-alive\n\n",
		"data: : OPENROUTER PROCESSING\n\n",
		"event: message\n" + `data: {"ok":true}` + "\n\n",
		"data: [DONE]\n\n",
	}, "")
	decoder := newTolerantEventStreamDecoder(io.NopCloser(strings.NewReader(body)))
	defer func() {
		require.NoError(t, decoder.Close())
	}()

	var events []ssestream.Event
	for decoder.Next() {
		events = append(events, decoder.Event())
	}

	require.NoError(t, decoder.Err())
	require.Len(t, events, 2)
	require.Equal(t, "message", events[0].Type)
	require.Equal(t, []byte(`{"ok":true}`), events[0].Data)
	require.Equal(t, []byte("[DONE]"), events[1].Data)
}
