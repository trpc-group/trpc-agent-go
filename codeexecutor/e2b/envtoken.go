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
	"fmt"
	"sort"
	"strings"
)

// minimalCleanPATH is the default search path injected for a
// CleanEnv spawn that does not carry its own PATH. It mirrors the
// POSIX branch of codeexecutor/local's cleanEnvPath (and the
// codeexecutor/container runtime) so all backends resolve bare
// commands against the same minimal set of system directories. The
// e2b sandbox always targets a Linux template, so no Windows branch
// is needed.
const minimalCleanPATH = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// programEnvArgs builds literal env arguments. Caller values override workspace
// defaults and never become shell source or the bootstrap shell environment.
func programEnvArgs(base, spec map[string]string, clean bool) ([]string, error) {
	args := []string{}
	if clean {
		args = append(args, "-i")
	}
	args = append(args, "--")
	if clean {
		if _, ok := spec["PATH"]; !ok {
			if _, ok := base["PATH"]; !ok {
				args = append(args, "PATH="+minimalCleanPATH)
			}
		}
	}
	merged := make(map[string]string, len(base)+len(spec))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range spec {
		merged[key] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return nil, fmt.Errorf("e2b: invalid environment variable name %q", key)
		}
		if strings.ContainsRune(merged[key], 0) {
			return nil, fmt.Errorf("e2b: environment variable %q contains NUL", key)
		}
		args = append(args, key+"="+merged[key])
	}
	return args, nil
}
