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
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
)

const (
	ffmpegBinaryName = "ffmpeg"

	convertedAudioPrefix = "openclaw-telegram-audio-"
	convertedAudioName   = "converted.wav"
	convertedAudioRate   = "16000"

	convertedAudioFileMode = 0o600
)

type audioInputSource struct {
	Name     string
	Path     string
	MimeType string
	Data     []byte
}

type convertedAudio struct {
	Data   []byte
	Format string
}

type audioInputConverter func(
	ctx context.Context,
	src audioInputSource,
) (*convertedAudio, error)

func defaultAudioInputConverter(
	ctx context.Context,
	src audioInputSource,
) (*convertedAudio, error) {
	format := inferAudioFormat(src.Name, src.Path, src.MimeType)
	if isSupportedAudioFormat(format) {
		return &convertedAudio{
			Data:   src.Data,
			Format: format,
		}, nil
	}
	if !looksConvertibleAudio(src.Name, src.Path, src.MimeType) {
		return nil, nil
	}
	if _, err := exec.LookPath(ffmpegBinaryName); err != nil {
		return nil, nil
	}
	return convertAudioInputWithFFmpeg(ctx, src)
}

func looksConvertibleAudio(
	name string,
	filePath string,
	mimeType string,
) bool {
	normalized := strings.ToLower(
		strings.TrimSpace(
			normalizeMediaMIME(name, filePath, mimeType),
		),
	)
	if strings.HasPrefix(normalized, mimePrefixAudio) {
		return true
	}
	switch strings.ToLower(
		mediaExtFromPathOrMIME(filePath, normalized),
	) {
	case ".m4a", ".ogg", ".oga":
		return true
	default:
		return false
	}
}

func convertAudioInputWithFFmpeg(
	ctx context.Context,
	src audioInputSource,
) (*convertedAudio, error) {
	dir, err := os.MkdirTemp("", convertedAudioPrefix)
	if err != nil {
		return nil, fmt.Errorf("telegram: create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	inputName := fallbackMediaFilename(
		src.Name,
		src.Path,
		defaultAudioName,
		src.MimeType,
	)
	if path.Ext(inputName) == "" {
		inputName += mediaExtFromPathOrMIME(src.Path, src.MimeType)
	}
	inputName = sanitizeFileToken(filepath.Base(inputName))
	if path.Ext(inputName) == "" {
		inputName += ".bin"
	}

	inputPath := filepath.Join(dir, inputName)
	outputPath := filepath.Join(dir, convertedAudioName)
	if err := os.WriteFile(
		inputPath,
		src.Data,
		convertedAudioFileMode,
	); err != nil {
		return nil, fmt.Errorf("telegram: write temp audio: %w", err)
	}

	cmd := exec.CommandContext(
		ctx,
		ffmpegBinaryName,
		"-nostdin",
		"-hide_banner",
		"-loglevel",
		"error",
		"-y",
		"-i",
		inputPath,
		"-vn",
		"-ac",
		"1",
		"-ar",
		convertedAudioRate,
		"-f",
		"wav",
		outputPath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.DebugfContext(
			ctx,
			"telegram: audio input conversion skipped: %v (%s)",
			err,
			strings.TrimSpace(string(output)),
		)
		return nil, nil
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf(
			"telegram: read converted audio: %w",
			err,
		)
	}
	return &convertedAudio{
		Data:   data,
		Format: audioFormatWAV,
	}, nil
}

func (c *Channel) audioModelPart(
	ctx context.Context,
	name string,
	filePath string,
	mimeType string,
	data []byte,
) *gwproto.ContentPart {
	if format := inferAudioFormat(name, filePath, mimeType); format != "" {
		return &gwproto.ContentPart{
			Type: gwproto.PartTypeAudio,
			Audio: &gwproto.AudioPart{
				Data:   data,
				Format: format,
			},
		}
	}
	if c == nil || c.audioInputConverter == nil {
		return nil
	}
	converted, err := c.audioInputConverter(ctx, audioInputSource{
		Name:     name,
		Path:     filePath,
		MimeType: mimeType,
		Data:     data,
	})
	if err != nil {
		log.WarnfContext(
			ctx,
			"telegram: prepare audio input %q: %v",
			name,
			err,
		)
		return nil
	}
	if converted == nil || len(converted.Data) == 0 ||
		!isSupportedAudioFormat(converted.Format) {
		return nil
	}
	return &gwproto.ContentPart{
		Type: gwproto.PartTypeAudio,
		Audio: &gwproto.AudioPart{
			Data:   converted.Data,
			Format: converted.Format,
		},
	}
}
