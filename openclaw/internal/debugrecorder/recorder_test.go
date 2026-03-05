//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package debugrecorder

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

func TestParseMode(t *testing.T) {
	t.Parallel()

	mode, err := ParseMode("")
	require.NoError(t, err)
	require.Equal(t, modeFull, mode)

	mode, err = ParseMode(" SAFE ")
	require.NoError(t, err)
	require.Equal(t, modeSafe, mode)

	_, err = ParseMode("nope")
	require.Error(t, err)
}

func TestRecorder_Start_WritesMetaAndEvents(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{
		Channel:   "telegram",
		RequestID: "req-1",
	})
	require.NoError(t, err)
	require.NotNil(t, trace)

	t.Cleanup(func() { _ = trace.Close(TraceEnd{Status: "ok"}) })

	require.True(t, strings.HasPrefix(trace.Dir(), rec.Dir()))

	_, err = os.Stat(filepath.Join(trace.Dir(), metaFileName))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(trace.Dir(), eventsFileName))
	require.NoError(t, err)

	evs, err := os.Open(filepath.Join(trace.Dir(), eventsFileName))
	require.NoError(t, err)
	defer evs.Close()

	scanner := bufio.NewScanner(evs)
	require.True(t, scanner.Scan())
	require.NoError(t, scanner.Err())

	var got record
	require.NoError(t, json.Unmarshal(scanner.Bytes(), &got))
	require.Equal(t, KindTraceStart, got.Kind)
}

func TestTrace_Close_WritesEndAndResult(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)

	require.NoError(t, trace.RecordText("hello"))

	end := TraceEnd{
		Status:   "ok",
		Duration: time.Second,
	}
	require.NoError(t, trace.Close(end))

	_, err = os.Stat(filepath.Join(trace.Dir(), resultFileName))
	require.NoError(t, err)

	evs, err := os.Open(filepath.Join(trace.Dir(), eventsFileName))
	require.NoError(t, err)
	defer evs.Close()

	scanner := bufio.NewScanner(evs)
	foundEnd := false
	for scanner.Scan() {
		var rec record
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &rec))
		if rec.Kind == KindTraceEnd {
			foundEnd = true
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, foundEnd)
}

func TestTrace_StoreBlob_SafeModeDoesNotWrite(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeSafe)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = trace.Close(TraceEnd{Status: "ok"}) })

	data := []byte("hello")
	ref, err := trace.StoreBlob("a.txt", data)
	require.NoError(t, err)
	require.Equal(t, "", ref.Ref)

	sum := sha256.Sum256(data)
	require.Equal(t, hex.EncodeToString(sum[:]), ref.SHA256)
	require.Equal(t, len(data), ref.Size)
	require.Equal(t, "a.txt", ref.Name)

	_, err = os.Stat(
		filepath.Join(trace.Dir(), defaultAttachmentsDir, ref.SHA256),
	)
	require.Error(t, err)
}

func TestTrace_StoreBlob_FullModeWritesAndDedupes(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = trace.Close(TraceEnd{Status: "ok"}) })

	data := []byte("hello")
	ref1, err := trace.StoreBlob("a.txt", data)
	require.NoError(t, err)
	require.NotEmpty(t, ref1.Ref)

	ref2, err := trace.StoreBlob("a.txt", data)
	require.NoError(t, err)
	require.Equal(t, ref1, ref2)

	dst := filepath.Join(trace.Dir(), ref1.Ref)
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestSummarizeRequest_StoresInlineData(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = trace.Close(TraceEnd{Status: "ok"}) })

	img := []byte("imgdata")
	req := gwproto.MessageRequest{
		Channel: "gateway",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeImage,
				Image: &gwproto.ImagePart{
					Data:   img,
					Format: "png",
				},
			},
		},
	}

	summary, err := SummarizeRequest(trace, req)
	require.NoError(t, err)
	require.Len(t, summary.ContentParts, 1)
	require.NotNil(t, summary.ContentParts[0].Image)
	require.NotEmpty(t, summary.ContentParts[0].Image.Data.SHA256)
	require.NotEmpty(t, summary.ContentParts[0].Image.Data.Ref)

	dst := filepath.Join(trace.Dir(), summary.ContentParts[0].Image.Data.Ref)
	_, err = os.Stat(dst)
	require.NoError(t, err)
}
