//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAudioTranscriptionExt(t *testing.T) {
	t.Parallel()

	ext, err := audioTranscriptionExt(audioFormatWAV)
	require.NoError(t, err)
	require.Equal(t, ".wav", ext)

	ext, err = audioTranscriptionExt(audioFormatMP3)
	require.NoError(t, err)
	require.Equal(t, ".mp3", ext)

	_, err = audioTranscriptionExt("ogg")
	require.Error(t, err)
}

func TestWhisperCLITranscriberTranscribe(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fake-whisper.sh")
	script := `#!/bin/sh
outdir=""
while [ $# -gt 0 ]; do
	if [ "$1" = "--output_dir" ]; then
		outdir="$2"
		break
	fi
	shift
done
printf 'merge page 2 and page 4\n' > "$outdir/audio.txt"
`
	require.NoError(
		t,
		os.WriteFile(scriptPath, []byte(script), 0o700),
	)

	transcriber := &whisperCLITranscriber{bin: scriptPath}
	transcript, err := transcriber.Transcribe(
		context.Background(),
		&model.Audio{
			Data:   []byte(strings.Repeat("a", 2048)),
			Format: audioFormatWAV,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "merge page 2 and page 4", transcript)
}

func TestWhisperCLITranscriberTranscribeTooSmall(t *testing.T) {
	t.Parallel()

	transcriber := &whisperCLITranscriber{bin: whisperBinaryName}
	_, err := transcriber.Transcribe(
		context.Background(),
		&model.Audio{
			Data:   []byte("tiny"),
			Format: audioFormatWAV,
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too small")
}
