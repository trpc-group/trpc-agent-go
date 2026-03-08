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

func TestFallbackMediaFilename(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"voice.ogg",
		fallbackMediaFilename(
			"",
			"voice/file_11.ogg",
			defaultVoiceName,
			mimeAudioOGG,
		),
	)
	require.Equal(
		t,
		"video-note.mp4",
		fallbackMediaFilename(
			"",
			"videos/file_10.mp4",
			defaultVideoNoteName,
			mimeVideoMP4,
		),
	)
	require.Equal(
		t,
		"clip.mp4",
		fallbackMediaFilename(
			"",
			"videos/clip.mp4",
			defaultVideoName,
			mimeVideoMP4,
		),
	)
	require.Equal(
		t,
		"custom.mov",
		fallbackMediaFilename(
			"custom.mov",
			"videos/file_10.mp4",
			defaultVideoName,
			mimeVideoMP4,
		),
	)
}

func TestFallbackDocumentFilename(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"video.mp4",
		fallbackDocumentFilename(
			"",
			"videos/file_10.mp4",
			mimeVideoMP4,
		),
	)
	require.Equal(
		t,
		"document.pdf",
		fallbackDocumentFilename(
			"",
			"docs/file_12.pdf",
			"application/pdf",
		),
	)
	require.Equal(
		t,
		"scan.jpg",
		fallbackDocumentFilename(
			"scan.jpg",
			"docs/file_13.jpg",
			mimeImageJPEG,
		),
	)
}

func TestLooksGeneratedTelegramFileName(t *testing.T) {
	t.Parallel()

	require.True(t, looksGeneratedTelegramFileName("file_11.ogg"))
	require.True(t, looksGeneratedTelegramFileName("file_9"))
	require.False(t, looksGeneratedTelegramFileName("file_name.ogg"))
	require.False(t, looksGeneratedTelegramFileName("voice.ogg"))
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

func TestMediaHelperMappings(t *testing.T) {
	t.Parallel()

	require.Equal(t, mimeImageJPEG, mimeTypeForImageFormat(" jpg "))
	require.Equal(t, mimeImagePNG, mimeTypeForImageFormat("png"))
	require.Equal(t, mimeAudioMPEG, mimeTypeForAudioFormat(audioFormatMP3))
	require.Equal(t, mimeAudioWAV, mimeTypeForAudioFormat(audioFormatWAV))
	require.Empty(t, mimeTypeForAudioFormat("ogg"))

	require.Equal(
		t,
		attachmentTooLargeMsg,
		attachmentTooLargeMessage(0),
	)
	require.Equal(t, "2 MiB", formatByteLimit(2*bytesPerMiB))
	require.Equal(t, "3 KiB", formatByteLimit(3*bytesPerKiB))
	require.Equal(t, "777 bytes", formatByteLimit(777))
	require.Contains(
		t,
		attachmentTooLargeMessage(2*bytesPerMiB),
		"2 MiB",
	)
}

func TestMediaHelperFallbacksAndErrors(t *testing.T) {
	t.Parallel()

	userErr := &userError{
		userMessage: " user-visible ",
		err:         errors.New("boom"),
	}
	require.Equal(t, "user-visible", userMessageFromErr(userErr))
	require.Empty(t, userMessageFromErr(errors.New("plain")))

	require.Equal(
		t,
		defaultAnimationName,
		documentFallbackBase("clip.gif", ""),
	)
	require.Equal(
		t,
		defaultPhotoName,
		documentFallbackBase("clip.png", ""),
	)
	require.Equal(
		t,
		defaultVideoName,
		documentFallbackBase("clip.mp4", ""),
	)
	require.Equal(
		t,
		defaultAudioName,
		documentFallbackBase("clip.wav", ""),
	)
	require.Equal(
		t,
		defaultDocumentName,
		documentFallbackBase("clip.bin", "application/octet-stream"),
	)

	require.Equal(t, ".ogg", mediaExtFromPathOrMIME("voice.oga", ""))
	require.Equal(t, ".mp3", mediaExtFromPathOrMIME("", mimeAudioMPEG))
	require.Equal(t, ".wav", mediaExtFromPathOrMIME("", mimeAudioWAV))
	require.Equal(t, ".mp4", mediaExtFromPathOrMIME("", mimeVideoMP4))
	require.Equal(t, ".gif", mediaExtFromPathOrMIME("", mimeImageGIF))
	require.Empty(t, mediaExtFromPathOrMIME("", ""))

	require.Equal(
		t,
		"video/mp4",
		normalizeMediaMIME("clip.mp4", "", ""),
	)
	require.Equal(
		t,
		mimeImagePNG,
		normalizeMediaMIME("", "frame.png", ""),
	)
	require.Equal(
		t,
		"text/plain",
		normalizeMediaMIME("", "", "text/plain"),
	)
	require.True(t, isVideoMedia("clip.mp4", "", ""))
	require.True(t, isVideoMedia("", "clip.mov", ""))
	require.True(t, isVideoMedia("", "", "video/webm"))
	require.False(t, isVideoMedia("note.txt", "", ""))
}

func TestMapDownloadError(t *testing.T) {
	t.Parallel()

	err := mapDownloadError(tgapi.ErrFileTooLarge, 4*bytesPerMiB)
	u, ok := err.(*userError)
	require.True(t, ok)
	require.Equal(
		t,
		attachmentTooLargeMessage(4*bytesPerMiB),
		u.userMessage,
	)
	require.True(t, errors.Is(err, tgapi.ErrFileTooLarge))

	other := errors.New("boom")
	err = mapDownloadError(other, 4*bytesPerMiB)
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
	require.Len(t, parts, 2)
	require.Equal(t, gwproto.PartTypeImage, parts[0].Type)
	require.NotNil(t, parts[0].Image)
	require.Equal(t, "png", parts[0].Image.Format)
	require.Equal(t, gwproto.PartTypeFile, parts[1].Type)
	require.NotNil(t, parts[1].File)
	require.Equal(t, defaultPhotoName+".png", parts[1].File.Filename)
	require.Equal(t, mimeImagePNG, parts[1].File.Format)
	require.Equal(t, pngHeader, parts[1].File.Data)
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
	require.Equal(t, attachmentTooLargeMessage(9), u.userMessage)
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

func TestAppendAnimationPart_BuildsVideoPart(t *testing.T) {
	t.Parallel()

	data := []byte("anim")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "animations/clip.gif"},
				data: data,
			},
		},
	}
	ch := &Channel{bot: bot}

	parts, err := ch.appendAnimationPart(
		context.Background(),
		nil,
		&tgapi.Animation{
			FileID:   testFileID,
			FileName: "clip.gif",
			MimeType: "image/gif",
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeVideo, parts[0].Type)
	require.Equal(t, "clip.gif", parts[0].File.Filename)
}

