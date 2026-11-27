//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramEnvArgs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		clean      bool
		base, spec map[string]string
		want       []string
	}{
		{"inherit", false, map[string]string{"A": "base"}, map[string]string{"A": "override", "B": "a ' \n$()"}, []string{"--", "A=override", "B=a ' \n$()"}},
		{"empty", false, nil, nil, []string{"--"}},
		{"clean", true, nil, nil, []string{"-i", "--", "PATH=" + minimalCleanPATH}},
		{"explicit path", true, nil, map[string]string{"PATH": "/opt/bin"}, []string{"-i", "--", "PATH=/opt/bin"}},
		{"empty path", true, nil, map[string]string{"PATH": ""}, []string{"-i", "--", "PATH="}},
		{"base path", true, map[string]string{"PATH": "/base"}, nil, []string{"-i", "--", "PATH=/base"}},
		{"case sensitive", true, nil, map[string]string{"Path": "lower"}, []string{"-i", "--", "PATH=" + minimalCleanPATH, "Path=lower"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := programEnvArgs(tc.base, tc.spec, tc.clean)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestProgramEnvArgsRejectsInvalidEnvironment(t *testing.T) {
	for _, env := range []map[string]string{{"": "x"}, {"A=B": "x"}, {"A\x00": "x"}, {"A": "x\x00"}} {
		_, err := programEnvArgs(nil, env, false)
		require.Error(t, err)
	}
}
