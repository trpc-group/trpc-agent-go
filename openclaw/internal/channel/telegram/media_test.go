//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	testFileID = "file_1"

	contentTypeJPG  = "image/jpg"
	contentTypeText = "text/plain"

	mimeAudioMP3 = "audio/mpeg"
	mimeAudioWAV = "audio/wav"
	mimeAudioOGG = "audio/ogg"

	debugEventsFileName = "events.jsonl"
)

type debugEventRecord struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func TestUserError_ErrorAndUnwrap(t *testing.T) {
	t.Parallel()

	var nilErr *userError
	require.Equal(t, "", nilErr.Error())

	base := errors.New("base")
	e := &userError{
		userMessage: downloadFailedMessage,
		err:         base,
	}
	require.Equal(t, "base", e.Error())
	require.Equal(t, base, e.Unwrap())
	require.True(t, errors.Is(e, base))
}

func TestWithMaxDownloadBytes_AppliesToConfig(t *testing.T) {
	t.Parallel()

	cfg := &config{}
	WithMaxDownloadBytes(123)(cfg)
	require.Equal(t, int64(123), cfg.maxDownloadBytes)
}

func TestJoinMessageText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    string
		b    string
		want string
	}{
		{name: "both-empty", a: "", b: "", want: ""},
		{name: "a-only", a: " a ", b: "", want: "a"},
		{name: "b-only", a: "", b: " b ", want: "b"},
		{name: "both", a: "a", b: "b", want: "a\nb"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, joinMessageText(tt.a, tt.b))
		})
	}
}

func TestFallbackFilename(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"primary.txt",
		fallbackFilename(" primary.txt ", "x/y", "fallback"),
	)
	require.Equal(
		t,
		"doc.pdf",
		fallbackFilename("", "docs/doc.pdf", "fallback"),
	)
	require.Equal(
		t,
		"fallback",
		fallbackFilename("", "", "fallback"),
	)
	require.Equal(
		t,
		"fallback",
		fallbackFilename("", ".", "fallback"),
	)
	require.Equal(
		t,
		"fallback",
		fallbackFilename("", "/", "fallback"),
	)
}

func TestImageFormatFromContentType(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"jpeg",
		imageFormatFromContentType(contentTypeJPG),
	)
	require.Equal(
		t,
		"",
		imageFormatFromContentType(contentTypeText),
	)
}

func TestImageFormatFromExt(t *testing.T) {
	t.Parallel()

	require.Equal(t, "jpeg", imageFormatFromExt(".jpeg"))
	require.Equal(t, "gif", imageFormatFromExt(".gif"))
	require.Equal(t, "webp", imageFormatFromExt(".webp"))
	require.Equal(t, "", imageFormatFromExt(".unknown"))
}

func TestInferImageFormat_PrefersExt(t *testing.T) {
	t.Parallel()

	data := []byte("not an image")
	require.Equal(t, "png", inferImageFormat("x.png", data))
}

func TestInferImageFormat_FallsBackToContentType(t *testing.T) {
	t.Parallel()

	pngHeader := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
	}

	require.Equal(
		t,
		"png",
		inferImageFormat("no_ext", pngHeader),
	)
}

func TestInferImageFormat_UnknownReturnsEmpty(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", inferImageFormat("file.unknown", []byte("hello")))
}

func TestInferAudioFormat(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		audioFormatWAV,
		inferAudioFormat("song.wav", "x/y", ""),
	)
	require.Equal(
		t,
		audioFormatMP3,
		inferAudioFormat("", "x.mp3", ""),
	)
	require.Equal(
		t,
		audioFormatMP3,
		inferAudioFormat("", "", mimeAudioMP3),
	)
	require.Equal(
		t,
		audioFormatWAV,
		inferAudioFormat("", "", mimeAudioWAV),
	)
	require.Equal(
		t,
		"",
		inferAudioFormat("", "", mimeAudioOGG),
	)
}

func TestMapDownloadError(t *testing.T) {
	t.Parallel()

	err := mapDownloadError(tgapi.ErrFileTooLarge)
	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(t, attachmentTooLargeMsg, u.userMessage)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))

	other := errors.New("boom")
	err = mapDownloadError(other)
	u, ok = err.(*userError)
	require.True(t, ok)
	require.Equal(t, downloadFailedMessage, u.userMessage)
	require.True(t, errors.Is(err, other))
}

