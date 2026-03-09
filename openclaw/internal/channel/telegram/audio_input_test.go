//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telegram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

const fakeFFmpegName = "ffmpeg"

func TestLooksConvertibleAudio(t *testing.T) {
	t.Parallel()

	require.True(
		t,
		looksConvertibleAudio("voice.m4a", "", "audio/mp4"),
	)
	require.True(
		t,
		looksConvertibleAudio(
			"",
			"voice.oga",
			"application/octet-stream",
		),
	)
	require.False(
		t,
		looksConvertibleAudio("note.txt", "", "text/plain"),
	)
}

func TestDefaultAudioInputConverter_ReturnsSupportedAudio(t *testing.T) {
	t.Parallel()

	got, err := defaultAudioInputConverter(
		context.Background(),
		audioInputSource{
			Name:     "note.mp3",
			MimeType: "audio/mpeg",
			Data:     []byte("mp3"),
		},
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, audioFormatMP3, got.Format)
	require.Equal(t, []byte("mp3"), got.Data)
}

func TestDefaultAudioInputConverter_UsesFFmpegForM4A(t *testing.T) {
	dir := t.TempDir()
	writeFakeFFmpeg(
		t,
		dir,
		"for last do :; done\nprintf 'wavdata' > \"$last\"\n",
	)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := defaultAudioInputConverter(
		context.Background(),
		audioInputSource{
			Name:     "recording.m4a",
			MimeType: "audio/mp4",
			Data:     []byte("m4a"),
		},
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, audioFormatWAV, got.Format)
	require.Equal(t, []byte("wavdata"), got.Data)
}

func TestDefaultAudioInputConverter_IgnoresFailedFFmpeg(t *testing.T) {
	dir := t.TempDir()
	writeFakeFFmpeg(t, dir, "exit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := defaultAudioInputConverter(
		context.Background(),
		audioInputSource{
			Name:     "recording.m4a",
			MimeType: "audio/mp4",
			Data:     []byte("m4a"),
		},
	)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestChannelAudioModelPart_HandlesConverterBranches(t *testing.T) {
	t.Parallel()

	ch := &Channel{}
	part := ch.audioModelPart(
		context.Background(),
		"voice.mp3",
		"",
		"audio/mpeg",
		[]byte("mp3"),
	)
	require.NotNil(t, part)
	require.Equal(t, gwproto.PartTypeAudio, part.Type)
	require.Equal(t, audioFormatMP3, part.Audio.Format)

	ch.audioInputConverter = func(
		_ context.Context,
		_ audioInputSource,
	) (*convertedAudio, error) {
		return nil, errors.New("boom")
	}
	require.Nil(
		t,
		ch.audioModelPart(
			context.Background(),
			"recording.m4a",
			"",
			"audio/mp4",
			[]byte("m4a"),
		),
	)

	ch.audioInputConverter = func(
		_ context.Context,
		_ audioInputSource,
	) (*convertedAudio, error) {
		return &convertedAudio{
			Data:   []byte("bad"),
			Format: "",
		}, nil
	}
	require.Nil(
		t,
		ch.audioModelPart(
			context.Background(),
			"recording.m4a",
			"",
			"audio/mp4",
			[]byte("m4a"),
		),
	)
}

func writeFakeFFmpeg(t *testing.T, dir string, body string) {
	t.Helper()

	script := "#!/bin/sh\n" + body
	path := filepath.Join(dir, fakeFFmpegName)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
}
