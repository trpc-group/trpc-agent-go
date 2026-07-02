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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveProfiles_ExpandsAggregate(t *testing.T) {
	t.Parallel()

	profiles, err := ResolveProfiles([]string{ProfileCommonFileTools})
	require.NoError(t, err)

	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	require.Equal(t, []string{
		ProfilePDF,
		ProfileOffice,
		ProfileAudio,
		ProfileVideo,
		ProfileImage,
		ProfileOCR,
	}, names)
	require.NotContains(t, names, ProfileChess)
}

func TestSourcesForProfiles_NormalizesInstallMetadata(t *testing.T) {
	t.Parallel()

	sources, err := SourcesForProfiles([]string{ProfilePDF})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, ProfilePDF, sources[0].Name)
	require.NotEmpty(t, sources[0].Install)
	require.Equal(t, "pypdf", sources[0].Requires.Python[0].Module)
}

func TestSourcesForProfiles_ChessProfile(t *testing.T) {
	t.Parallel()

	sources, err := SourcesForProfiles([]string{ProfileChess})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, ProfileChess, sources[0].Name)
	require.Equal(t, []string{"stockfish"}, sources[0].Requires.Bins)
	require.Contains(t, sources[0].Requires.Python, PythonPackage{
		Module:  "chess",
		Package: "python-chess",
	})
	require.NotEmpty(t, sources[0].Install)
	action := sources[0].Install[0]
	require.Equal(t, InstallKindBrew, action.Kind)
	require.Equal(t, []string{"stockfish"}, action.Bins)
	require.True(
		t,
		containsInstallActionKind(sources[0].Install, InstallKindDNF),
	)
}

func TestCatalogHelpers(t *testing.T) {
	t.Parallel()

	profiles := Profiles()
	require.NotEmpty(t, profiles)
	require.Equal(t, DefaultProfiles(), []string{ProfileCommonFileTools})
	require.Equal(t, profiles[0].Name, ProfileAudio)
	require.Contains(t, ProfileNames(), ProfileCommonFileTools)

	_, err := ResolveProfiles([]string{"unknown"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown dependency profile")

	merged := MergeSources(
		Source{Name: " b "},
		Source{Name: "a"},
		Source{},
	)
	require.Len(t, merged, 2)
	require.Equal(t, "a", merged[0].Name)
	require.Equal(t, "b", merged[1].Name)
}

func TestNormalizeInstallActions_NormalizesPlatformFields(t *testing.T) {
	t.Parallel()

	actions := normalizeInstallActions([]InstallAction{{
		Kind:      "DOWNLOAD",
		URL:       " https://example.com/a.zip ",
		Archive:   "TGZ",
		TargetDir: " runtime ",
		OS:        []string{" Linux ", "win32", "linux", ""},
		Arch:      []string{" x86_64 ", "amd64", "aarch64", ""},
		Links: []InstallLink{{
			Source: " bin/tool ",
			Target: " tool ",
		}, {
			Source: "bin/tool",
			Target: "tool",
		}},
		Extract:         true,
		StripComponents: -2,
	}, {
		Kind:      "DOWNLOAD",
		URL:       " https://example.com/a.zip ",
		Archive:   "TGZ",
		TargetDir: " runtime ",
		OS:        []string{"linux", "win32"},
		Arch:      []string{"amd64", "aarch64"},
		Bins:      []string{" second-bin "},
		Links: []InstallLink{{
			Source: " bin/second ",
			Target: " second ",
		}},
		Extract: true,
	}})
	require.Len(t, actions, 1)
	require.Equal(t, InstallKindDownload, actions[0].Kind)
	require.Equal(t, "tgz", actions[0].Archive)
	require.Equal(t, "runtime", actions[0].TargetDir)
	require.Equal(t, []string{"linux", "windows"}, actions[0].OS)
	require.Equal(t, []string{"amd64", "arm64"}, actions[0].Arch)
	require.Equal(t, []string{"second-bin"}, actions[0].Bins)
	require.Equal(t, []InstallLink{{
		Source: "bin/tool",
		Target: "tool",
	}, {
		Source: "bin/second",
		Target: "second",
	}}, actions[0].Links)
	require.True(t, actions[0].Extract)
	require.Equal(t, 0, actions[0].StripComponents)
	require.Equal(t, "windows", normalizeOSName("win32"))
	require.Equal(t, "amd64", normalizeArchName("x86_64"))
}

func containsInstallActionKind(actions []InstallAction, kind string) bool {
	for _, action := range actions {
		if action.Kind == kind {
			return true
		}
	}
	return false
}
