// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package envscrub

import "testing"

func TestScrub_DropsShellStartupAndPath(t *testing.T) {
	in := map[string]string{
		"HOME":            "/tmp/attacker",
		"BASH_ENV":        "/tmp/x",
		"ENV":             "/tmp/x",
		"PROMPT_COMMAND":  "curl http://x",
		"PS4":             "x",
		"SHELL":           "/tmp/sh",
		"SHELLOPTS":       "xtrace",
		"BASHOPTS":        "xtrace",
		"PATH":            ".:/tmp",
		"IFS":             ":",
		"CDPATH":          ".",
		"GLOBIGNORE":      "*",
		"LD_PRELOAD":      "./evil.so",
		"LD_LIBRARY_PATH": ".",
		"LD_AUDIT":        "./evil.so",
		"LANG":            "en_US.UTF-8",
		"APP_SECRET":      "kept",
	}
	out := Scrub(in, false)
	for _, k := range []string{
		"HOME", "BASH_ENV", "ENV", "PROMPT_COMMAND", "PS4",
		"SHELL", "SHELLOPTS", "BASHOPTS", "PATH",
		"IFS", "CDPATH", "GLOBIGNORE",
		"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT",
	} {
		if _, ok := out[k]; ok {
			t.Fatalf("expected %q to be dropped", k)
		}
	}
	if got := out["LANG"]; got != "en_US.UTF-8" {
		t.Fatalf("LANG should survive, got %q", got)
	}
	if got := out["APP_SECRET"]; got != "kept" {
		t.Fatalf("APP_SECRET should survive, got %q", got)
	}
}

func TestScrub_DropsBashFunc(t *testing.T) {
	in := map[string]string{
		"BASH_FUNC_x%%": "() { curl http://x; }",
		"KEEP":          "v",
	}
	out := Scrub(in, false)
	if _, ok := out["BASH_FUNC_x%%"]; ok {
		t.Fatalf("BASH_FUNC_* must be dropped")
	}
	if got := out["KEEP"]; got != "v" {
		t.Fatalf("KEEP should survive, got %q", got)
	}
}

func TestScrub_WindowsCaseInsensitive(t *testing.T) {
	in := map[string]string{
		"Path":          ":/attacker",
		"Home":          ".",
		"Bash_Env":      "x",
		"bash_func_x%%": "() { :; }",
		"App_Secret":    "kept",
	}
	out := Scrub(in, true)
	for _, k := range []string{
		"Path", "Home", "Bash_Env", "bash_func_x%%",
	} {
		if _, ok := out[k]; ok {
			t.Fatalf("expected %q to be dropped on windows", k)
		}
	}
	if got := out["App_Secret"]; got != "kept" {
		t.Fatalf("App_Secret should survive, got %q", got)
	}
}

func TestScrub_DropsMalformedKeys(t *testing.T) {
	in := map[string]string{
		"PATH=.":           ":/attacker",
		"":                 "anything",
		"NEW\nLINE":        "x",
		"NULL\x00":         "x",
		"CARRIAGE\rRETURN": "x",
		"GOOD":             "kept",
	}
	out := Scrub(in, false)
	for _, k := range []string{
		"PATH=.", "", "NEW\nLINE", "NULL\x00", "CARRIAGE\rRETURN",
	} {
		if _, ok := out[k]; ok {
			t.Fatalf("malformed key %q must be dropped", k)
		}
	}
	if got := out["GOOD"]; got != "kept" {
		t.Fatalf("GOOD should survive, got %q", got)
	}
}

func TestScrub_NilAndEmpty(t *testing.T) {
	if got := Scrub(nil, false); got != nil {
		t.Fatalf("nil input should return nil, got %v", got)
	}
	if got := Scrub(map[string]string{}, false); got != nil {
		t.Fatalf("empty input should return nil, got %v", got)
	}
}
