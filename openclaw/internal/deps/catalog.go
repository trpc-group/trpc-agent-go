//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deps

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	ProfilePDF             = "pdf"
	ProfileOffice          = "office"
	ProfileAudio           = "audio"
	ProfileVideo           = "video"
	ProfileImage           = "image"
	ProfileOCR             = "ocr"
	ProfileCommonFileTools = "common-file-tools"
)

const (
	InstallKindAPT      = "apt"
	InstallKindBrew     = "brew"
	InstallKindDownload = "download"
	InstallKindGo       = "go"
	InstallKindDNF      = "dnf"
	InstallKindNode     = "node"
	InstallKindNPM      = "npm"
	InstallKindPIP      = "pip"
	InstallKindUV       = "uv"
	InstallKindYUM      = "yum"
)

type PythonPackage struct {
	Module  string `yaml:"module,omitempty" json:"module,omitempty"`
	Package string `yaml:"package,omitempty" json:"package,omitempty"`
	Label   string `yaml:"label,omitempty" json:"label,omitempty"`
}

type Requirement struct {
	Bins    []string        `yaml:"bins,omitempty" json:"bins,omitempty"`
	AnyBins []string        `yaml:"anyBins,omitempty" json:"any_bins,omitempty"`
	Env     []string        `yaml:"env,omitempty" json:"env,omitempty"`
	Config  []string        `yaml:"config,omitempty" json:"config,omitempty"`
	Python  []PythonPackage `yaml:"python,omitempty" json:"python,omitempty"`
}

type InstallAction struct {
	ID              string   `yaml:"id,omitempty" json:"id,omitempty"`
	Kind            string   `yaml:"kind,omitempty" json:"kind,omitempty"`
	Formula         string   `yaml:"formula,omitempty" json:"formula,omitempty"`
	Package         string   `yaml:"package,omitempty" json:"package,omitempty"`
	Packages        []string `yaml:"packages,omitempty" json:"packages,omitempty"`
	Bins            []string `yaml:"bins,omitempty" json:"bins,omitempty"`
	Label           string   `yaml:"label,omitempty" json:"label,omitempty"`
	Tap             string   `yaml:"tap,omitempty" json:"tap,omitempty"`
	Module          string   `yaml:"module,omitempty" json:"module,omitempty"`
	URL             string   `yaml:"url,omitempty" json:"url,omitempty"`
	Archive         string   `yaml:"archive,omitempty" json:"archive,omitempty"`
	TargetDir       string   `yaml:"targetDir,omitempty" json:"target_dir,omitempty"`
	OS              []string `yaml:"os,omitempty" json:"os,omitempty"`
	Extract         bool     `yaml:"extract,omitempty" json:"extract,omitempty"`
	StripComponents int      `yaml:"stripComponents,omitempty" json:"strip_components,omitempty"`
}

type Source struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Requires    Requirement     `json:"requires,omitempty"`
	Install     []InstallAction `json:"install,omitempty"`
}

type Profile struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Expands     []string        `json:"expands,omitempty"`
	Requires    Requirement     `json:"requires,omitempty"`
	Install     []InstallAction `json:"install,omitempty"`
}