func TestAppendVideoNotePart_BuildsVideoPart(t *testing.T) {
	t.Parallel()

	data := []byte("note")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "videos/note.mp4"},
				data: data,
			},
		},
	}
	ch := &Channel{bot: bot}

	parts, err := ch.appendVideoNotePart(
		context.Background(),
		nil,
		&tgapi.VideoNote{
			FileID:   testFileID,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeVideo, parts[0].Type)
	require.Equal(t, "note.mp4", parts[0].File.Filename)
}

func TestAppendVideoNotePart_UsesFriendlyFallbackName(t *testing.T) {
	t.Parallel()

	data := []byte("note")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "videos/file_10.mp4"},
				data: data,
			},
		},
	}
	ch := &Channel{bot: bot}

	parts, err := ch.appendVideoNotePart(
		context.Background(),
		nil,
		&tgapi.VideoNote{
			FileID:   testFileID,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, "video-note.mp4", parts[0].File.Filename)
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

func TestAppendVoicePart_UsesFriendlyFallbackName(t *testing.T) {
	t.Parallel()

	data := []byte("voice")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "voice/file_11.oga"},
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
	require.Equal(t, "voice.ogg", parts[0].File.Filename)
}

func TestAppendVoicePart_ConvertsVoiceForModel(t *testing.T) {
	t.Parallel()

	data := []byte("voice")
	converted := []byte("wavdata")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "voice/file_11.oga"},
				data: data,
			},
		},
	}
	ch := &Channel{
		bot: bot,
		audioInputConverter: func(
			_ context.Context,
			src audioInputSource,
		) (*convertedAudio, error) {
			require.Equal(t, "voice.ogg", src.Name)
			require.Equal(t, mimeAudioOGG, src.MimeType)
			require.Equal(t, data, src.Data)
			return &convertedAudio{
				Data:   converted,
				Format: audioFormatWAV,
			}, nil
		},
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
	require.Len(t, parts, 2)
	require.Equal(t, gwproto.PartTypeAudio, parts[0].Type)
	require.Equal(t, audioFormatWAV, parts[0].Audio.Format)
	require.Equal(t, converted, parts[0].Audio.Data)
	require.Equal(t, gwproto.PartTypeFile, parts[1].Type)
	require.Equal(t, "voice.ogg", parts[1].File.Filename)
	require.Equal(t, mimeAudioOGG, parts[1].File.Format)
	require.Equal(t, data, parts[1].File.Data)
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

func TestAppendAudioPart_ConvertsUnsupportedAudioForModel(t *testing.T) {
	t.Parallel()

	data := []byte("m4adata")
	converted := []byte("wavdata")

	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "audio/recording.m4a"},
				data: data,
			},
		},
	}
	ch := &Channel{
		bot: bot,
		audioInputConverter: func(
			_ context.Context,
			src audioInputSource,
		) (*convertedAudio, error) {
			require.Equal(t, "recording.m4a", src.Name)
			require.Equal(t, "audio/mp4", src.MimeType)
			require.Equal(t, data, src.Data)
			return &convertedAudio{
				Data:   converted,
				Format: audioFormatWAV,
			}, nil
		},
	}

	parts, err := ch.appendAudioPart(
		context.Background(),
		nil,
		&tgapi.Audio{
			FileID:   testFileID,
			FileName: "recording.m4a",
			MimeType: "audio/mp4",
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	require.Equal(t, gwproto.PartTypeAudio, parts[0].Type)
	require.Equal(t, audioFormatWAV, parts[0].Audio.Format)
	require.Equal(t, converted, parts[0].Audio.Data)
	require.Equal(t, gwproto.PartTypeFile, parts[1].Type)
	require.Equal(t, "recording.m4a", parts[1].File.Filename)
	require.Equal(t, "audio/mp4", parts[1].File.Format)
	require.Equal(t, data, parts[1].File.Data)
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
	require.Equal(t, attachmentTooLargeMessage(9), u.userMessage)
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

func TestAppendDocumentPart_BuildsImageAndFileParts(t *testing.T) {
	t.Parallel()

	data := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
	}
	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "docs/file_13.png"},
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
			MimeType: mimeImagePNG,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	require.Equal(t, gwproto.PartTypeImage, parts[0].Type)
	require.Equal(t, gwproto.PartTypeFile, parts[1].Type)
	require.Equal(t, "photo.png", parts[1].File.Filename)
	require.Equal(t, mimeImagePNG, parts[1].File.Format)
}

