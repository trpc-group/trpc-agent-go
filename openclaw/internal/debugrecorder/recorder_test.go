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
	"errors"
	"math"
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

func TestNilReceiversAreSafe(t *testing.T) {
	t.Parallel()

	var rec *Recorder
	require.Equal(t, "", rec.Dir())
	require.Equal(t, Mode(""), rec.Mode())

	var trace *Trace
	require.Equal(t, "", trace.Dir())
	require.Equal(t, Mode(""), trace.Mode())
	require.NoError(t, trace.RecordText("ignored"))
	require.NoError(t, trace.RecordError(errors.New("ignored")))
	_, err := trace.StoreBlob("a.txt", []byte("ignored"))
	require.NoError(t, err)
	require.NoError(t, trace.Close(TraceEnd{Status: "ok"}))
}

func TestNew_InvalidModeFails(t *testing.T) {
	t.Parallel()

	_, err := New(t.TempDir(), Mode("nope"))
	require.Error(t, err)
}

func TestNew_EmptyDirFails(t *testing.T) {
	t.Parallel()

	_, err := New(" ", modeFull)
	require.Error(t, err)
}

func TestNew_EmptyModeDefaultsToFull(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), "")
	require.NoError(t, err)
	require.Equal(t, modeFull, rec.Mode())
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

func TestRecorder_Start_NilRecorderFails(t *testing.T) {
	t.Parallel()

	var rec *Recorder
	_, err := rec.Start(TraceStart{Channel: "gateway"})
	require.Error(t, err)
}

func TestRecorder_Start_CollidingDirGetsSuffix(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	fixed := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	rec.now = func() time.Time { return fixed }

	t1, err := rec.Start(TraceStart{
		Channel:   "telegram",
		RequestID: "req-1",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = t1.Close(TraceEnd{Status: "ok"}) })

	t2, err := rec.Start(TraceStart{
		Channel:   "telegram",
		RequestID: "req-1",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = t2.Close(TraceEnd{Status: "ok"}) })

	require.NotEqual(t, t1.Dir(), t2.Dir())
	require.True(t, strings.HasPrefix(t2.Dir(), rec.Dir()))
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

func TestTrace_Record_ValidationAndClosedIsNoOp(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)

	require.Error(t, trace.Record(" ", map[string]any{}))
	require.NoError(t, trace.RecordError(nil))
	require.NoError(t, trace.RecordError(errors.New("boom")))
	require.NoError(t, trace.Close(TraceEnd{Status: "ok"}))
	require.NoError(t, trace.RecordText("ignored after close"))
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

func TestTrace_StoreBlob_ClosedTraceIsNoOp(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)

	require.NoError(t, trace.Close(TraceEnd{Status: "ok"}))

	ref, err := trace.StoreBlob("a.txt", []byte("hello"))
	require.NoError(t, err)
	require.Equal(t, "", ref.Ref)
}

func TestTrace_StoreBlob_EmptyDataDoesNotWrite(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = trace.Close(TraceEnd{Status: "ok"}) })

	ref, err := trace.StoreBlob("a.txt", nil)
	require.NoError(t, err)
	require.Equal(t, 0, ref.Size)
	require.Equal(t, "", ref.Ref)
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

func TestTrace_StoreBlob_MkdirFails(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "root")
	require.NoError(t, os.WriteFile(root, []byte("x"), 0o644))

	trace := &Trace{
		root: root,
		mode: modeFull,
	}
	_, err := trace.StoreBlob("a.txt", []byte("hello"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "mkdir")
}

func TestTrace_StoreBlob_CreateTempFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	attDir := filepath.Join(root, defaultAttachmentsDir)
	require.NoError(t, os.MkdirAll(attDir, defaultTraceDirPerm))
	require.NoError(t, os.Chmod(attDir, 0o555))

	trace := &Trace{
		root: root,
		mode: modeFull,
	}
	_, err := trace.StoreBlob("a.txt", []byte("hello"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "temp file")
}

func TestContextHelpers(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	ctx := WithRecorder(nil, rec)
	require.Equal(t, rec, RecorderFromContext(ctx))
	require.Equal(t, modeFull, rec.Mode())

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = trace.Close(TraceEnd{Status: "ok"}) })

	ctx = WithTrace(ctx, trace)
	require.Equal(t, trace, TraceFromContext(ctx))
	require.Equal(t, modeFull, trace.Mode())

	require.Equal(t, rec, RecorderFromContext(WithRecorder(ctx, nil)))
	require.Equal(t, trace, TraceFromContext(WithTrace(ctx, nil)))

	require.Nil(t, RecorderFromContext(WithRecorder(nil, nil)))
	require.Nil(t, TraceFromContext(WithTrace(nil, nil)))

	require.Nil(t, RecorderFromContext(nil))
	require.Nil(t, TraceFromContext(nil))
}

