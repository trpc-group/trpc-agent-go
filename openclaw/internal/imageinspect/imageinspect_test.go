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

func TestInspectResolvesRelativeBasenameInAllowedDirs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	nested := filepath.Join(dir, "uploads")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	path := filepath.Join(nested, "attachment.png")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(
		file,
		image.NewRGBA(image.Rect(0, 0, 3, 2)),
	))
	require.NoError(t, file.Close())

	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	ocr := false
	got, err := tool.inspect(context.Background(), inspectRequest{
		Path: "attachment.png",
		OCR:  &ocr,
	})
	require.NoError(t, err)
	require.Equal(t, path, got.Path)
	require.Equal(t, 3, got.Width)
	require.Equal(t, 2, got.Height)
}

func TestInspectPreprocessesScaleThresholdAndInvert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.White)
		}
	}
	img.Set(0, 0, color.Black)
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(file, img))
	require.NoError(t, file.Close())

	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	ocr := false
	threshold := 128
	got, err := tool.inspect(context.Background(), inspectRequest{
		Path:       path,
		OCR:        &ocr,
		Scale:      2,
		Threshold:  &threshold,
		Invert:     true,
		ASCII:      true,
		ASCIIWidth: 8,
	})
	require.NoError(t, err)
	require.Equal(t, 4, got.Width)
	require.Equal(t, 2, got.Height)
	require.NotEmpty(t, got.ASCII)
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

func TestInspectRejectsRelativeEscapeFromAllowedDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	other := filepath.Join(root, "other")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(other, 0o755))
	path := filepath.Join(other, "outside.png")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(
		file,
		image.NewRGBA(image.Rect(0, 0, 1, 1)),
	))
	require.NoError(t, file.Close())

	tool, err := newInspector(Config{AllowedDirs: []string{allowed}})
	require.NoError(t, err)
	ocr := false
	_, err = tool.inspect(context.Background(), inspectRequest{
		Path: "../other/outside.png",
		OCR:  &ocr,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside allowed_dirs")
}

func TestInspectRejectsSymlinkEscapeFromAllowedDirs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	other := filepath.Join(root, "other")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(other, 0o755))
	outside := filepath.Join(other, "outside.png")
	file, err := os.Create(outside)
	require.NoError(t, err)
	require.NoError(t, png.Encode(
		file,
		image.NewRGBA(image.Rect(0, 0, 1, 1)),
	))
	require.NoError(t, file.Close())
	link := filepath.Join(allowed, "link.png")
	require.NoError(t, os.Symlink(outside, link))

	tool, err := newInspector(Config{AllowedDirs: []string{allowed}})
	require.NoError(t, err)
	ocr := false
	_, err = tool.inspect(context.Background(), inspectRequest{
		Path: link,
		OCR:  &ocr,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside allowed_dirs")

	_, err = tool.inspect(context.Background(), inspectRequest{
		Path: "link.png",
		OCR:  &ocr,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside allowed_dirs")
}