func TestAppendDocumentPart_BuildsAudioAndFileParts(t *testing.T) {
	t.Parallel()

	data := []byte("mp3data")
	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "docs/briefing.mp3"},
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
			MimeType: mimeAudioMP3,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	require.Equal(t, gwproto.PartTypeAudio, parts[0].Type)
	require.Equal(t, audioFormatMP3, parts[0].Audio.Format)
	require.Equal(t, gwproto.PartTypeFile, parts[1].Type)
	require.Equal(t, "briefing.mp3", parts[1].File.Filename)
	require.Equal(t, mimeAudioMP3, parts[1].File.Format)
}

func TestAppendDocumentPart_UsesFriendlyMediaFallbackName(t *testing.T) {
	t.Parallel()

	data := []byte("video")
	bot := &stubBot{
		downloads: map[string]stubDownload{
			testFileID: {
				file: tgapi.File{FilePath: "docs/file_10.mp4"},
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
			MimeType: mimeVideoMP4,
			FileSize: int64(len(data)),
		},
		int64(len(data)),
	)
	require.NoError(t, err)
	require.Len(t, parts, 1)
	require.Equal(t, gwproto.PartTypeVideo, parts[0].Type)
	require.Equal(t, "video.mp4", parts[0].File.Filename)
	require.Equal(t, mimeVideoMP4, parts[0].File.Format)
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
	require.Len(t, parts, 2)
	require.Equal(t, gwproto.PartTypeAudio, parts[0].Type)
	require.NotNil(t, parts[0].Audio)
	require.Equal(t, audioFormatMP3, parts[0].Audio.Format)
	require.Equal(t, data, parts[0].Audio.Data)
	require.Equal(t, gwproto.PartTypeFile, parts[1].Type)
	require.NotNil(t, parts[1].File)
	require.Equal(t, "song.mp3", parts[1].File.Filename)
	require.Equal(t, mimeAudioMP3, parts[1].File.Format)
	require.Equal(t, data, parts[1].File.Data)

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
		require.Equal(t, "song.mp3", att.Name)
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
		"telegram:dm:1",
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
		"telegram:dm:1",
		"rid",
		tgapi.Message{MessageID: 3},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty message")
}