func TestWriteJSONFile_EmptyPathFails(t *testing.T) {
	t.Parallel()

	require.Error(t, writeJSONFile(" ", map[string]any{}))
}

func TestWriteJSONFile_MarshalFails(t *testing.T) {
	t.Parallel()

	dst := filepath.Join(t.TempDir(), "out.json")
	require.Error(t, writeJSONFile(dst, math.Inf(1)))
}

func TestRandomHex_InvalidBytesFails(t *testing.T) {
	t.Parallel()

	_, err := randomHex(0)
	require.Error(t, err)
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

func TestSummarizeRequest_HandlesMultiplePartTypes(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeFull)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{Channel: "gateway"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = trace.Close(TraceEnd{Status: "ok"}) })

	text := "hi"
	audio := []byte("audiodata")
	file := []byte("filedata")
	req := gwproto.MessageRequest{
		Channel: "gateway",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeText,
				Text: &text,
			},
			{
				Type: gwproto.PartTypeAudio,
				Audio: &gwproto.AudioPart{
					Data:   audio,
					Format: "mp3",
					URL:    "https://example.com/audio.mp3",
				},
			},
			{
				Type: gwproto.PartTypeFile,
				File: &gwproto.FilePart{
					Filename: "a.txt",
					Data:     file,
					Format:   "text/plain",
					URL:      "https://example.com/a.txt",
				},
			},
			{
				Type: gwproto.PartTypeLocation,
				Location: &gwproto.LocationPart{
					Latitude:  1.23,
					Longitude: 3.21,
				},
			},
			{
				Type: gwproto.PartTypeLink,
				Link: &gwproto.LinkPart{
					URL:   "https://example.com",
					Title: "Example",
				},
			},
		},
	}

	summary, err := SummarizeRequest(trace, req)
	require.NoError(t, err)
	require.Len(t, summary.ContentParts, 5)

	require.Equal(t, "text", summary.ContentParts[0].Type)
	require.Equal(t, "hi", summary.ContentParts[0].Text)

	require.Equal(t, "audio", summary.ContentParts[1].Type)
	require.NotNil(t, summary.ContentParts[1].Audio)
	require.NotEmpty(t, summary.ContentParts[1].Audio.Data.Ref)

	require.Equal(t, "file", summary.ContentParts[2].Type)
	require.NotNil(t, summary.ContentParts[2].File)
	require.Equal(t, "a.txt", summary.ContentParts[2].File.Filename)
	require.NotEmpty(t, summary.ContentParts[2].File.Data.Ref)

	require.Equal(t, "location", summary.ContentParts[3].Type)
	require.NotNil(t, summary.ContentParts[3].Location)

	require.Equal(t, "link", summary.ContentParts[4].Type)
	require.NotNil(t, summary.ContentParts[4].Link)
}

func TestSummarizeRequest_NilTraceDoesNotPanic(t *testing.T) {
	t.Parallel()

	data := []byte("hello")
	req := gwproto.MessageRequest{
		Channel: "gateway",
		ContentParts: []gwproto.ContentPart{
			{
				Type: gwproto.PartTypeFile,
				File: &gwproto.FilePart{
					Filename: "a.txt",
					Data:     data,
				},
			},
		},
	}

	summary, err := SummarizeRequest(nil, req)
	require.NoError(t, err)
	require.Len(t, summary.ContentParts, 1)
	require.NotNil(t, summary.ContentParts[0].File)
	require.Empty(t, summary.ContentParts[0].File.Data.Ref)
}

func TestSummarizeRequest_StoresRequestSystemPrompt(t *testing.T) {
	t.Parallel()

	req := gwproto.MessageRequest{
		Channel:             "gateway",
		Text:                "hello",
		RequestSystemPrompt: "Use the active persona for tone.",
	}

	summary, err := SummarizeRequest(nil, req)
	require.NoError(t, err)
	require.Equal(t, "hello", summary.Text)
	require.Equal(
		t,
		"Use the active persona for tone.",
		summary.RequestSystemPrompt,
	)
}

func TestSafeComponent_SanitizesAndTruncates(t *testing.T) {
	t.Parallel()

	require.Equal(t, "a_b_c", safeComponent(" a/b:c "))
	require.Equal(t, "", safeComponent("._-"))

	raw := strings.Repeat("a", maxSafeComponentLen+10)
	out := safeComponent(raw)
	require.Len(t, out, maxSafeComponentLen)
}

