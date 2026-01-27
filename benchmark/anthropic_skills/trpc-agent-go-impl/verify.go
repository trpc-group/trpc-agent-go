//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
)

const maxZipReadBytes int64 = 2 * 1024 * 1024

type launchThemeSpec struct {
	Theme string `json:"theme"`
	Teal  string `json:"teal"`
}

type brandTokens struct {
	Dark      string `json:"dark"`
	Light     string `json:"light"`
	MidGray   string `json:"mid_gray"`
	LightGray string `json:"light_gray"`
	Orange    string `json:"orange"`
	Blue      string `json:"blue"`
	Green     string `json:"green"`
}

func latestWorkspaceDir(workRoot string, execID string) (string, error) {
	if strings.TrimSpace(workRoot) == "" {
		return "", fmt.Errorf("empty work root")
	}
	prefix := "ws_" + safeExecID(execID) + "_"
	ents, err := os.ReadDir(workRoot)
	if err != nil {
		return "", err
	}
	var (
		bestName string
		bestTime time.Time
	)
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestName == "" || info.ModTime().After(bestTime) {
			bestName = name
			bestTime = info.ModTime()
		}
	}
	if bestName == "" {
		return "", fmt.Errorf("workspace not found for %q", execID)
	}
	return filepath.Join(workRoot, bestName), nil
}

func safeExecID(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'a', 'b', 'c', 'd', 'e', 'f', 'g',
			'h', 'i', 'j', 'k', 'l', 'm', 'n',
			'o', 'p', 'q', 'r', 's', 't', 'u',
			'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G',
			'H', 'I', 'J', 'K', 'L', 'M', 'N',
			'O', 'P', 'Q', 'R', 'S', 'T', 'U',
			'V', 'W', 'X', 'Y', 'Z',
			'0', '1', '2', '3', '4', '5', '6',
			'7', '8', '9', '-', '_':
			return r
		default:
			return '_'
		}
	}, s)
}

func verifyLaunchKitOutputs(
	repo skillrepo.Repository,
	wsDir string,
) error {
	wantTeal, err := expectedOceanDepthsTeal(repo)
	if err != nil {
		return err
	}

	specPath := filepath.Join(wsDir, fileLaunchThemeSpecJSON)
	specBytes, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	var spec launchThemeSpec
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return fmt.Errorf("invalid theme spec json: %w", err)
	}
	if strings.TrimSpace(spec.Theme) == "" {
		return fmt.Errorf("theme spec: empty theme")
	}
	if strings.TrimSpace(spec.Teal) == "" {
		return fmt.Errorf("theme spec: empty teal")
	}
	gotTeal := strings.ToUpper(strings.TrimSpace(spec.Teal))
	if gotTeal != wantTeal {
		return fmt.Errorf("theme spec teal=%q want %q", gotTeal, wantTeal)
	}

	gifPath := filepath.Join(wsDir, fileLaunchGIF)
	gifBytes, err := os.ReadFile(gifPath)
	if err != nil {
		return err
	}
	if !isGIF(gifBytes) {
		return fmt.Errorf("launch gif is not a GIF")
	}

	pptxPath := filepath.Join(wsDir, fileLaunchPPTX)
	if _, err := os.Stat(pptxPath); err != nil {
		return err
	}
	wantHex := strings.TrimPrefix(wantTeal, "#")
	if err := verifyPPTXHasText(
		pptxPath,
		launchKitSlideTitle,
	); err != nil {
		return err
	}
	if err := verifyPPTXHasColorHex(pptxPath, wantHex); err != nil {
		return err
	}
	if err := verifyPPTXHasGIF(pptxPath); err != nil {
		return err
	}
	return nil
}

