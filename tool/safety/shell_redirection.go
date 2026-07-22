//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "strings"

func containsActiveRedirection(command string) bool {
	return visitActiveShellBytes(command, func(_ int, character byte) bool {
		return character == '<' || character == '>'
	})
}

func containsActiveSystemWrite(command string) bool {
	return visitActiveShellBytes(command, func(index int, character byte) bool {
		if character != '>' {
			return false
		}
		target := shellWordAfterRedirection(command[index+1:])
		return isSystemPath(target)
	})
}

func visitActiveShellBytes(command string, visit func(int, byte) bool) bool {
	var (
		singleQuoted bool
		doubleQuoted bool
		escaped      bool
	)
	for index := 0; index < len(command); index++ {
		character := command[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && !singleQuoted {
			escaped = true
			continue
		}
		switch character {
		case '\'':
			if !doubleQuoted {
				singleQuoted = !singleQuoted
			}
			continue
		case '"':
			if !singleQuoted {
				doubleQuoted = !doubleQuoted
			}
			continue
		}
		if !singleQuoted && !doubleQuoted && visit(index, character) {
			return true
		}
	}
	return false
}

func shellWordAfterRedirection(input string) string {
	input = strings.TrimLeft(input, ">| \t\r\n")
	if input == "" || input[0] == '&' {
		return ""
	}
	if input[0] == '\'' || input[0] == '"' {
		quote := input[0]
		for index := 1; index < len(input); index++ {
			if input[index] == quote && (quote == '\'' || input[index-1] != '\\') {
				return input[1:index]
			}
		}
		return ""
	}
	end := 0
	for end < len(input) && !strings.ContainsRune(" \t\r\n;|&<>", rune(input[end])) {
		end++
	}
	return input[:end]
}

func commandWritesSystemPath(name string, args []string) bool {
	switch name {
	case "tee", "truncate":
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") && isSystemPath(arg) {
				return true
			}
		}
	case "dd":
		for _, arg := range args {
			if strings.HasPrefix(strings.ToLower(arg), "of=") && isSystemPath(arg[3:]) {
				return true
			}
		}
	}
	return false
}

func isSystemPath(path string) bool {
	normalized := strings.ToLower(strings.TrimSpace(path))
	normalized = strings.Trim(normalized, "'\"")
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	for strings.Contains(normalized, "//") {
		normalized = strings.ReplaceAll(normalized, "//", "/")
	}
	if len(normalized) >= 3 && normalized[1] == ':' && normalized[2] == '/' {
		normalized = normalized[2:]
	}
	if normalized == "/" {
		return true
	}
	for _, prefix := range []string{
		"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc",
		"/program files", "/root", "/sbin", "/sys", "/usr", "/var", "/windows",
	} {
		if normalized == prefix || strings.HasPrefix(normalized, prefix+"/") {
			return true
		}
	}
	return false
}
