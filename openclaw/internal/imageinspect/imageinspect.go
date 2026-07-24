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
	"image/color"
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
	defaultName              = "image_inspect"
	defaultTimeout           = 20 * time.Second
	defaultMaxOCRChars       = 12000
	defaultASCIIWidth        = 96
	maxASCIIWidth            = 160
	maxASCIIHeight           = 80
	maxBasenameSearchEntries = 10000
	maxDuplicatePathParts    = 128
	maxDuplicateCandidates   = 64
)

var preferredBasenameSubdirs = []string{
	filepath.Join("workspaces", "scratch", "out"),
	filepath.Join("workspaces", "scratch"),
	filepath.Join("runtime", "tmp"),
	"artifacts",
	"downloads",
	"out",
}

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
				"when tesseract is available, OCR text. Supports optional "+
				"crop rectangles, OCR preprocessing with scale, threshold, "+
				"or invert, and ASCII previews for "+
				"screenshots, scanned documents, math worksheets, "+
				"diagrams, and other visible-image tasks.",
		),
	), nil
}

type inspector struct {
	allowedDirs       []string
	allowedDirAliases []string
	allowAllFiles     bool
	tesseractCommand  string
	timeout           time.Duration
	maxOCRChars       int
}

func newInspector(cfg Config) (*inspector, error) {
	if !cfg.AllowAllFiles && len(cfg.AllowedDirs) == 0 {
		return nil, errors.New(
			"image_inspect requires allowed_dirs or allow_all_files",
		)
	}

	allowedDirs := make([]string, 0, len(cfg.AllowedDirs))
	allowedDirAliases := make([]string, 0, len(cfg.AllowedDirs))
	for _, dir := range cfg.AllowedDirs {
		abs, err := filepath.Abs(strings.TrimSpace(dir))
		if err != nil {
			return nil, fmt.Errorf("resolve allowed dir %q: %w", dir, err)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("resolve allowed dir %q: %w", dir, err)
		}
		allowedDirs = append(allowedDirs, filepath.Clean(real))
		allowedDirAliases = append(allowedDirAliases, filepath.Clean(abs))
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
		allowedDirs:       allowedDirs,
		allowedDirAliases: allowedDirAliases,
		allowAllFiles:     cfg.AllowAllFiles,
		tesseractCommand:  cmd,
		timeout:           timeout,
		maxOCRChars:       maxOCRChars,
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
	Scale      float64      `json:"scale,omitempty"`
	Threshold  *int         `json:"threshold,omitempty"`
	Invert     bool         `json:"invert,omitempty"`
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
	if needsImagePreprocess(req) {
		target = preprocessImage(target, req)
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
		if req.Crop != nil || needsImagePreprocess(req) {
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
	if !filepath.IsAbs(raw) && !i.allowAllFiles {
		if path, ok, err := i.resolveAllowedRelativePath(raw); ok || err != nil {
			return path, err
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	abs = filepath.Clean(abs)
	if i.allowAllFiles {
		return abs, nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			if path, ok, resolveErr := i.resolveDuplicatedAllowedPaths(
				[]string{abs},
				raw,
			); ok || resolveErr != nil {
				return path, resolveErr
			}
		}
		return "", fmt.Errorf("resolve image path: %w", err)
	}
	real = filepath.Clean(real)
	for _, dir := range i.allowedDirs {
		if pathInAllowedDir(real, dir) {
			return real, nil
		}
	}
	return "", fmt.Errorf("path %q is outside allowed_dirs", raw)
}

func (i *inspector) resolveAllowedRelativePath(
	raw string,
) (string, bool, error) {
	cleaned := filepath.Clean(raw)
	if cleaned == ".." ||
		strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", true, fmt.Errorf("path %q is outside allowed_dirs", raw)
	}
	for _, dir := range i.allowedDirs {
		candidate := filepath.Clean(filepath.Join(dir, cleaned))
		if !pathInAllowedDir(candidate, dir) {
			continue
		}
		if !fileExists(candidate) {
			continue
		}
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", true, fmt.Errorf("resolve image path: %w", err)
		}
		real = filepath.Clean(real)
		if !pathInAllowedDir(real, dir) {
			return "", true, fmt.Errorf("path %q is outside allowed_dirs", raw)
		}
		return real, true, nil
	}
	absCandidates := make([]string, 0, len(i.allowedDirs))
	for _, dir := range i.allowedDirs {
		absCandidates = append(absCandidates, filepath.Clean(
			filepath.Join(dir, cleaned),
		))
	}
	if path, ok, err := i.resolveDuplicatedAllowedPaths(
		absCandidates,
		raw,
	); ok || err != nil {
		return path, true, err
	}

	if strings.Contains(cleaned, string(os.PathSeparator)) {
		return "", false, nil
	}
	if path, ok, err := i.resolvePreferredBasename(cleaned); ok || err != nil {
		return path, ok, err
	}
	var match string
	visited := 0
	for _, dir := range i.allowedDirs {
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			visited++
			if visited > maxBasenameSearchEntries {
				return fmt.Errorf(
					"basename search exceeded %d files in allowed_dirs",
					maxBasenameSearchEntries,
				)
			}
			if d.Name() != cleaned {
				return nil
			}
			if match != "" {
				return fmt.Errorf("multiple files named %q in allowed_dirs", cleaned)
			}
			match = path
			return nil
		}); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", true, err
		}
	}
	if match == "" {
		return "", false, nil
	}
	real, err := filepath.EvalSymlinks(match)
	if err != nil {
		return "", true, fmt.Errorf("resolve image path: %w", err)
	}
	real = filepath.Clean(real)
	for _, dir := range i.allowedDirs {
		if pathInAllowedDir(real, dir) {
			return real, true, nil
		}
	}
	return "", true, fmt.Errorf("path %q is outside allowed_dirs", raw)
}

