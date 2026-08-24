//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build linux || darwin || ios || freebsd || netbsd || openbsd || dragonfly

package opensandbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// isSkippableOpenErr reports whether err conclusively represents an
// entry that vanished or was swapped for a symlink during the walk —
// the only conditions under which walkDir may skip an entry. All other
// failures (permission denied, descriptor exhaustion, filesystem I/O
// errors) must propagate so a partial staging run is never reported as
// success.
func isSkippableOpenErr(err error) bool {
	// fs.ErrNotExist covers ENOENT (entry raced away); ELOOP is the
	// O_NOFOLLOW rejection of a symlink leaf; ENOTDIR covers a path
	// component replaced by a non-directory between enumeration and
	// the relative open.
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, syscall.ELOOP) ||
		errors.Is(err, syscall.ENOTDIR)
}

// openChildNoFollow opens the entry name relative to the pinned parent
// directory handle dirF without following symlinks, and returns the
// handle together with a Stat of the opened file.
//
// This is the openat(2)+O_NOFOLLOW equivalent of os.Root.Open (Go 1.24)
// on Go 1.21: name resolution starts from the pinned parent file
// descriptor, never from a pathname, so a concurrent writer that
// replaces an ancestor directory with a symlink cannot redirect the
// open to a tree outside hostRoot. A child entry that is (or has become)
// a symlink fails with ELOOP and is skipped by the caller, matching the
// documented "skip non-regular entries" staging contract.
//
// O_NONBLOCK prevents hanging on a FIFO that replaced an enumerated
// regular file between enumeration and open; it has no effect on
// regular files and directories.
func openChildNoFollow(dirF *os.File, name string) (*os.File, fs.FileInfo, error) {
	fd, err := syscall.Openat(int(dirF.Fd()), name,
		syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), filepath.Join(dirF.Name(), name))
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}
