//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build darwin

package opensandbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// pinnedWalkSupported reports that this platform can traverse a host
// tree from a pinned directory handle with no-follow semantics.
const pinnedWalkSupported = true

// isSkippableOpenErr reports whether err conclusively represents an
// entry that vanished or was swapped for a symlink during the walk —
// the only conditions under which walkDir may skip an entry. All other
// failures (permission denied, descriptor exhaustion, filesystem I/O
// errors) must propagate so a partial staging run is never reported as
// success.
func isSkippableOpenErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, unix.ELOOP) ||
		errors.Is(err, unix.ENOTDIR)
}

// openDirNoFollow opens the directory at path without following a
// symlink at the final component. The Darwin syscall package does not
// export Openat, so this uses golang.org/x/sys/unix, which provides
// the same O_NOFOLLOW|O_DIRECTORY|O_NONBLOCK root open as Linux/BSD:
// a root swapped for a symlink fails with ELOOP, and a root swapped
// for a FIFO is rejected promptly instead of blocking for a writer.
func openDirNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|
			unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openChildNoFollow opens the entry name relative to the pinned parent
// directory handle dirF without following symlinks, using unix.Openat.
// Name resolution starts from the pinned parent file descriptor, so a
// concurrent writer that replaces an ancestor with a symlink cannot
// redirect the open outside hostRoot.
func openChildNoFollow(dirF *os.File, name string) (*os.File, fs.FileInfo, error) {
	fd, err := unix.Openat(int(dirF.Fd()), name,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
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