var builtinProfiles = map[string]Profile{
	ProfilePDF: {
		Name:        ProfilePDF,
		Description: "PDF readers, text extraction, and Python fallbacks.",
		Requires: Requirement{
			Bins: []string{"pdftotext", "pdfinfo"},
			Python: []PythonPackage{
				{Module: "pypdf", Package: "pypdf"},
				{Module: "PyPDF2", Package: "PyPDF2"},
				{Module: "fitz", Package: "PyMuPDF"},
			},
		},
		Install: []InstallAction{
			systemInstall(
				InstallKindBrew,
				"poppler",
				"Install PDF tools (brew)",
				"pdftotext",
				"pdfinfo",
			),
			systemInstall(
				InstallKindAPT,
				"poppler-utils",
				"Install PDF tools (apt)",
				"pdftotext",
				"pdfinfo",
			),
			systemInstall(
				InstallKindDNF,
				"poppler-utils",
				"Install PDF tools (dnf)",
				"pdftotext",
				"pdfinfo",
			),
			systemInstall(
				InstallKindYUM,
				"poppler-utils",
				"Install PDF tools (yum)",
				"pdftotext",
				"pdfinfo",
			),
		},
	},
	ProfileOffice: {
		Name:        ProfileOffice,
		Description: "Spreadsheet, Word, and slide parsing helpers.",
		Requires: Requirement{
			Python: []PythonPackage{
				{Module: "pandas", Package: "pandas"},
				{Module: "openpyxl", Package: "openpyxl"},
				{Module: "docx", Package: "python-docx"},
				{Module: "pptx", Package: "python-pptx"},
			},
		},
	},
	ProfileAudio: {
		Name:        ProfileAudio,
		Description: "Audio transcoding and inspection tools.",
		Requires: Requirement{
			Bins: []string{"ffmpeg", "ffprobe"},
		},
		Install: []InstallAction{
			systemInstall(
				InstallKindBrew,
				"ffmpeg",
				"Install ffmpeg (brew)",
				"ffmpeg",
				"ffprobe",
			),
			systemInstall(
				InstallKindAPT,
				"ffmpeg",
				"Install ffmpeg (apt)",
				"ffmpeg",
				"ffprobe",
			),
			systemInstall(
				InstallKindDNF,
				"ffmpeg",
				"Install ffmpeg (dnf)",
				"ffmpeg",
				"ffprobe",
			),
			systemInstall(
				InstallKindYUM,
				"ffmpeg",
				"Install ffmpeg (yum)",
				"ffmpeg",
				"ffprobe",
			),
		},
	},
	ProfileVideo: {
		Name:        ProfileVideo,
		Description: "Video frame extraction and transcoding tools.",
		Requires: Requirement{
			Bins: []string{"ffmpeg", "ffprobe"},
		},
		Install: []InstallAction{
			systemInstall(
				InstallKindBrew,
				"ffmpeg",
				"Install ffmpeg (brew)",
				"ffmpeg",
				"ffprobe",
			),
			systemInstall(
				InstallKindAPT,
				"ffmpeg",
				"Install ffmpeg (apt)",
				"ffmpeg",
				"ffprobe",
			),
			systemInstall(
				InstallKindDNF,
				"ffmpeg",
				"Install ffmpeg (dnf)",
				"ffmpeg",
				"ffprobe",
			),
			systemInstall(
				InstallKindYUM,
				"ffmpeg",
				"Install ffmpeg (yum)",
				"ffmpeg",
				"ffprobe",
			),
		},
	},
	ProfileImage: {
		Name:        ProfileImage,
		Description: "Common image conversion and manipulation tools.",
		Requires: Requirement{
			AnyBins: []string{"magick", "convert"},
		},
		Install: []InstallAction{
			systemInstall(
				InstallKindBrew,
				"imagemagick",
				"Install ImageMagick (brew)",
				"magick",
				"convert",
			),
			systemInstall(
				InstallKindAPT,
				"imagemagick",
				"Install ImageMagick (apt)",
				"magick",
				"convert",
			),
			systemInstall(
				InstallKindDNF,
				"ImageMagick",
				"Install ImageMagick (dnf)",
				"magick",
				"convert",
			),
			systemInstall(
				InstallKindYUM,
				"ImageMagick",
				"Install ImageMagick (yum)",
				"magick",
				"convert",
			),
		},
	},
	ProfileOCR: {
		Name:        ProfileOCR,
		Description: "OCR utilities for scanned images and documents.",
		Requires: Requirement{
			Bins: []string{"tesseract"},
		},
		Install: []InstallAction{
			systemInstall(
				InstallKindBrew,
				"tesseract",
				"Install tesseract (brew)",
				"tesseract",
			),
			systemInstall(
				InstallKindAPT,
				"tesseract-ocr",
				"Install tesseract (apt)",
				"tesseract",
			),
			systemInstall(
				InstallKindDNF,
				"tesseract",
				"Install tesseract (dnf)",
				"tesseract",
			),
			systemInstall(
				InstallKindYUM,
				"tesseract",
				"Install tesseract (yum)",
				"tesseract",
			),
		},
	},
	ProfileCommonFileTools: {
		Name:        ProfileCommonFileTools,
		Description: "Recommended default toolchain for common file work.",
		Expands: []string{
			ProfilePDF,
			ProfileOffice,
			ProfileAudio,
			ProfileVideo,
			ProfileImage,
			ProfileOCR,
		},
	},
}

func systemInstall(
	kind string,
	pkg string,
	label string,
	bins ...string,
) InstallAction {
	return InstallAction{
		ID:      pkg,
		Kind:    kind,
		Formula: pkg,
		Bins:    append([]string(nil), bins...),
		Label:   label,
	}
}

func Profiles() []Profile {
	names := make([]string, 0, len(builtinProfiles))
	for name := range builtinProfiles {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Profile, 0, len(names))
	for _, name := range names {
		out = append(out, normalizeProfile(builtinProfiles[name]))
	}
	return out
}

func DefaultProfiles() []string {
	return []string{ProfileCommonFileTools}
}

func ResolveProfiles(names []string) ([]Profile, error) {
	normalized := normalizeProfileNames(names)
	if len(normalized) == 0 {
		normalized = DefaultProfiles()
	}

	seen := map[string]struct{}{}
	out := make([]Profile, 0, len(normalized))
	for _, name := range normalized {
		if err := appendProfile(&out, seen, name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func SourcesForProfiles(names []string) ([]Source, error) {
	profiles, err := ResolveProfiles(names)
	if err != nil {
		return nil, err
	}

	out := make([]Source, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, Source{
			Name:        profile.Name,
			Description: profile.Description,
			Requires:    profile.Requires,
			Install:     profile.Install,
		})
	}
	return out, nil
}

func ProfileNames() []string {
	profiles := Profiles()
	out := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, profile.Name)
	}
	return out
}

func normalizeProfileNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, raw := range names {
		for _, part := range strings.Split(raw, ",") {
			name := strings.ToLower(strings.TrimSpace(part))
			if name == "" {
				continue
			}
			out = append(out, name)
		}
	}
	return out
}

func appendProfile(
	dst *[]Profile,
	seen map[string]struct{},
	name string,
) error {
	profile, ok := builtinProfiles[name]
	if !ok {
		return fmt.Errorf("unknown dependency profile: %s", name)
	}
	if _, ok := seen[name]; ok {
		return nil
	}
	seen[name] = struct{}{}

	expanded := normalizeProfile(profile)
	for _, child := range expanded.Expands {
		if err := appendProfile(dst, seen, child); err != nil {
			return err
		}
	}
	if len(expanded.Expands) > 0 &&
		isAggregateProfile(expanded) {
		return nil
	}

	*dst = append(*dst, expanded)
	return nil
}

func isAggregateProfile(profile Profile) bool {
	return len(profile.Expands) > 0 &&
		len(profile.Requires.Bins) == 0 &&
		len(profile.Requires.AnyBins) == 0 &&
		len(profile.Requires.Env) == 0 &&
		len(profile.Requires.Config) == 0 &&
		len(profile.Requires.Python) == 0 &&
		len(profile.Install) == 0
}

func normalizeProfile(profile Profile) Profile {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Description = strings.TrimSpace(profile.Description)
	profile.Expands = normalizeStrings(profile.Expands)
	profile.Requires = normalizeRequirement(profile.Requires)
	profile.Install = normalizeInstallActions(profile.Install)
	return profile
}

func normalizeSource(source Source) Source {
	source.Name = strings.TrimSpace(source.Name)
	source.Description = strings.TrimSpace(source.Description)
	source.Requires = normalizeRequirement(source.Requires)
	source.Install = normalizeInstallActions(source.Install)
	return source
}

func normalizeRequirement(req Requirement) Requirement {
	req.Bins = normalizeStrings(req.Bins)
	req.AnyBins = normalizeStrings(req.AnyBins)
	req.Env = normalizeStrings(req.Env)
	req.Config = normalizeStrings(req.Config)
	req.Python = normalizePythonPackages(req.Python)
	return req
}

func normalizePythonPackages(
	pkgs []PythonPackage,
) []PythonPackage {
	out := make([]PythonPackage, 0, len(pkgs))
	seen := map[string]struct{}{}
	for _, raw := range pkgs {
		pkg := PythonPackage{
			Module:  strings.TrimSpace(raw.Module),
			Package: strings.TrimSpace(raw.Package),
			Label:   strings.TrimSpace(raw.Label),
		}
		if pkg.Module == "" && pkg.Package == "" {
			continue
		}
		if pkg.Module == "" {
			pkg.Module = pkg.Package
		}
		if pkg.Package == "" {
			pkg.Package = pkg.Module
		}
		key := pkg.Module + "\x00" + pkg.Package
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, pkg)
	}
	return out
}

func normalizeInstallActions(
	actions []InstallAction,
) []InstallAction {
	out := make([]InstallAction, 0, len(actions))
	seen := map[string]struct{}{}
	for _, raw := range actions {
		action := InstallAction{
			ID:              strings.TrimSpace(raw.ID),
			Kind:            strings.ToLower(strings.TrimSpace(raw.Kind)),
			Formula:         strings.TrimSpace(raw.Formula),
			Package:         strings.TrimSpace(raw.Package),
			Packages:        normalizeStrings(raw.Packages),
			Bins:            normalizeStrings(raw.Bins),
			Label:           strings.TrimSpace(raw.Label),
			Tap:             strings.TrimSpace(raw.Tap),
			Module:          strings.TrimSpace(raw.Module),
			URL:             strings.TrimSpace(raw.URL),
			Archive:         strings.ToLower(strings.TrimSpace(raw.Archive)),
			TargetDir:       strings.TrimSpace(raw.TargetDir),
			OS:              normalizeOSList(raw.OS),
			Extract:         raw.Extract,
			StripComponents: raw.StripComponents,
		}
		if action.Kind == "" {
			continue
		}
		if action.StripComponents < 0 {
			action.StripComponents = 0
		}
		key := strings.Join([]string{
			action.Kind,
			action.ID,
			action.Formula,
			action.Package,
			action.Tap,
			action.Module,
			action.URL,
			action.Archive,
			action.TargetDir,
			strings.Join(action.OS, ","),
			fmt.Sprintf("%t", action.Extract),
			fmt.Sprintf("%d", action.StripComponents),
			strings.Join(action.Packages, ","),
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, action)
	}
	return out
}

func normalizeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeOSList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := normalizeOSName(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeOSName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "win32" {
		return "windows"
	}
	return value
}

func MergeSources(sources ...Source) []Source {
	out := make([]Source, 0, len(sources))
	for _, source := range sources {
		source = normalizeSource(source)
		if source.Name == "" {
			continue
		}
		out = append(out, source)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return slices.Clip(out)
}
