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
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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

func TestNewToolAllowsAllFiles(t *testing.T) {
	t.Parallel()

	got, err := NewTool(Config{AllowAllFiles: true})
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestNewInspectorRejectsMissingAllowedDir(t *testing.T) {
	t.Parallel()

	_, err := newInspector(Config{
		AllowedDirs: []string{filepath.Join(t.TempDir(), "missing")},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve allowed dir")
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

func TestInspectRunsOCRWithTempImageAndTruncates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	writeTestPNG(t, path, image.Rect(0, 0, 4, 2))
	cmd := writeShellScript(t, dir, "tesseract-ok", `
printf 'abcdefghijklmnop'
`)

	tool, err := newInspector(Config{
		AllowedDirs:      []string{dir},
		TesseractCommand: cmd,
		MaxOCRChars:      10,
		Timeout:          time.Second,
	})
	require.NoError(t, err)
	threshold := 128
	got, err := tool.inspect(context.Background(), inspectRequest{
		Path:      path,
		MaxChars:  6,
		Scale:     1.5,
		Threshold: &threshold,
		Crop: &cropRequest{
			X:      -2,
			Y:      -1,
			Width:  20,
			Height: 20,
		},
	})
	require.NoError(t, err)
	require.True(t, got.TesseractUsed)
	require.Equal(t, "abcdef\n...[truncated]", got.OCRText)
	require.Equal(t, &cropResponse{
		X:      0,
		Y:      0,
		Width:  4,
		Height: 2,
	}, got.Crop)
}

func TestInspectReportsOCRErrorFromStderr(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	writeTestPNG(t, path, image.Rect(0, 0, 2, 2))
	cmd := writeShellScript(t, dir, "tesseract-fail", `
printf 'bad ocr' >&2
exit 2
`)

	tool, err := newInspector(Config{
		AllowedDirs:      []string{dir},
		TesseractCommand: cmd,
		Timeout:          time.Second,
	})
	require.NoError(t, err)
	got, err := tool.inspect(context.Background(), inspectRequest{
		Path: path,
	})
	require.NoError(t, err)
	require.False(t, got.TesseractUsed)
	require.Contains(t, got.OCRError, "bad ocr")
}

func TestRunOCRTimeout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cmd := writeShellScript(t, dir, "tesseract-slow", `
sleep 1
`)

	tool, err := newInspector(Config{
		AllowedDirs:      []string{dir},
		TesseractCommand: cmd,
		Timeout:          time.Millisecond,
	})
	require.NoError(t, err)
	_, err = tool.runOCR(context.Background(), "sample.png", inspectRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
}

func TestRunOCRAddsLangPSMAndReportsMissingCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cmd := writeShellScript(t, dir, "tesseract-args", `
printf '%s' "$*"
`)
	tool, err := newInspector(Config{
		AllowedDirs:      []string{dir},
		TesseractCommand: cmd,
		Timeout:          time.Second,
	})
	require.NoError(t, err)
	text, err := tool.runOCR(context.Background(), "sample.png", inspectRequest{
		Lang: "eng",
		PSM:  6,
	})
	require.NoError(t, err)
	require.Equal(t, "sample.png stdout -l eng --psm 6", text)

	tool.tesseractCommand = filepath.Join(dir, "missing-tesseract")
	_, err = tool.runOCR(context.Background(), "sample.png", inspectRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tesseract failed")
}

func TestInspectAllowsAllFilesAndReportsDecodeError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "not-an-image.txt")
	require.NoError(t, os.WriteFile(path, []byte("nope"), 0o644))

	tool, err := newInspector(Config{AllowAllFiles: true})
	require.NoError(t, err)
	_, err = tool.inspect(context.Background(), inspectRequest{Path: path})
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode image")
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

func TestResolvePathRequiresPath(t *testing.T) {
	t.Parallel()

	tool, err := newInspector(Config{AllowedDirs: []string{t.TempDir()}})
	require.NoError(t, err)
	_, err = tool.resolvePath(" ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path is required")
}

func TestResolvePathCorrectsDuplicatedAttachmentRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	attachmentID := "attachment-12345"
	actual := filepath.Join(root, attachmentID, attachmentID+".png")
	require.NoError(t, os.MkdirAll(filepath.Dir(actual), 0o755))
	writeTestPNG(t, actual, image.Rect(0, 0, 1, 1))

	tool, err := newInspector(Config{AllowedDirs: []string{root}})
	require.NoError(t, err)
	raw := filepath.Join(root, attachmentID, attachmentID, attachmentID+".png")
	got, err := tool.resolvePath(raw)
	require.NoError(t, err)
	require.Equal(t, actual, got)
}

func TestResolvePathCorrectsDuplicatedAllowedDirBase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	attachmentID := "attachment-12345"
	allowed := filepath.Join(root, attachmentID)
	actual := filepath.Join(allowed, attachmentID+".png")
	require.NoError(t, os.MkdirAll(filepath.Dir(actual), 0o755))
	writeTestPNG(t, actual, image.Rect(0, 0, 1, 1))

	tool, err := newInspector(Config{AllowedDirs: []string{allowed}})
	require.NoError(t, err)
	raw := filepath.Join(allowed, attachmentID, attachmentID+".png")
	got, err := tool.resolvePath(raw)
	require.NoError(t, err)
	require.Equal(t, actual, got)
}

