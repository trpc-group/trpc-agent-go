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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	whisperBinaryName = "whisper"

	audioTranscriptionTempPrefix = "openclaw-audio-transcription-"
	audioTranscriptionInputStem  = "audio"
	audioTranscriptionOutputExt  = ".txt"
	audioTranscriptionTask       = "transcribe"
	audioTranscriptionFP16False  = "False"

	audioTranscriptionFileMode = 0o600

	defaultAudioTranscriptionTimeout = 45 * time.Second
	minAudioTranscriptionBytes       = 1024
)

type audioTranscriber interface {
	Transcribe(
		ctx context.Context,
		audio *model.Audio,
	) (string, error)
}

type whisperCLITranscriber struct {
	bin string
}

func newDefaultAudioTranscriber() audioTranscriber {
	bin, err := exec.LookPath(whisperBinaryName)
	if err != nil {
		return nil
	}
	return &whisperCLITranscriber{bin: bin}
}

func (t *whisperCLITranscriber) Transcribe(
	ctx context.Context,
	audio *model.Audio,
) (string, error) {
	if t == nil || strings.TrimSpace(t.bin) == "" {
		return "", errors.New("missing whisper binary")
	}
	if audio == nil {
		return "", errors.New("missing audio")
	}
	if len(audio.Data) < minAudioTranscriptionBytes {
		return "", errors.New("audio too small to transcribe")
	}

	ext, err := audioTranscriptionExt(audio.Format)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp(
		"",
		audioTranscriptionTempPrefix,
	)
	if err != nil {
		return "", fmt.Errorf(
			"create audio transcription temp dir: %w",
			err,
		)
	}
	defer os.RemoveAll(dir)

	inputPath := filepath.Join(
		dir,
		audioTranscriptionInputStem+ext,
	)
	if err := os.WriteFile(
		inputPath,
		audio.Data,
		audioTranscriptionFileMode,
	); err != nil {
		return "", fmt.Errorf(
			"write audio transcription input: %w",
			err,
		)
	}

	cmd := exec.CommandContext(
		ctx,
		t.bin,
		inputPath,
		"--output_format",
		strings.TrimPrefix(audioTranscriptionOutputExt, "."),
		"--output_dir",
		dir,
		"--task",
		audioTranscriptionTask,
		"--fp16",
		audioTranscriptionFP16False,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"transcribe audio with whisper: %w (%s)",
			err,
			strings.TrimSpace(string(output)),
		)
	}

	transcriptPath := filepath.Join(
		dir,
		audioTranscriptionInputStem+audioTranscriptionOutputExt,
	)
	transcriptBytes, err := os.ReadFile(transcriptPath)
	if err != nil {
		return "", fmt.Errorf(
			"read audio transcription output: %w",
			err,
		)
	}

	transcript := strings.TrimSpace(string(transcriptBytes))
	if transcript == "" {
		return "", errors.New("empty audio transcript")
	}
	return transcript, nil
}

func audioTranscriptionExt(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case audioFormatWAV:
		return ".wav", nil
	case audioFormatMP3:
		return ".mp3", nil
	default:
		return "", fmt.Errorf(
			"unsupported audio transcription format: %s",
			format,
		)
	}
}
