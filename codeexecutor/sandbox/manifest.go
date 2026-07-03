//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

// Manifest describes the initial sandbox session state v1 can materialize.
// It is intentionally append-only during CreateWorkspace: existing files are
// left in place so live sessions are not silently rewritten.
type Manifest struct {
	Files           []ManifestFile
	Environment     map[string]string
	ExtraReadPaths  []string
	ExtraWritePaths []string
	EphemeralPaths  []string
}

// ManifestFile is a workspace-relative file in a sandbox manifest.
type ManifestFile struct {
	Path    string
	Content []byte
	Mode    uint32
}