func TestResolvePathCorrectsDuplicatedRelativeAttachment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	attachmentID := "attachment-12345"
	actual := filepath.Join(root, attachmentID, attachmentID+".png")
	require.NoError(t, os.MkdirAll(filepath.Dir(actual), 0o755))
	writeTestPNG(t, actual, image.Rect(0, 0, 1, 1))

	tool, err := newInspector(Config{AllowedDirs: []string{root}})
	require.NoError(t, err)
	raw := filepath.Join(attachmentID, attachmentID, attachmentID+".png")
	got, err := tool.resolvePath(raw)
	require.NoError(t, err)
	require.Equal(t, actual, got)
}

func TestResolvePathRejectsDuplicatedSymlinkEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	attachmentID := "attachment-12345"
	inside := filepath.Join(root, attachmentID, attachmentID+".png")
	outsidePath := filepath.Join(outside, attachmentID+".png")
	writeTestPNG(t, outsidePath, image.Rect(0, 0, 1, 1))
	require.NoError(t, os.MkdirAll(filepath.Dir(inside), 0o755))
	require.NoError(t, os.Symlink(outsidePath, inside))

	tool, err := newInspector(Config{AllowedDirs: []string{root}})
	require.NoError(t, err)
	raw := filepath.Join(root, attachmentID, attachmentID, attachmentID+".png")
	_, err = tool.resolvePath(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside allowed_dirs")
}