func TestAppendPhotoPart_ContentTypeInference(t *testing.T) {
	t.Parallel()

	pngHeader := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
	}

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "photos/no_ext"},
				data: pngHeader,
			},
		},
	}
	ch := &Channel{
		bot: bot,
	}

	parts, err := ch.appendPhotoPart(
		context.Background(),
		nil,
		[]tgapi.PhotoSize{{FileID: testFileID}},
		int64(len(pngHeader)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeImage, parts[0].Type)
	require.NotNil(t, parts[0].Image)
	require.Equal(t, "png", parts[0].Image.Format)
}

func TestAppendPhotoPart_EmptyAndUnsupported_NoOpOrError(t *testing.T) {
	t.Parallel()

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "files/blob.bin"},
				data: []byte("hello"),
			},
		},
	}
	ch := &Channel{
		bot: bot,
	}

	parts := []gwproto.ContentPart{{Type: gwproto.PartTypeText}}

	out, err := ch.appendPhotoPart(
		context.Background(),
		parts,
		nil,
		10,
	)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	out, err = ch.appendPhotoPart(
		context.Background(),
		parts,
		[]tgapi.PhotoSize{{FileID: " "}},
		10,
	)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	_, err = ch.appendPhotoPart(
		context.Background(),
		nil,
		[]tgapi.PhotoSize{{FileID: testFileID}},
		10,
	)
	require.Error(t, err)
	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(t, unsupportedMediaMsg, u.userMessage)
}

func TestAppendPhotoPart_TooLarge(t *testing.T) {
	t.Parallel()

	bot := &stubBot{
		downloads: map[string]stubDownload{},
	}
	ch := &Channel{
		bot: bot,
	}

	_, err := ch.appendPhotoPart(
		context.Background(),
		nil,
		[]tgapi.PhotoSize{{FileID: testFileID, FileSize: 10}},
		9,
	)
	require.Error(t, err)
	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(t, attachmentTooLargeMsg, u.userMessage)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))

	bot.mu.Lock()
	require.Empty(t, bot.dlCalls)
	bot.mu.Unlock()
}

func TestAppendPhotoPart_RecordsAttachmentInTrace(t *testing.T) {
	t.Parallel()

	data := []byte("img")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "photos/a.png"},
				data: data,
			},
		},
	}
	ch := &Channel{
		bot: bot,
	}

	mode, err := debugrecorder.ParseMode("full")
	require.NoError(t, err)
	rec, err := debugrecorder.New(t.TempDir(), mode)
	require.NoError(t, err)

	trace, err := rec.Start(debugrecorder.TraceStart{
		Channel:   channelID,
		RequestID: "req-1",
	})
	require.NoError(t, err)

	ctx := debugrecorder.WithTrace(context.Background(), trace)
	_, err = ch.appendPhotoPart(
		ctx,
		nil,
		[]tgapi.PhotoSize{{FileID: testFileID}},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.NoError(t, trace.Close(debugrecorder.TraceEnd{Status: "ok"}))

	evs, err := os.Open(filepath.Join(trace.Dir(), debugEventsFileName))
	require.NoError(t, err)
	defer evs.Close()

	scanner := bufio.NewScanner(evs)
	found := false
	for scanner.Scan() {
		var evt debugEventRecord
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &evt))
		if evt.Kind != debugrecorder.KindTelegramAttachment {
			continue
		}

		var att telegramAttachmentSummary
		require.NoError(t, json.Unmarshal(evt.Payload, &att))
		require.Equal(t, attachmentKindPhoto, att.Kind)
		require.Equal(t, testFileID, att.FileID)
		require.NotEmpty(t, att.Blob.SHA256)
		require.NotEmpty(t, att.Blob.Ref)

		dst := filepath.Join(trace.Dir(), att.Blob.Ref)
		got, err := os.ReadFile(dst)
		require.NoError(t, err)
		require.Equal(t, data, got)

		found = true
		break
	}
	require.NoError(t, scanner.Err())
	require.True(t, found)
}

func TestAppendVideoPart_BuildsVideoPart(t *testing.T) {
	t.Parallel()

	data := []byte("video")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "videos/clip.mp4"},
				data: data,
			},
		},
	}
	ch := &Channel{
		bot: bot,
	}

	parts, err := ch.appendVideoPart(
		context.Background(),
		nil,
		&tgapi.Video{
			FileID:   testFileID,
			MimeType: "video/mp4",
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeVideo, parts[0].Type)
	require.NotNil(t, parts[0].File)
	require.Equal(t, "clip.mp4", parts[0].File.Filename)
	require.Equal(t, "video/mp4", parts[0].File.Format)
	require.Equal(t, data, parts[0].File.Data)
}

