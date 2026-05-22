//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

var coreEnvironmentKeys = map[string]struct{}{
	"PATH":     {},
	"SHELL":    {},
	"HOME":     {},
	"TMPDIR":   {},
	"TMP":      {},
	"TEMP":     {},
	"LANG":     {},
	"LC_ALL":   {},
	"LC_CTYPE": {},
	"USER":     {},
	"LOGNAME":  {},
}

var windowsCoreEnvironmentKeys = map[string]struct{}{
	"PATH":                   {},
	"PATHEXT":                {},
	"USERNAME":               {},
	"USERPROFILE":            {},
	"LOCALAPPDATA":           {},
	"APPDATA":                {},
	"TEMP":                   {},
	"TMP":                    {},
	"PROCESSOR_ARCHITECTURE": {},
	"OS":                     {},
}

var secretNameFragments = []string{
	"KEY",
	"TOKEN",
	"SECRET",
	"PASSWORD",
	"CREDENTIAL",
}

func (r *Runtime) buildEnvironment(
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) []string {
	env := map[string]string{}
	host := hostEnvMap()
	switch r.envPolicy.Inherit {
	case ShellEnvironmentPolicyInheritNone:
	case ShellEnvironmentPolicyInheritCore:
		for k, v := range host {
			if isCoreEnvironmentKey(k) {
				env[k] = v
			}
		}
	default:
		for k, v := range host {
			env[k] = v
		}
	}
	if r.envPolicy.ApplyDefaultExcludes {
		for k := range env {
			if isSecretName(k) {
				delete(env, k)
			}
		}
	}
	for _, pattern := range r.envPolicy.Exclude {
		for k := range env {
			if envNameMatch(pattern, k) {
				delete(env, k)
			}
		}
	}
	for k, v := range r.envPolicy.Set {
		if k != "" {
			env[k] = v
		}
	}
	for k, v := range spec.Env {
		if k != "" {
			env[k] = v
		}
	}
	if len(r.envPolicy.IncludeOnly) > 0 {
		for k := range env {
			if !envNameMatchesAny(r.envPolicy.IncludeOnly, k) {
				delete(env, k)
			}
		}
	}
	home := filepath.Join(ws.Path, "home")
	tmp := filepath.Join(ws.Path, "tmp")
	work := filepath.Join(ws.Path, codeexecutor.DirWork)
	out := filepath.Join(ws.Path, codeexecutor.DirOut)
	runs := filepath.Join(ws.Path, codeexecutor.DirRuns)
	env["HOME"] = home
	env["TMPDIR"] = tmp
	env["TMP"] = tmp
	env["TEMP"] = tmp
	env[codeexecutor.WorkspaceEnvDirKey] = ws.Path
	env[codeexecutor.EnvWorkDir] = work
	env[codeexecutor.EnvOutputDir] = out
	env[codeexecutor.EnvRunDir] = runs
	env[codeexecutor.EnvSkillsDir] = filepath.Join(ws.Path, codeexecutor.DirSkills)
	if env["PATH"] == "" {
		env["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return envSlice(env)
}

func isCoreEnvironmentKey(name string) bool {
	keys := coreEnvironmentKeys
	if runtime.GOOS == "windows" {
		keys = windowsCoreEnvironmentKeys
	}
	for k := range keys {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

func hostEnvMap() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		env[k] = v
	}
	return env
}

func envSlice(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func envNameMatch(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	pattern = strings.ToLower(pattern)
	name = strings.ToLower(name)
	if strings.ContainsAny(pattern, "*?[") {
		ok, err := path.Match(pattern, name)
		return err == nil && ok
	}
	return pattern == name
}

func envNameMatchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if envNameMatch(pattern, name) {
			return true
		}
	}
	return false
}

// redactEnvironment returns a copy safe for diagnostics.
func redactEnvironment(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		if isSecretName(k) {
			v = "<redacted>"
		}
		out = append(out, k+"="+v)
	}
	return out
}

func isSecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, frag := range secretNameFragments {
		if strings.Contains(upper, frag) {
			return true
		}
	}
	return false
}