func verifyBrandLandingOutputs(
	repo skillrepo.Repository,
	wsDir string,
) error {
	want, err := expectedBrandTokens(repo)
	if err != nil {
		return err
	}

	tokensPath := filepath.Join(wsDir, fileBrandTokens)
	b, err := os.ReadFile(tokensPath)
	if err != nil {
		return err
	}
	var got brandTokens
	if err := json.Unmarshal(b, &got); err != nil {
		return fmt.Errorf("invalid brand tokens json: %w", err)
	}
	if err := compareBrandTokens(want, got); err != nil {
		return err
	}

	cssPath := filepath.Join(wsDir, fileBrandStylesCSS)
	cssBytes, err := os.ReadFile(cssPath)
	if err != nil {
		return err
	}
	css := strings.ToUpper(string(cssBytes))
	for _, hex := range wantHexList(want) {
		if !strings.Contains(css, strings.ToUpper(hex)) {
			return fmt.Errorf("styles.css missing %q", hex)
		}
	}

	htmlPath := filepath.Join(wsDir, fileBrandIndexHTML)
	htmlBytes, err := os.ReadFile(htmlPath)
	if err != nil {
		return err
	}
	html := string(htmlBytes)
	if !strings.Contains(html, "Bench Landing") {
		return fmt.Errorf("index.html missing title text")
	}
	if !strings.Contains(html, "styles.css") {
		return fmt.Errorf("index.html missing styles.css link")
	}
	return nil
}

func expectedBrandTokens(repo skillrepo.Repository) (brandTokens, error) {
	if repo == nil {
		return brandTokens{}, fmt.Errorf("nil repo")
	}
	root, err := repo.Path(skillBrandGuide)
	if err != nil {
		return brandTokens{}, err
	}
	p := filepath.Join(root, skillDefinitionFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return brandTokens{}, err
	}
	body := string(b)
	get := func(label string) (string, error) {
		re := regexp.MustCompile(
			"(?mi)^-\\s*" + regexp.QuoteMeta(label) +
				":\\s*`(#([0-9a-f]{6}))`",
		)
		m := re.FindStringSubmatch(body)
		if len(m) < 2 {
			return "", fmt.Errorf("brand color %q not found", label)
		}
		return strings.ToUpper(m[1]), nil
	}

	dark, err := get("Dark")
	if err != nil {
		return brandTokens{}, err
	}
	light, err := get("Light")
	if err != nil {
		return brandTokens{}, err
	}
	midGray, err := get("Mid Gray")
	if err != nil {
		return brandTokens{}, err
	}
	lightGray, err := get("Light Gray")
	if err != nil {
		return brandTokens{}, err
	}
	orange, err := get("Orange")
	if err != nil {
		return brandTokens{}, err
	}
	blue, err := get("Blue")
	if err != nil {
		return brandTokens{}, err
	}
	green, err := get("Green")
	if err != nil {
		return brandTokens{}, err
	}
	return brandTokens{
		Dark:      dark,
		Light:     light,
		MidGray:   midGray,
		LightGray: lightGray,
		Orange:    orange,
		Blue:      blue,
		Green:     green,
	}, nil
}

func wantHexList(t brandTokens) []string {
	return []string{
		t.Dark,
		t.Light,
		t.MidGray,
		t.LightGray,
		t.Orange,
		t.Blue,
		t.Green,
	}
}

func compareBrandTokens(want brandTokens, got brandTokens) error {
	if normalizeHex(got.Dark) != want.Dark {
		return fmt.Errorf("brand token dark=%q want %q",
			got.Dark, want.Dark)
	}
	if normalizeHex(got.Light) != want.Light {
		return fmt.Errorf("brand token light=%q want %q",
			got.Light, want.Light)
	}
	if normalizeHex(got.MidGray) != want.MidGray {
		return fmt.Errorf("brand token mid_gray=%q want %q",
			got.MidGray, want.MidGray)
	}
	if normalizeHex(got.LightGray) != want.LightGray {
		return fmt.Errorf("brand token light_gray=%q want %q",
			got.LightGray, want.LightGray)
	}
	if normalizeHex(got.Orange) != want.Orange {
		return fmt.Errorf("brand token orange=%q want %q",
			got.Orange, want.Orange)
	}
	if normalizeHex(got.Blue) != want.Blue {
		return fmt.Errorf("brand token blue=%q want %q",
			got.Blue, want.Blue)
	}
	if normalizeHex(got.Green) != want.Green {
		return fmt.Errorf("brand token green=%q want %q",
			got.Green, want.Green)
	}
	return nil
}

func normalizeHex(s string) string {
	v := strings.TrimSpace(s)
	if v == "" {
		return v
	}
	if !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	return strings.ToUpper(v)
}

