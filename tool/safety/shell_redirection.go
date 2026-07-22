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
		return anyPositionalSystemPath(args)
	case "dd":
		for _, arg := range args {
			if strings.HasPrefix(strings.ToLower(arg), "of=") &&
				isSystemPath(arg[3:]) {
				return true
			}
		}
	case "cp", "mv", "install":
		if optionWritesSystemPath(args, "-t", "--target-directory") {
			return true
		}
		return isSystemPath(lastPositionalArg(args))
	case "curl":
		return optionWritesSystemPath(args, "-o", "--output") ||
			optionWritesSystemPath(args, "", "--output-dir")
	case "wget":
		return optionWritesSystemPath(args, "-O", "--output-document") ||
			optionWritesSystemPath(args, "-P", "--directory-prefix")
	case "sed":
		if !hasOption(args, "-i", "--in-place") {
			return false
		}
		return anyPositionalSystemPath(args)
	}
	return false
}

func anyPositionalSystemPath(args []string) bool {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") && isSystemPath(arg) {
			return true
		}
	}
	return false
}

func lastPositionalArg(args []string) string {
	for index := len(args) - 1; index >= 0; index-- {
		if args[index] != "" && !strings.HasPrefix(args[index], "-") {
			return args[index]
		}
	}
	return ""
}

func optionWritesSystemPath(args []string, short, long string) bool {
	for index, arg := range args {
		lower := strings.ToLower(arg)
		shortLower := strings.ToLower(short)
		longLower := strings.ToLower(long)
		switch {
		case lower == shortLower || lower == longLower:
			if index+1 < len(args) && isSystemPath(args[index+1]) {
				return true
			}
		case strings.HasPrefix(lower, longLower+"="):
			if isSystemPath(arg[len(long)+1:]) {
				return true
			}
		case len(short) == 2 && strings.HasPrefix(lower, shortLower) &&
			len(arg) > len(short) && !strings.HasPrefix(lower, "--"):
			if isSystemPath(arg[len(short):]) {
				return true
			}
		}
	}
	return false
}

func hasOption(args []string, short, long string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == strings.ToLower(short) || lower == strings.ToLower(long) ||
			strings.HasPrefix(lower, strings.ToLower(short)) && len(arg) > len(short) ||
			strings.HasPrefix(lower, strings.ToLower(long)+"=") {
			return true
		}
	}
	return false
}

func isSystemPath(value string) bool {
	normalized := normalizeLexicalPath(value)
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