func TestResolvePathRejectsAmbiguousDuplicatedCandidates(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	allowed := filepath.Join(root, "attachment")
	first := filepath.Join(allowed, "b", "b", "image.png")
	second := filepath.Join(allowed, "attachment", "b", "image.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(first), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(second), 0o755))
	writeTestPNG(t, first, image.Rect(0, 0, 1, 1))
	writeTestPNG(t, second, image.Rect(0, 0, 1, 1))

	tool, err := newInspector(Config{AllowedDirs: []string{allowed}})
	require.NoError(t, err)
	raw := filepath.Join(allowed, "attachment", "b", "b", "image.png")
	_, err = tool.resolvePath(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple corrected paths")
}

func TestDuplicatedAllowedPathCandidatesRejectsNonDuplicatedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.Empty(t, duplicatedAllowedPathCandidates(root, root))
	require.Empty(t, duplicatedAllowedPathCandidates(filepath.Dir(root), root))
	require.Empty(t, duplicatedAllowedPathCandidates(
		filepath.Join(root, "single"),
		root,
	))
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

func TestResolveAllowedRelativePathRejectsDuplicateBasenames(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "a")
	second := filepath.Join(dir, "b")
	require.NoError(t, os.MkdirAll(first, 0o755))
	require.NoError(t, os.MkdirAll(second, 0o755))
	writeTestPNG(t, filepath.Join(first, "same.png"), image.Rect(0, 0, 1, 1))
	writeTestPNG(t, filepath.Join(second, "same.png"), image.Rect(0, 0, 1, 1))

	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	_, err = tool.resolvePath("same.png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple files")
}

func TestResolveAllowedRelativePathFindsPreferredOutputBeforeLargeWalk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	many := filepath.Join(dir, "many")
	require.NoError(t, os.MkdirAll(many, 0o755))
	for n := 0; n <= maxBasenameSearchEntries; n++ {
		require.NoError(
			t,
			os.WriteFile(
				filepath.Join(many, fmt.Sprintf("file-%05d.txt", n)),
				[]byte("x"),
				0o644,
			),
		)
	}
	target := filepath.Join(dir, "workspaces", "scratch", "out", "shot.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	writeTestPNG(t, target, image.Rect(0, 0, 1, 1))

	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	got, err := tool.resolvePath("shot.png")
	require.NoError(t, err)
	require.Equal(t, target, got)
}

func TestResolvePreferredBasenameRejectsDuplicatePreferredOutputs(t *testing.T) {
	t.Parallel()

	first := t.TempDir()
	second := t.TempDir()
	for _, dir := range []string{first, second} {
		target := filepath.Join(dir, "workspaces", "scratch", "out", "same.png")
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		writeTestPNG(t, target, image.Rect(0, 0, 1, 1))
	}

	tool, err := newInspector(Config{AllowedDirs: []string{first, second}})
	require.NoError(t, err)
	_, err = tool.resolvePath("same.png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple files")
}

func TestResolvePreferredBasenameRejectsEscapingSymlink(t *testing.T) {
	t.Parallel()

	allowed := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "shot.png")
	writeTestPNG(t, outsidePath, image.Rect(0, 0, 1, 1))

	preferred := filepath.Join(allowed, "workspaces", "scratch", "out")
	require.NoError(t, os.MkdirAll(preferred, 0o755))
	require.NoError(
		t,
		os.Symlink(outsidePath, filepath.Join(preferred, "shot.png")),
	)

	tool, err := newInspector(Config{AllowedDirs: []string{allowed}})
	require.NoError(t, err)
	_, err = tool.resolvePath("shot.png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside allowed_dirs")
}

func TestResolveAllowedRelativePathMissesDeletedAllowedDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(dir))

	got, ok, err := tool.resolveAllowedRelativePath("missing.png")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, got)
}

func TestResolveAllowedRelativePathMissingBasenameFallsBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	_, err = tool.resolvePath("missing.png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve image path")
}

func TestResolveAllowedRelativePathWithSeparatorFallsBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool, err := newInspector(Config{AllowedDirs: []string{dir}})
	require.NoError(t, err)
	_, err = tool.resolvePath("nested/missing.png")
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve image path")
}

func TestScaleImageClampsScaleBounds(t *testing.T) {
	t.Parallel()

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	small := scaleImage(img, 0.01)
	require.Equal(t, image.Rect(0, 0, 1, 1), small.Bounds())

	large := scaleImage(img, 10)
	require.Equal(t, image.Rect(0, 0, 12, 12), large.Bounds())

	empty := scaleImage(image.NewRGBA(image.Rect(0, 0, 0, 0)), 1)
	require.Equal(t, image.Rect(0, 0, 1, 1), empty.Bounds())
}

func TestASCIIWidthDefaultsAndClamps(t *testing.T) {
	t.Parallel()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	require.NotEmpty(t, asciiPreview(img, 0))
	got := asciiPreview(img, maxASCIIWidth+100)
	require.Len(t, strings.Split(got, "\n")[0], maxASCIIWidth)
}

func TestASCIIPreviewHandlesEmptyAndTallImages(t *testing.T) {
	t.Parallel()

	require.Empty(t, asciiPreview(image.NewRGBA(image.Rect(0, 0, 0, 1)), 10))

	tall := image.NewRGBA(image.Rect(0, 0, 1, 1000))
	got := asciiPreview(tall, 10)
	require.Len(t, strings.Split(got, "\n"), maxASCIIHeight)
}

func writeTestPNG(t *testing.T, path string, bounds image.Rectangle) {
	t.Helper()

	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, png.Encode(file, image.NewRGBA(bounds)))
	require.NoError(t, file.Close())
}

func writeShellScript(
	t *testing.T,
	dir string,
	name string,
	body string,
) string {
	t.Helper()

	path := filepath.Join(dir, name)
	tmpPath := path + ".tmp"
	content := "#!/bin/sh\n" + body
	require.NoError(t, os.WriteFile(tmpPath, []byte(content), 0o755))
	require.NoError(t, os.Rename(tmpPath, path))
	return path
}
