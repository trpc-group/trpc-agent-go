//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package artifact

import "fmt"

// DeleteMode selects how Delete behaves.
type DeleteMode int

const (
	// DeleteAll deletes all versions of the artifact (default).
	DeleteAll DeleteMode = iota
	// DeleteLatest deletes only the latest version.
	DeleteLatest
	// DeleteVersion deletes a specific version.
	DeleteVersion
)

// DeleteOptions controls Delete behavior.
type DeleteOptions struct {
	Mode    DeleteMode
	Version VersionID
}

// DeleteOption configures Delete behavior (functional options style).
type DeleteOption func(*DeleteOptions)

// DeleteAllOpt sets Mode=DeleteAll (default).
func DeleteAllOpt() DeleteOption {
	return func(o *DeleteOptions) {
		if o == nil {
			return
		}
		o.Mode = DeleteAll
		o.Version = ""
	}
}

// DeleteLatestOpt sets Mode=DeleteLatest.
func DeleteLatestOpt() DeleteOption {
	return func(o *DeleteOptions) {
		if o == nil {
			return
		}
		o.Mode = DeleteLatest
		o.Version = ""
	}
}

// DeleteVersionOpt sets Mode=DeleteVersion and the target version.
func DeleteVersionOpt(ver VersionID) DeleteOption {
	return func(o *DeleteOptions) {
		if o == nil {
			return
		}
		o.Mode = DeleteVersion
		o.Version = ver
	}
}

// Validate returns an error when options are invalid.
func (o DeleteOptions) Validate() error {
	switch o.Mode {
	case DeleteAll:
		return nil
	case DeleteLatest:
		return nil
	case DeleteVersion:
		if o.Version == "" {
			return fmt.Errorf("delete version requires non-empty version")
		}
		return nil
	default:
		return fmt.Errorf("unknown delete mode: %d", int(o.Mode))
	}
}
