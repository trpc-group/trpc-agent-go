//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import "strings"

func scanSEDInlinePrograms(
	policy Policy,
	args []string,
	indirectionDepth int,
) []Finding {
	programs, unresolved := sedInlinePrograms(args)
	var findings []Finding
	if unresolved {
		findings = append(findings, sedExecutionReview(
			"sed program options or external source could not be scanned conservatively",
		))
	}
	for _, program := range programs {
		commands, dynamic, unparsed := sedExecutionCommands(program)
		if dynamic || unparsed {
			findings = append(findings, sedExecutionReview(
				"sed program can execute a dynamic or unparsed command",
			))
		}
		for _, command := range commands {
			findings = append(findings, sedExecutionReview(
				"sed program executes an embedded command",
			))
			findings = append(findings, scanNestedCommandAtDepth(
				policy, command, indirectionDepth+1,
			)...)
		}
	}
	return findings
}

func sedInlinePrograms(args []string) ([]string, bool) {
	sources := sedProgramSources{}
	for index := 0; index < len(args); {
		arg := args[index]
		if arg == "--" {
			sources.consumeAfterSeparator(args[index+1:])
			break
		}
		if next, handled := sources.consumeSourceOption(args, index); handled {
			index = next
			continue
		}
		if next, handled := sources.consumeOtherOption(args, index); handled {
			index = next
			continue
		}
		if !sources.specified {
			sources.programs = append(sources.programs, arg)
			sources.specified = true
		}
		index++
	}
	return sources.programs, sources.unresolved
}

type sedProgramSources struct {
	programs   []string
	specified  bool
	unresolved bool
}

func (s *sedProgramSources) consumeAfterSeparator(operands []string) {
	if s.specified {
		return
	}
	if len(operands) == 0 {
		s.unresolved = true
		return
	}
	s.programs = append(s.programs, operands[0])
}

func (s *sedProgramSources) consumeSourceOption(
	args []string,
	index int,
) (int, bool) {
	arg := args[index]
	switch {
	case arg == "-e" || arg == "--expression":
		s.specified = true
		if index+1 >= len(args) {
			s.unresolved = true
			return index + 1, true
		}
		s.programs = append(s.programs, args[index+1])
		return index + 2, true
	case strings.HasPrefix(arg, "--expression="):
		s.specified = true
		s.programs = append(s.programs, strings.TrimPrefix(arg, "--expression="))
		return index + 1, true
	case strings.HasPrefix(arg, "-e") && len(arg) > 2:
		s.specified = true
		s.programs = append(s.programs, arg[2:])
		return index + 1, true
	case arg == "-f" || arg == "--file":
		s.specified = true
		if index+1 >= len(args) {
			s.unresolved = true
			return index + 1, true
		}
		s.unresolved = s.unresolved || args[index+1] != "-"
		return index + 2, true
	case strings.HasPrefix(arg, "--file="):
		s.specified = true
		s.unresolved = s.unresolved || strings.TrimPrefix(arg, "--file=") != "-"
		return index + 1, true
	case strings.HasPrefix(arg, "-f") && len(arg) > 2:
		s.specified = true
		s.unresolved = s.unresolved || arg[2:] != "-"
		return index + 1, true
	default:
		return index, false
	}
}

func (s *sedProgramSources) consumeOtherOption(
	args []string,
	index int,
) (int, bool) {
	arg := args[index]
	switch {
	case arg == "-l" || arg == "--line-length":
		if index+1 >= len(args) {
			s.unresolved = true
			return index + 1, true
		}
		return index + 2, true
	case strings.HasPrefix(arg, "--line-length="):
		return index + 1, true
	case sedAmbiguousInPlaceOption(arg):
		s.unresolved = true
		return index + 1, true
	case sedNoValueOption(arg):
		return index + 1, true
	case strings.HasPrefix(arg, "-") && arg != "-":
		s.unresolved = true
		return index + 1, true
	default:
		return index, false
	}
}