func verifyDocxOOXMLOutputs(wsDir string) error {
	docxPath := filepath.Join(wsDir, fileDocxBench)
	xml, err := zipReadEntry(docxPath, "word/document.xml")
	if err != nil {
		return err
	}
	if !bytes.Contains(xml, []byte(markerDocxOK)) {
		return fmt.Errorf("docx missing marker %q", markerDocxOK)
	}

	unpacked := filepath.Join(wsDir, dirUnpackedDocx)
	docXML := filepath.Join(unpacked, "word", "document.xml")
	b, err := os.ReadFile(docXML)
	if err != nil {
		return err
	}
	if !bytes.Contains(b, []byte(markerDocxOK)) {
		return fmt.Errorf("unpacked docx missing marker %q", markerDocxOK)
	}
	return nil
}

func verifyTemplateOutputs(wsDir string) error {
	p := filepath.Join(wsDir, fileBenchTemplate)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	s := string(b)
	if !strings.HasPrefix(s, "---") {
		return fmt.Errorf("generated SKILL.md missing frontmatter")
	}
	if !strings.Contains(s, "name: bench-template-skill") {
		return fmt.Errorf("generated SKILL.md missing name")
	}
	if !strings.Contains(s, "description: Minimal benchmark skill") {
		return fmt.Errorf("generated SKILL.md missing description")
	}
	return nil
}

func expectedOceanDepthsTeal(repo skillrepo.Repository) (string, error) {
	if repo == nil {
		return "", fmt.Errorf("nil repo")
	}
	root, err := repo.Path(skillThemeFactory)
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, oceanDepthsThemeRelPath)
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(
		"(?i)\\*\\*Teal\\*\\*: `(#([0-9a-f]{6}))`",
	)
	m := re.FindStringSubmatch(string(b))
	if len(m) < 2 {
		return "", fmt.Errorf("teal not found in %s", oceanDepthsThemeRelPath)
	}
	return strings.ToUpper(m[1]), nil
}

func verifyPPTXHasText(pptxPath string, want string) error {
	ok, err := zipAnyTextEntryContains(
		pptxPath,
		"ppt/slides/",
		".xml",
		want,
		false,
	)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pptx missing text %q", want)
	}
	return nil
}

func verifyPPTXHasColorHex(pptxPath string, wantHex string) error {
	ok, err := zipAnyTextEntryContains(
		pptxPath,
		"ppt/",
		".xml",
		wantHex,
		true,
	)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("pptx missing color %q", wantHex)
	}
	return nil
}

func verifyPPTXHasGIF(pptxPath string) error {
	name, b, err := zipFindFirst(pptxPath, "ppt/media/", ".gif")
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("pptx missing embedded gif")
	}
	if !isGIF(b) {
		return fmt.Errorf("pptx media %q is not a gif", name)
	}
	return nil
}

func zipReadEntry(zipPath string, name string) ([]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name != name {
			continue
		}
		return readZipFileLimited(f, maxZipReadBytes)
	}
	return nil, fmt.Errorf("zip entry not found: %s", name)
}

func zipFindFirst(
	zipPath string,
	prefix string,
	suffix string,
) (string, []byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", nil, err
	}
	defer r.Close()
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Name), suffix) {
			continue
		}
		b, err := readZipFileLimited(f, maxZipReadBytes)
		if err != nil {
			return "", nil, err
		}
		return f.Name, b, nil
	}
	return "", nil, nil
}

func zipAnyTextEntryContains(
	zipPath string,
	prefix string,
	suffix string,
	substr string,
	caseInsensitive bool,
) (bool, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return false, err
	}
	defer r.Close()

	want := substr
	if caseInsensitive {
		want = strings.ToUpper(want)
	}
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(f.Name), suffix) {
			continue
		}
		b, err := readZipFileLimited(f, maxZipReadBytes)
		if err != nil {
			return false, err
		}
		got := string(b)
		if caseInsensitive {
			got = strings.ToUpper(got)
		}
		if strings.Contains(got, want) {
			return true, nil
		}
	}
	return false, nil
}

func readZipFileLimited(f *zip.File, maxBytes int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, rc, maxBytes+1); err != nil &&
		err != io.EOF {
		return nil, err
	}
	if int64(buf.Len()) > maxBytes {
		return nil, fmt.Errorf("zip entry too large: %s", f.Name)
	}
	return buf.Bytes(), nil
}

func isGIF(b []byte) bool {
	return bytes.HasPrefix(b, []byte("GIF87a")) ||
		bytes.HasPrefix(b, []byte("GIF89a"))
}
