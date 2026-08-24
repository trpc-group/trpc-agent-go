//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !(linux || darwin || ios || freebsd || netbsd || openbsd || dragonfly)

package opensandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// openChildNoFollow is the fallback for platforms without openat(2)
// (notably Windows). It opens the child by pathname and cannot pin the
// parent, so it re-checks with Lstat after the open and refuses when
// the entry is (or has become) a symlink, which os.Open would otherwise
// silently follow.
//
// The caller pre-filters entries whose enumerated metadata is already
// non-regular (symlinks included), so this check only fires for entries
// swapped between enumeration and open. A residual race (swap back to a
// regular file after the Lstat) remains on these platforms and cannot
// be closed without fd-relative opens; note that creating symlinks on
// Windows itself requires administrator or developer mode.
func openChildNoFollow(dirF *os.File, name string) (*os.File, fs.FileInfo, error) {
	p := filepath.Join(dirF.Name(), name)
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if li, lierr := os.Lstat(p); lierr == nil && li.Mode()&os.ModeSymlink != 0 {
		f.Close()
		return nil, nil, fmt.Errorf(
			"opensandbox: %s is a symlink; refusing to follow", p,
		)
	}
	return f, info, nil
}