func TestAppendVideoPart_NilEmptyAndTooLarge(t *testing.T) {
	t.Parallel()

	ch := &Channel{
		bot: &stubBot{},
	}

	parts := []gwproto.ContentPart{{Type: gwproto.PartTypeText}}

	out, err := ch.appendVideoPart(context.Background(), parts, nil, 10)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	out, err = ch.appendVideoPart(
		context.Background(),
		parts,
		&tgapi.Video{FileID: " "},
		10,
	)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	_, err = ch.appendVideoPart(
		context.Background(),
		nil,
		&tgapi.Video{FileID: testFileID, FileSize: 10},
		9,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))
}

func TestAppendVoicePart_BuildsFilePart(t *testing.T) {
	t.Parallel()

	data := []byte("voice")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "voice/voice.ogg"},
				data: data,
			},
		},
	}
	ch := &Channel{
		bot: bot,
	}

	parts, err := ch.appendVoicePart(
		context.Background(),
		nil,
		&tgapi.Voice{
			FileID:   testFileID,
			MimeType: mimeAudioOGG,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeFile, parts[0].Type)
	require.NotNil(t, parts[0].File)
	require.Equal(t, "voice.ogg", parts[0].File.Filename)
	require.Equal(t, mimeAudioOGG, parts[0].File.Format)
	require.Equal(t, data, parts[0].File.Data)
}

func TestAppendVoicePart_NilEmptyAndTooLarge(t *testing.T) {
	t.Parallel()

	ch := &Channel{
		bot: &stubBot{},
	}

	parts := []gwproto.ContentPart{{Type: gwproto.PartTypeText}}

	out, err := ch.appendVoicePart(context.Background(), parts, nil, 10)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	out, err = ch.appendVoicePart(
		context.Background(),
		parts,
		&tgapi.Voice{FileID: " "},
		10,
	)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	_, err = ch.appendVoicePart(
		context.Background(),
		nil,
		&tgapi.Voice{FileID: testFileID, FileSize: 10},
		9,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))
}

func TestAppendAudioPart_UnsupportedFallsBackToFile(t *testing.T) {
	t.Parallel()

	data := []byte("ogg")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "audio/recording.ogg"},
				data: data,
			},
		},
	}
	ch := &Channel{
		bot: bot,
	}

	parts, err := ch.appendAudioPart(
		context.Background(),
		nil,
		&tgapi.Audio{
			FileID:   testFileID,
			FileName: "recording.ogg",
			MimeType: mimeAudioOGG,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeFile, parts[0].Type)
	require.NotNil(t, parts[0].File)
	require.Equal(t, "recording.ogg", parts[0].File.Filename)
	require.Equal(t, mimeAudioOGG, parts[0].File.Format)
	require.Equal(t, data, parts[0].File.Data)
}

func TestAppendAudioPart_NilEmptyAndTooLarge(t *testing.T) {
	t.Parallel()

	ch := &Channel{
		bot: &stubBot{},
	}

	parts := []gwproto.ContentPart{{Type: gwproto.PartTypeText}}

	out, err := ch.appendAudioPart(context.Background(), parts, nil, 10)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	out, err = ch.appendAudioPart(
		context.Background(),
		parts,
		&tgapi.Audio{FileID: " "},
		10,
	)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	_, err = ch.appendAudioPart(
		context.Background(),
		nil,
		&tgapi.Audio{FileID: testFileID, FileSize: 10},
		9,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))
}

func TestAppendDocumentPart_NilAndEmptyID_NoOp(t *testing.T) {
	t.Parallel()

	ch := &Channel{
		bot: &stubBot{},
	}

	parts := []gwproto.ContentPart{{Type: gwproto.PartTypeText}}

	out, err := ch.appendDocumentPart(
		context.Background(),
		parts,
		nil,
		1,
	)
	require.NoError(t, err)
	require.Equal(t, parts, out)

	out, err = ch.appendDocumentPart(
		context.Background(),
		parts,
		&tgapi.Document{FileID: " "},
		1,
	)
	require.NoError(t, err)
	require.Equal(t, parts, out)
}

func TestAppendDocumentPart_TooLarge(t *testing.T) {
	t.Parallel()

	ch := &Channel{
		bot: &stubBot{},
	}

	_, err := ch.appendDocumentPart(
		context.Background(),
		nil,
		&tgapi.Document{
			FileID:   testFileID,
			FileSize: 10,
		},
		9,
	)
	require.Error(t, err)
	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(t, attachmentTooLargeMsg, u.userMessage)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))
}

func TestAppendDocumentPart_Success(t *testing.T) {
	t.Parallel()

	data := []byte("pdf")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "docs/doc.pdf"},
				data: data,
			},
		},
	}
	ch := &Channel{bot: bot}

	parts, err := ch.appendDocumentPart(
		context.Background(),
		nil,
		&tgapi.Document{
			FileID:   testFileID,
			MimeType: "application/pdf",
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeFile, parts[0].Type)
	require.NotNil(t, parts[0].File)
	require.Equal(t, "doc.pdf", parts[0].File.Filename)
	require.Equal(t, "application/pdf", parts[0].File.Format)
	require.Equal(t, data, parts[0].File.Data)
}

