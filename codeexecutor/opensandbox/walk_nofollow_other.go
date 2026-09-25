//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package opensandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// pinnedWalkSupported is false on platforms without a usable openat(2)
// (Windows, and other non-Unix targets). Directory staging fail-closes
// rather than reopening children by pathname, which cannot close the
// host-directory swap race.
const pinnedWalkSupported = false

// errCannotPinWalk is returned when the host tree cannot be traversed
// from a pinned directory handle. Callers must treat this as a hard
// failure — never as a skippable race — so a residual swap cannot be
// reported as a successful upload.
var errCannotPinWalk = errors.New(
	"cannot pin staging tree without openat; refusing directory upload",
)

// isSkippableOpenErr reports whether err conclusively represents an
// entry that vanished or was swapped for a symlink during the walk.
// The fail-closed pin error is never skippable.
func isSkippableOpenErr(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// openDirNoFollow fail-closes instead of using a blocking pathname
// open. A pathname fallback cannot pin the root, swallows Lstat
// failures when written as `lierr == nil && ...`, and can hang on a
// FIFO replacement. Refusing the upload is the documented contract
// when the source tree cannot be pinned.
func openDirNoFollow(path string) (*os.File, error) {
	return nil, fmt.Errorf("opensandbox: %s: %w", path, errCannotPinWalk)
}

// openChildNoFollow fail-closes for the same reason: opening by
// pathname from dirF.Name() cannot pin the parent, so a swapped
// ancestor would still be followed.
func openChildNoFollow(dirF *os.File, name string) (*os.File, fs.FileInfo, error) {
	return nil, nil, fmt.Errorf(
		"opensandbox: %s: %w",
		dirF.Name(), errCannotPinWalk,
	)
}