func sedAmbiguousInPlaceOption(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return false
	}
	for index := 1; index < len(arg); index++ {
		switch arg[index] {
		case 'E', 'n', 'r', 's', 'u', 'z':
			continue
		case 'i':
			return index+1 == len(arg)
		default:
			return false
		}
	}
	return false
}

func sedNoValueOption(arg string) bool {
	switch arg {
	case "--debug", "--follow-symlinks", "--help", "--null-data",
		"--posix", "--quiet", "--regexp-extended", "--sandbox",
		"--separate", "--silent", "--unbuffered", "--version":
		return true
	}
	if strings.HasPrefix(arg, "--in-place") {
		return arg == "--in-place" || strings.HasPrefix(arg, "--in-place=")
	}
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return false
	}
	for index := 1; index < len(arg); index++ {
		switch arg[index] {
		case 'E', 'n', 'r', 's', 'u', 'z':
			continue
		case 'i', 'l':
			return true
		default:
			return false
		}
	}
	return true
}

func sedExecutionCommands(program string) ([]string, bool, bool) {
	var commands []string
	dynamic := false
	unparsed := false
	for index := 0; index < len(program); {
		index = skipSEDCommandSeparators(program, index)
		if index >= len(program) {
			break
		}
		if program[index] == '#' {
			index = skipSEDLine(program, index)
			continue
		}
		var ok bool
		index, ok = skipSEDAddresses(program, index)
		if !ok || index >= len(program) {
			unparsed = true
			break
		}
		command := program[index]
		index++
		switch command {
		case '{', '}':
			continue
		case 'e':
			end := skipSEDLine(program, index)
			nested := strings.TrimSpace(program[index:end])
			if nested == "" {
				dynamic = true
			} else {
				commands = append(commands, nested)
			}
			index = end
		case 's':
			var replacement string
			var executes bool
			replacement, executes, index, ok = parseSEDSubstitution(program, index)
			if !ok {
				unparsed = true
				index = skipSEDLine(program, index)
				continue
			}
			if !executes {
				continue
			}
			if nested, static := staticSEDReplacement(replacement); static &&
				strings.TrimSpace(nested) != "" {
				commands = append(commands, nested)
			} else {
				dynamic = true
			}
		case 'a', 'c', 'i', 'r', 'R', 'w', 'W':
			index = skipSEDLine(program, index)
		case 'y':
			index, ok = skipSEDTransliteration(program, index)
			if !ok {
				unparsed = true
				index = skipSEDLine(program, index)
			}
		default:
			if !strings.ContainsRune(":bBdDgGhHlnNpPqQtTx=Fvz", rune(command)) {
				unparsed = true
				index = skipSEDLine(program, index)
				continue
			}
			index = skipSEDCommand(program, index)
		}
	}
	return commands, dynamic, unparsed
}

func skipSEDCommandSeparators(program string, index int) int {
	for index < len(program) {
		switch program[index] {
		case ' ', '\t', '\r', '\n', ';':
			index++
		default:
			return index
		}
	}
	return index
}

func skipSEDAddresses(program string, index int) (int, bool) {
	for address := 0; address < 2; address++ {
		index = skipSEDSpaces(program, index)
		next, found, ok := skipSEDAddress(program, index)
		if !ok {
			return index, false
		}
		if !found {
			break
		}
		index = skipSEDSpaces(program, next)
		if address == 0 && index < len(program) && program[index] == ',' {
			index++
			continue
		}
		break
	}
	index = skipSEDSpaces(program, index)
	if index < len(program) && program[index] == '!' {
		index = skipSEDSpaces(program, index+1)
	}
	return index, true
}

