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
