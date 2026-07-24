//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

//go:build windows

package safety

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestAuditWriter_WindowsOwnerOnlyDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	file, err := openAuditFile(path)
	require.NoError(t, err)
	defer file.Close()
	require.NoError(t, setAuditFilePermissions(file))

	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	require.NoError(t, err)
	sddl := descriptor.String()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	require.Contains(t, sddl, user.User.Sid.String())
	require.Contains(t, sddl, "D:P")
	for _, broadPrincipal := range []string{";;;WD)", ";;;AU)", ";;;BU)"} {
		require.False(t, strings.Contains(sddl, broadPrincipal), sddl)
	}
}
