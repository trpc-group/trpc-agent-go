//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package imageinspect provides a local image inspection tool for OpenClaw.
package imageinspect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "image/gif"
	_ "image/jpeg"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultName        = "image_inspect"
	defaultTimeout     = 20 * time.Second
	defaultMaxOCRChars = 12000
	defaultASCIIWidth  = 96
	maxASCIIWidth      = 160
	maxASCIIHeight     = 80
)

// Config configures the image inspection tool.
type Config struct {
	AllowedDirs      []string      `yaml:"allowed_dirs,omitempty"`
	AllowAllFiles    bool          `yaml:"allow_all_files,omitempty"`
	TesseractCommand string        `yaml:"tesseract_command,omitempty"`
	Timeout          time.Duration `yaml:"timeout,omitempty"`
	MaxOCRChars      int           `yaml:"max_ocr_chars,omitempty"`
}

// NewTool creates an image inspection tool.
func NewTool(cfg Config) (tool.CallableTool, error) {
	inspector, err := newInspector(cfg)
	if err != nil {
		return nil, err
	}
	return function.NewFunctionTool(
		inspector.inspect,
		function.WithName(defaultName),
		function.WithDescription(
			"Inspect a local raster image. Returns dimensions and, "+
				"when tesseract is available, OCR text. Supports "+
				"optional crop rectangles and ASCII previews for "+
				"screenshots, scanned documents, math worksheets, "+
				"diagrams, and other visible-image tasks.",
		),
	), nil
}

type inspector struct {
	allowedDirs      []string
	allowAllFiles    bool
	tesseractCommand string
	timeout          time.Duration
	maxOCRChars      int
}