func TestAppendDocumentPart_DownloadFails(t *testing.T) {
	t.Parallel()

	base := errors.New("download failed")
	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {err: base},
		},
	}
	ch := &Channel{bot: bot}

	_, err := ch.appendDocumentPart(
		context.Background(),
		nil,
		&tgapi.Document{FileID: testFileID},
		10,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, base))

	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(t, downloadFailedMessage, u.userMessage)
}

func TestAppendVideoPart_DownloadFails(t *testing.T) {
	t.Parallel()

	base := errors.New("download failed")
	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {err: base},
		},
	}
	ch := &Channel{bot: bot}

	_, err := ch.appendVideoPart(
		context.Background(),
		nil,
		&tgapi.Video{FileID: testFileID},
		10,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, base))

	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(t, downloadFailedMessage, u.userMessage)
}

func TestAppendVoicePart_DownloadFails(t *testing.T) {
	t.Parallel()

	base := errors.New("download failed")
	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {err: base},
		},
	}
	ch := &Channel{bot: bot}

	_, err := ch.appendVoicePart(
		context.Background(),
		nil,
		&tgapi.Voice{FileID: testFileID},
		10,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, base))

	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(t, downloadFailedMessage, u.userMessage)
}

func TestAppendAudioPart_MP3BuildsAudioPartAndRecordsTrace(t *testing.T) {
	t.Parallel()

	data := []byte("mp3data")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "audio/raw.bin"},
				data: data,
			},
		},
	}
	ch := &Channel{bot: bot}

	mode, err := debugrecorder.ParseMode("full")
	require.NoError(t, err)

	rec, err := debugrecorder.New(t.TempDir(), mode)
	require.NoError(t, err)

	trace, err := rec.Start(debugrecorder.TraceStart{
		Channel:   channelID,
		RequestID: "req-1",
	})
	require.NoError(t, err)

	ctx := debugrecorder.WithTrace(context.Background(), trace)
	parts, err := ch.appendAudioPart(
		ctx,
		nil,
		&tgapi.Audio{
			FileID:   testFileID,
			FileName: "song.mp3",
			MimeType: mimeAudioMP3,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeAudio, parts[0].Type)
	require.NotNil(t, parts[0].Audio)
	require.Equal(t, audioFormatMP3, parts[0].Audio.Format)
	require.Equal(t, data, parts[0].Audio.Data)

	require.NoError(t, trace.Close(debugrecorder.TraceEnd{Status: "ok"}))

	evs, err := os.Open(filepath.Join(trace.Dir(), debugEventsFileName))
	require.NoError(t, err)
	defer evs.Close()

	scanner := bufio.NewScanner(evs)
	found := false
	for scanner.Scan() {
		var evt debugEventRecord
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &evt))
		if evt.Kind != debugrecorder.KindTelegramAttachment {
			continue
		}

		var att telegramAttachmentSummary
		require.NoError(t, json.Unmarshal(evt.Payload, &att))
		require.Equal(t, attachmentKindAudio, att.Kind)
		require.Equal(t, testFileID, att.FileID)
		require.Equal(t, "audio.mp3", att.Name)
		require.Equal(t, audioFormatMP3, att.Format)
		require.NotEmpty(t, att.Blob.Ref)

		dst := filepath.Join(trace.Dir(), att.Blob.Ref)
		got, err := os.ReadFile(dst)
		require.NoError(t, err)
		require.Equal(t, data, got)

		found = true
		break
	}
	require.NoError(t, scanner.Err())
	require.True(t, found)
}

func TestBuildGatewayRequest_TooLargePropagates(t *testing.T) {
	t.Parallel()

	ch := &Channel{
		bot:              &stubBot{},
		maxDownloadBytes: 9,
	}

	_, err := ch.buildGatewayRequest(
		context.Background(),
		"1",
		"",
		"rid",
		tgapi.Message{
			MessageID: 3,
			Photo: []tgapi.PhotoSize{
				{FileID: testFileID, FileSize: 10},
			},
		},
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))
}

func TestBuildGatewayRequest_EmptyMessageErrors(t *testing.T) {
	t.Parallel()

	ch := &Channel{
		bot: &stubBot{},
	}

	_, err := ch.buildGatewayRequest(
		context.Background(),
		"1",
		"",
		"rid",
		tgapi.Message{MessageID: 3},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty message")
}