func TestRecorder_NewTraceDir_TruncatesBase(t *testing.T) {
	t.Parallel()

	rec := &Recorder{
		dir:  t.TempDir(),
		mode: modeFull,
		now:  time.Now,
	}
	now := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)

	channel := strings.Repeat("a", maxSafeComponentLen+10)
	req := strings.Repeat("b", maxSafeComponentLen+10)
	dir, err := rec.newTraceDir(now, TraceStart{
		Channel:   channel,
		RequestID: req,
	})
	require.NoError(t, err)

	base := filepath.Base(dir)
	require.LessOrEqual(t, len(base), maxTraceBaseLen)
}

func TestRecorder_Start_WritesSessionIndex(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	rec := &Recorder{
		dir:  t.TempDir(),
		mode: modeSafe,
		now:  func() time.Time { return now },
	}
	trace, err := rec.Start(TraceStart{
		Channel:   "telegram",
		UserID:    "7602183958",
		SessionID: "telegram:dm:7602183958",
		MessageID: "137",
		RequestID: "telegram:7602183958:137",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, trace.Close(TraceEnd{Status: "ok"}))
	})

	refPath := filepath.Join(
		rec.dir,
		defaultBySessionDir,
		"telegram_dm_7602183958",
		"20260305",
		"090000_137",
		traceRefName,
	)
	raw, err := os.ReadFile(refPath)
	require.NoError(t, err)

	var ref traceRef
	require.NoError(t, json.Unmarshal(raw, &ref))
	require.Equal(t, "telegram", ref.Channel)
	require.Equal(t, "telegram:dm:7602183958", ref.SessionID)
	require.Equal(t, "telegram:7602183958:137", ref.RequestID)
	require.Equal(t, "137", ref.MessageID)

	target := filepath.Clean(
		filepath.Join(filepath.Dir(refPath), ref.TraceDir),
	)
	require.Equal(t, trace.Dir(), target)

	require.NoError(t, trace.SetTraceID("trace-123"))

	raw, err = os.ReadFile(refPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &ref))
	require.Equal(t, "trace-123", ref.TraceID)

	metaRaw, err := os.ReadFile(filepath.Join(trace.Dir(), metaFileName))
	require.NoError(t, err)
	require.Contains(t, string(metaRaw), "\"trace_id\": \"trace-123\"")
}

func TestTrace_SetTraceID_WithoutSessionIndex(t *testing.T) {
	t.Parallel()

	rec := &Recorder{
		dir:  t.TempDir(),
		mode: modeSafe,
		now:  func() time.Time { return time.Now().UTC() },
	}
	trace, err := rec.Start(TraceStart{
		Channel: "telegram",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, trace.Close(TraceEnd{Status: "ok"}))
	})

	require.Empty(t, trace.traceRef)
	require.NoError(t, trace.SetTraceID("trace-456"))
	require.NoError(t, trace.SetTraceID("trace-456"))
	require.NoError(t, trace.SetTraceID(""))

	metaRaw, err := os.ReadFile(filepath.Join(trace.Dir(), metaFileName))
	require.NoError(t, err)
	require.Contains(t, string(metaRaw), "\"trace_id\": \"trace-456\"")
}

func TestTrace_SetTraceID_RetriesAfterTraceRefWriteFailure(t *testing.T) {
	t.Parallel()

	rec, err := New(t.TempDir(), modeSafe)
	require.NoError(t, err)

	trace, err := rec.Start(TraceStart{
		Channel:   "telegram",
		SessionID: "telegram:dm:7602183958",
		RequestID: "telegram:7602183958:137",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, trace.Close(TraceEnd{Status: "ok"}))
	})

	require.NoError(t, os.WriteFile(trace.traceRef, []byte("{"), 0o600))

	err = trace.SetTraceID("trace-789")
	require.Error(t, err)
	require.Empty(t, trace.traceID)

	metaRaw, err := os.ReadFile(filepath.Join(trace.Dir(), metaFileName))
	require.NoError(t, err)
	require.Contains(t, string(metaRaw), "\"trace_id\": \"trace-789\"")

	require.NoError(t, os.WriteFile(
		trace.traceRef,
		[]byte("{}"),
		0o600,
	))
	require.NoError(t, trace.SetTraceID("trace-789"))
	require.Equal(t, "trace-789", trace.traceID)
}

func TestWriteTraceIDJSON_GuardsAndErrors(t *testing.T) {
	t.Parallel()

	require.NoError(t, writeTraceIDJSON("", "trace-1"))
	require.NoError(t, writeTraceIDJSON("ignored", ""))

	path := filepath.Join(t.TempDir(), "meta.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	err := writeTraceIDJSON(path, "trace-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal json")
}