func newInspector(cfg Config) (*inspector, error) {
	if !cfg.AllowAllFiles && len(cfg.AllowedDirs) == 0 {
		return nil, errors.New(
			"image_inspect requires allowed_dirs or allow_all_files",
		)
	}

	allowedDirs := make([]string, 0, len(cfg.AllowedDirs))
	for _, dir := range cfg.AllowedDirs {
		abs, err := filepath.Abs(strings.TrimSpace(dir))
		if err != nil {
			return nil, fmt.Errorf("resolve allowed dir %q: %w", dir, err)
		}
		allowedDirs = append(allowedDirs, filepath.Clean(abs))
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxOCRChars := cfg.MaxOCRChars
	if maxOCRChars <= 0 {
		maxOCRChars = defaultMaxOCRChars
	}
	cmd := strings.TrimSpace(cfg.TesseractCommand)
	if cmd == "" {
		cmd = "tesseract"
	}

	return &inspector{
		allowedDirs:      allowedDirs,
		allowAllFiles:    cfg.AllowAllFiles,
		tesseractCommand: cmd,
		timeout:          timeout,
		maxOCRChars:      maxOCRChars,
	}, nil
}

type inspectRequest struct {
	Path       string       `json:"path"`
	OCR        *bool        `json:"ocr,omitempty"`
	Lang       string       `json:"lang,omitempty"`
	PSM        int          `json:"psm,omitempty"`
	MaxChars   int          `json:"max_chars,omitempty"`
	Crop       *cropRequest `json:"crop,omitempty"`
	ASCII      bool         `json:"ascii,omitempty"`
	ASCIIWidth int          `json:"ascii_width,omitempty"`
}

type cropRequest struct {
	X      int `json:"x" jsonschema:"description=Left pixel coordinate"`
	Y      int `json:"y" jsonschema:"description=Top pixel coordinate"`
	Width  int `json:"width" jsonschema:"description=Crop width in pixels"`
	Height int `json:"height" jsonschema:"description=Crop height in pixels"`
}

type inspectResponse struct {
	Path          string        `json:"path"`
	Format        string        `json:"format,omitempty"`
	Width         int           `json:"width"`
	Height        int           `json:"height"`
	Crop          *cropResponse `json:"crop,omitempty"`
	OCRText       string        `json:"ocr_text,omitempty"`
	OCRError      string        `json:"ocr_error,omitempty"`
	ASCII         string        `json:"ascii,omitempty"`
	TesseractUsed bool          `json:"tesseract_used,omitempty"`
}

type cropResponse struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (i *inspector) inspect(
	ctx context.Context,
	req inspectRequest,
) (inspectResponse, error) {
	path, err := i.resolvePath(req.Path)
	if err != nil {
		return inspectResponse{}, err
	}

	file, err := os.Open(path)
	if err != nil {
		return inspectResponse{}, fmt.Errorf("open image: %w", err)
	}
	defer file.Close()

	img, format, err := image.Decode(file)
	if err != nil {
		return inspectResponse{}, fmt.Errorf("decode image: %w", err)
	}
	bounds := img.Bounds()
	resp := inspectResponse{
		Path:   path,
		Format: format,
		Width:  bounds.Dx(),
		Height: bounds.Dy(),
	}

	target := img
	cleanup := func() {}
	defer func() { cleanup() }()

	if req.Crop != nil {
		cropped, crop := cropImage(img, *req.Crop)
		target = cropped
		resp.Crop = crop
	}
	if req.ASCII {
		resp.ASCII = asciiPreview(target, req.ASCIIWidth)
	}

	runOCR := true
	if req.OCR != nil {
		runOCR = *req.OCR
	}
	if runOCR {
		ocrPath := path
		if req.Crop != nil {
			ocrPath, cleanup, err = writeTempPNG(target)
			if err != nil {
				return inspectResponse{}, err
			}
		}
		text, ocrErr := i.runOCR(ctx, ocrPath, req)
		if ocrErr != nil {
			resp.OCRError = ocrErr.Error()
		} else {
			resp.OCRText = text
			resp.TesseractUsed = true
		}
	}

	return resp, nil
}

func (i *inspector) resolvePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	abs = filepath.Clean(abs)
	if i.allowAllFiles {
		return abs, nil
	}
	for _, dir := range i.allowedDirs {
		if abs == dir || strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed_dirs", raw)
}

func cropImage(
	img image.Image,
	req cropRequest,
) (image.Image, *cropResponse) {
	b := img.Bounds()
	x0 := clamp(req.X, 0, b.Dx())
	y0 := clamp(req.Y, 0, b.Dy())
	x1 := clamp(req.X+req.Width, x0, b.Dx())
	y1 := clamp(req.Y+req.Height, y0, b.Dy())
	rect := image.Rect(x0, y0, x1, y1).Add(b.Min)
	crop := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := 0; y < rect.Dy(); y++ {
		for x := 0; x < rect.Dx(); x++ {
			crop.Set(x, y, img.At(rect.Min.X+x, rect.Min.Y+y))
		}
	}
	return crop, &cropResponse{
		X:      x0,
		Y:      y0,
		Width:  rect.Dx(),
		Height: rect.Dy(),
	}
}

func clamp(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func writeTempPNG(img image.Image) (string, func(), error) {
	file, err := os.CreateTemp("", "openclaw-image-inspect-*.png")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp image: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("encode temp image: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close temp image: %w", err)
	}
	return path, cleanup, nil
}

func (i *inspector) runOCR(
	ctx context.Context,
	path string,
	req inspectRequest,
) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	args := []string{path, "stdout"}
	if lang := strings.TrimSpace(req.Lang); lang != "" {
		args = append(args, "-l", lang)
	}
	if req.PSM > 0 {
		args = append(args, "--psm", fmt.Sprint(req.PSM))
	}
	cmd := exec.CommandContext(timeoutCtx, i.tesseractCommand, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if timeoutCtx.Err() != nil {
		return "", fmt.Errorf("tesseract timed out after %s", i.timeout)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("tesseract failed: %s", detail)
		}
		return "", fmt.Errorf("tesseract failed: %w", err)
	}
	limit := i.maxOCRChars
	if req.MaxChars > 0 && req.MaxChars < limit {
		limit = req.MaxChars
	}
	return truncateText(string(out), limit), nil
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "\n...[truncated]"
}

func asciiPreview(img image.Image, width int) string {
	if width <= 0 {
		width = defaultASCIIWidth
	}
	if width > maxASCIIWidth {
		width = maxASCIIWidth
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return ""
	}
	height := int(math.Round(float64(b.Dy()) / float64(b.Dx()) *
		float64(width) * 0.5))
	if height < 1 {
		height = 1
	}
	if height > maxASCIIHeight {
		height = maxASCIIHeight
	}

	var out strings.Builder
	chars := []byte(" .:-=+*#%@")
	for y := 0; y < height; y++ {
		srcY := b.Min.Y + y*b.Dy()/height
		for x := 0; x < width; x++ {
			srcX := b.Min.X + x*b.Dx()/width
			r, g, bl, _ := img.At(srcX, srcY).RGBA()
			luma := 0.299*float64(r>>8) +
				0.587*float64(g>>8) +
				0.114*float64(bl>>8)
			idx := int((255 - luma) / 255 * float64(len(chars)-1))
			out.WriteByte(chars[clamp(idx, 0, len(chars)-1)])
		}
		out.WriteByte('\n')
	}
	return strings.TrimRight(out.String(), "\n")
}
