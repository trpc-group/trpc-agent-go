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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// errSymlinkRefused is the sentinel wrapped by this fallback when an
// entry turns out to be a symlink; walkDir treats it as a skippable
// symlink race, like ELOOP from the openat implementation.
var errSymlinkRefused = errors.New("symlink refused")

// isSkippableOpenErr reports whether err conclusively represents an
// entry that vanished or was swapped for a symlink during the walk —
// the only conditions under which walkDir may skip an entry. All other
// failures (permission denied, descriptor exhaustion, filesystem I/O
// errors) must propagate so a partial staging run is never reported as
// success.
func isSkippableOpenErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, errSymlinkRefused)
}

// openDirNoFollow is the fallback root open for platforms without
// openat(2): it opens path and then refuses when the final component
// turned out to be a symlink, mirroring the unix O_NOFOLLOW semantics
// for the walk root.
func openDirNoFollow(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if li, lierr := os.Lstat(path); lierr == nil && li.Mode()&os.ModeSymlink != 0 {
		f.Close()
		return nil, fmt.Errorf(
			"opensandbox: %s is a symlink; refusing to follow: %w", path, errSymlinkRefused,
		)
	}
	return f, nil
}

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
			"opensandbox: %s is a symlink; refusing to follow: %w", p, errSymlinkRefused,
		)
	}
	return f, info, nil
}
