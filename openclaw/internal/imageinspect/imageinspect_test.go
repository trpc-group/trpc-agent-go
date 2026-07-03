//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package imageinspect

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewToolRequiresFileScope(t *testing.T) {
	t.Parallel()

	_, err := NewTool(Config{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires allowed_dirs")
}

func TestInspectImageMetadataCropAndASCII(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	img := image.NewRGBA(image.Rect(0, 0, 10, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, color.White)
		}
	}
	img.Set(4, 2, color.Black)
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(file, img))
	require.NoError(t, file.Close())

	tool, err := newInspector(Config{
		AllowedDirs: []string{dir},
		Timeout:     time.Second,
	})
	require.NoError(t, err)

	ocr := false
	got, err := tool.inspect(context.Background(), inspectRequest{
		Path:       path,
		OCR:        &ocr,
		ASCII:      true,
		ASCIIWidth: 8,
		Crop: &cropRequest{
			X:      2,
			Y:      1,
			Width:  6,
			Height: 4,
		},
	})
	require.NoError(t, err)
	require.Equal(t, "png", got.Format)
	require.Equal(t, 10, got.Width)
	require.Equal(t, 6, got.Height)
	require.Equal(t, &cropResponse{
		X:      2,
		Y:      1,
		Width:  6,
		Height: 4,
	}, got.Crop)
	require.NotEmpty(t, got.ASCII)
	require.Empty(t, got.OCRText)
}

func TestInspectRejectsPathOutsideAllowedDirs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	other := t.TempDir()
	path := filepath.Join(other, "outside.png")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(
		file,
		image.NewRGBA(image.Rect(0, 0, 1, 1)),
	))
	require.NoError(t, file.Close())

	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	ocr := false
	_, err = tool.inspect(context.Background(), inspectRequest{
		Path: path,
		OCR:  &ocr,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside allowed_dirs")
}