func skipSEDAddress(program string, index int) (int, bool, bool) {
	if index >= len(program) {
		return index, false, true
	}
	switch {
	case program[index] >= '0' && program[index] <= '9':
		return skipSEDNumericAddress(program, index), true, true
	case program[index] == '$':
		return index + 1, true, true
	case program[index] == '/':
		next, _, ok := readSEDDelimited(program, index+1, '/')
		return next, true, ok
	case program[index] == '\\' && index+1 < len(program):
		delimiter := program[index+1]
		next, _, ok := readSEDDelimited(program, index+2, delimiter)
		return next, true, ok
	case program[index] == '+' || program[index] == '~':
		return skipSEDRelativeAddress(program, index)
	default:
		return index, false, true
	}
}

func skipSEDNumericAddress(program string, index int) int {
	index = skipSEDDigits(program, index)
	if index < len(program) && program[index] == '~' {
		index = skipSEDDigits(program, index+1)
	}
	return index
}

func skipSEDRelativeAddress(program string, index int) (int, bool, bool) {
	index++
	start := index
	index = skipSEDDigits(program, index)
	found := index > start
	return index, found, found
}

func skipSEDDigits(program string, index int) int {
	for index < len(program) && program[index] >= '0' && program[index] <= '9' {
		index++
	}
	return index
}

func parseSEDSubstitution(program string, index int) (string, bool, int, bool) {
	if index >= len(program) || program[index] == '\n' || program[index] == '\\' {
		return "", false, index, false
	}
	delimiter := program[index]
	next, _, ok := readSEDDelimited(program, index+1, delimiter)
	if !ok {
		return "", false, next, false
	}
	next, replacement, ok := readSEDDelimited(program, next, delimiter)
	if !ok {
		return "", false, next, false
	}
	flagsStart := next
	for next < len(program) && !strings.ContainsRune(" \t\r\n;", rune(program[next])) {
		next++
	}
	flags := program[flagsStart:next]
	return replacement, strings.ContainsRune(flags, 'e'), next, true
}

func skipSEDTransliteration(program string, index int) (int, bool) {
	if index >= len(program) || program[index] == '\n' || program[index] == '\\' {
		return index, false
	}
	delimiter := program[index]
	next, _, ok := readSEDDelimited(program, index+1, delimiter)
	if !ok {
		return next, false
	}
	next, _, ok = readSEDDelimited(program, next, delimiter)
	return next, ok
}

func readSEDDelimited(program string, index int, delimiter byte) (int, string, bool) {
	start := index
	for index < len(program) {
		if program[index] == '\n' {
			return index, program[start:index], false
		}
		if program[index] == '\\' {
			if index+1 >= len(program) {
				return len(program), program[start:index], false
			}
			index += 2
			continue
		}
		if program[index] == delimiter {
			return index + 1, program[start:index], true
		}
		index++
	}
	return index, program[start:index], false
}

func staticSEDReplacement(replacement string) (string, bool) {
	var command strings.Builder
	for index := 0; index < len(replacement); index++ {
		current := replacement[index]
		if current == '&' {
			return "", false
		}
		if current != '\\' {
			command.WriteByte(current)
			continue
		}
		if index+1 >= len(replacement) {
			return "", false
		}
		index++
		next := replacement[index]
		if next >= '0' && next <= '9' || strings.ContainsRune("&ELUlu", rune(next)) {
			return "", false
		}
		switch next {
		case 'n':
			command.WriteByte('\n')
		case '\\':
			command.WriteByte('\\')
		default:
			command.WriteByte(next)
		}
	}
	return command.String(), true
}

func skipSEDSpaces(program string, index int) int {
	for index < len(program) && strings.ContainsRune(" \t\r", rune(program[index])) {
		index++
	}
	return index
}

func skipSEDCommand(program string, index int) int {
	for index < len(program) && program[index] != ';' && program[index] != '\n' {
		index++
	}
	return index
}

func skipSEDLine(program string, index int) int {
	for index < len(program) && program[index] != '\n' {
		index++
	}
	return index
}

func sedExecutionReview(evidence string) Finding {
	return newFinding(
		DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
		evidence,
		"remove sed process execution or review the embedded command",
	)
}