func (i *inspector) resolvePreferredBasename(
	basename string,
) (string, bool, error) {
	var match string
	for _, dir := range i.allowedDirs {
		for _, subdir := range preferredBasenameSubdirs {
			candidate := filepath.Clean(filepath.Join(dir, subdir, basename))
			if !pathInAllowedDir(candidate, dir) || !fileExists(candidate) {
				continue
			}
			real, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", true, fmt.Errorf("resolve image path: %w", err)
			}
			real = filepath.Clean(real)
			if !pathInAllowedDir(real, dir) {
				return "", true, fmt.Errorf(
					"path %q is outside allowed_dirs",
					basename,
				)
			}
			if match != "" && match != real {
				return "", true, fmt.Errorf(
					"multiple files named %q in preferred allowed_dirs",
					basename,
				)
			}
			match = real
		}
	}
	if match == "" {
		return "", false, nil
	}
	return match, true, nil
}

func (i *inspector) resolveDuplicatedAllowedPaths(
	absCandidates []string,
	raw string,
) (string, bool, error) {
	var match string
	for _, abs := range absCandidates {
		for idx, dir := range i.allowedDirs {
			candidateRoots := []string{dir}
			if idx < len(i.allowedDirAliases) &&
				i.allowedDirAliases[idx] != dir {
				candidateRoots = append(candidateRoots, i.allowedDirAliases[idx])
			}
			for _, root := range candidateRoots {
				for _, candidate := range duplicatedAllowedPathCandidates(abs, root) {
					if !fileExists(candidate) {
						continue
					}
					real, err := filepath.EvalSymlinks(candidate)
					if err != nil {
						return "", true, fmt.Errorf("resolve image path: %w", err)
					}
					real = filepath.Clean(real)
					if !pathInAllowedDir(real, dir) {
						return "", true, fmt.Errorf(
							"path %q is outside allowed_dirs",
							raw,
						)
					}
					if match != "" && match != real {
						return "", true, fmt.Errorf(
							"multiple corrected paths match %q in allowed_dirs",
							raw,
						)
					}
					match = real
				}
			}
		}
	}
	if match == "" {
		return "", false, nil
	}
	return match, true, nil
}

func duplicatedAllowedPathCandidates(abs string, dir string) []string {
	rel, err := filepath.Rel(dir, abs)
	if err != nil || rel == "." {
		return nil
	}
	if rel == ".." ||
		strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return nil
	}
	parts := splitPath(rel)
	if len(parts) < 2 || len(parts) > maxDuplicatePathParts {
		return nil
	}

	seen := map[string]struct{}{}
	var candidates []string
	add := func(candidateParts []string) {
		if len(candidateParts) == 0 {
			return
		}
		candidate := filepath.Clean(filepath.Join(
			append([]string{dir}, candidateParts...)...,
		))
		if !pathInAllowedDir(candidate, dir) {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}

	if parts[0] == filepath.Base(dir) {
		add(parts[1:])
	}
	for idx := 0; idx < len(parts)-1; idx++ {
		if len(candidates) >= maxDuplicateCandidates {
			break
		}
		if parts[idx] != parts[idx+1] {
			continue
		}
		candidateParts := make([]string, 0, len(parts)-1)
		candidateParts = append(candidateParts, parts[:idx]...)
		candidateParts = append(candidateParts, parts[idx+1:]...)
		add(candidateParts)
	}
	return candidates
}

func splitPath(path string) []string {
	parts := strings.Split(path, string(os.PathSeparator))
	out := parts[:0]
	for _, part := range parts {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}

func pathInAllowedDir(path string, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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

func needsImagePreprocess(req inspectRequest) bool {
	return req.Invert ||
		req.Threshold != nil ||
		(req.Scale > 0 && math.Abs(req.Scale-1) > 0.001)
}

func preprocessImage(img image.Image, req inspectRequest) image.Image {
	out := img
	if req.Scale > 0 && math.Abs(req.Scale-1) > 0.001 {
		out = scaleImage(out, req.Scale)
	}
	if req.Threshold != nil || req.Invert {
		out = thresholdImage(out, req.Threshold, req.Invert)
	}
	return out
}

func scaleImage(img image.Image, scale float64) image.Image {
	if scale < 0.25 {
		scale = 0.25
	}
	if scale > 6 {
		scale = 6
	}
	b := img.Bounds()
	width := int(math.Round(float64(b.Dx()) * scale))
	height := int(math.Round(float64(b.Dy()) * scale))
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcY := b.Min.Y + clamp(int(float64(y)/scale), 0, b.Dy()-1)
		for x := 0; x < width; x++ {
			srcX := b.Min.X + clamp(int(float64(x)/scale), 0, b.Dx()-1)
			out.Set(x, y, img.At(srcX, srcY))
		}
	}
	return out
}

func thresholdImage(img image.Image, threshold *int, invert bool) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	thresholdValue := 0
	if threshold != nil {
		thresholdValue = clamp(*threshold, 0, 255)
	}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			luma := uint8(0.299*float64(r>>8) +
				0.587*float64(g>>8) +
				0.114*float64(bl>>8))
			if threshold != nil {
				if int(luma) > thresholdValue {
					luma = 255
				} else {
					luma = 0
				}
			}
			if invert {
				luma = 255 - luma
			}
			out.SetRGBA(x, y, color.RGBA{
				R: luma,
				G: luma,
				B: luma,
				A: uint8(a >> 8),
			})
		}
	}
	return out
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
