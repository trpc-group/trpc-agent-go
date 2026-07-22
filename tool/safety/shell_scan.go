//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// containsActiveShellExpansion identifies expansion syntax that the shell
// would evaluate. Text inside single quotes and escaped metacharacters are
// literals, so treating them as active would create avoidable false positives.
// Structural acceptance remains the responsibility of internal/shellsafe.
func containsActiveShellExpansion(command string) bool {
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
		if singleQuoted {
			continue
		}
		if character == '`' {
			return true
		}
		if character == '$' && index+1 < len(command) &&
			isShellParameterStart(command[index+1]) {
			return true
		}
	}
	return false
}

func isShellParameterStart(character byte) bool {
	if character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' {
		return true
	}
	switch character {
	case '_', '(', '{', '*', '@', '$', '#', '?', '!', '-':
		return true
	default:
		return false
	}
}
