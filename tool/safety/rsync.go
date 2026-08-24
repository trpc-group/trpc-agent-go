//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"path"
	"strings"
)

type rsyncArguments struct {
	positionals       []string
	executionPrograms []rsyncExecutionProgram
	remoteOptions     []string
	delete            bool
	dryRun            bool
	unresolved        bool
}

type rsyncExecutionProgram struct {
	selector string
	value    string
}

func scanRsyncExecutionOptions(
	policy Policy,
	args []string,
	depth int,
) []Finding {
	parsed := parseRsyncArguments(args)
	var findings []Finding
	for _, program := range parsed.executionPrograms {
		evidence := "rsync --rsync-path executes an embedded remote command"
		recommendation := "remove --rsync-path or review the complete remote command"
		if program.selector == "--rsh" || program.selector == "-e" {
			evidence = "rsync remote-shell option launches a selected local helper"
			recommendation = "remove the remote-shell selector or review its complete command"
		}
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
			evidence, recommendation,
		))
		if strings.TrimSpace(program.value) == "" {
			continue
		}
		findings = append(findings, scanNestedCommandAtDepth(
			policy, program.value, depth,
		)...)
	}
	if parsed.delete && !parsed.dryRun {
		findings = append(findings, rsyncDeleteFinding(parsed))
	}
	for range parsed.remoteOptions {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "command.indirect_execution",
			"rsync remote option changes the behavior of the other process",
			"remove the remote option or review its receiver-side effects",
		))
	}
	return findings
}

// parseRsyncArguments is shared by execution and network checks so they agree
// on option-value boundaries and effective operands. Unknown syntax sets
// unresolved and is handled conservatively by the caller.
func parseRsyncArguments(args []string) rsyncArguments {
	var parsed rsyncArguments
	options := true
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if options && arg == "--" {
			options = false
			continue
		}
		if !options || !strings.HasPrefix(arg, "-") || arg == "-" {
			parsed.positionals = append(parsed.positionals, arg)
			continue
		}
		if strings.HasPrefix(arg, "--") {
			index = parseRsyncLongOption(args, index, &parsed)
			continue
		}
		consumeNext, recognized, valueOption, value := parseRsyncShortOptions(
			arg, &parsed,
		)
		if !recognized {
			parsed.unresolved = true
		}
		if consumeNext {
			if index+1 >= len(args) {
				parsed.unresolved = true
				if valueOption == 'e' {
					parsed.executionPrograms = append(parsed.executionPrograms,
						rsyncExecutionProgram{selector: "-e"},
					)
				}
				if valueOption == 'M' {
					recordRsyncRemoteOption(&parsed, "")
				}
				continue
			}
			index++
			value = args[index]
		}
		if valueOption == 'e' {
			parsed.executionPrograms = append(parsed.executionPrograms,
				rsyncExecutionProgram{selector: "-e", value: value},
			)
		}
		if valueOption == 'M' {
			recordRsyncRemoteOption(&parsed, value)
		}
	}
	return parsed
}

func rsyncNetworkDestinations(args []string) ([]string, bool) {
	parsed := parseRsyncArguments(args)
	destinations := make([]string, 0, len(parsed.positionals))
	unresolved := parsed.unresolved
	remoteSeen := false
	for _, operand := range parsed.positionals {
		candidate := strings.TrimSpace(strings.Trim(operand, `"'`))
		if strings.HasPrefix(candidate, ":") {
			if !remoteSeen {
				unresolved = true
			}
			continue
		}
		remote, resolved := rsyncRemoteOperand(operand)
		if !remote {
			continue
		}
		if !resolved {
			unresolved = true
			continue
		}
		destinations = append(destinations, operand)
		remoteSeen = true
	}
	return destinations, unresolved
}

func rsyncRemoteOperand(value string) (bool, bool) {
	candidate := strings.TrimSpace(strings.Trim(value, `"'`))
	if candidate == "" {
		return false, true
	}
	if strings.Contains(candidate, "://") {
		if !strings.HasPrefix(strings.ToLower(candidate), "rsync://") {
			return true, false
		}
		_, ok := explicitHost(candidate)
		return true, ok
	}
	colon := strings.Index(candidate, ":")
	if colon < 0 {
		return false, true
	}
	if slash := strings.Index(candidate, "/"); slash >= 0 && slash < colon {
		return false, true
	}
	_, ok := scpRemoteHost(candidate)
	return true, ok
}

func parseRsyncLongOption(
	args []string,
	index int,
	parsed *rsyncArguments,
) int {
	arg := args[index]
	name, value, attached := strings.Cut(arg, "=")
	switch {
	case name == "--dry-run":
		parsed.dryRun = true
		return index
	case name == "--no-dry-run":
		parsed.dryRun = false
		return index
	case rsyncDeleteOption(name):
		parsed.delete = true
		return index
	case rsyncNoDeleteOption(name):
		parsed.delete = false
		return index
	case name == "--rsync-path" || name == "--rsh":
		selector := name
		if attached {
			parsed.executionPrograms = append(parsed.executionPrograms,
				rsyncExecutionProgram{selector: selector, value: value},
			)
			return index
		}
		if index+1 >= len(args) {
			parsed.executionPrograms = append(parsed.executionPrograms,
				rsyncExecutionProgram{selector: selector},
			)
			parsed.unresolved = true
			return index
		}
		parsed.executionPrograms = append(parsed.executionPrograms,
			rsyncExecutionProgram{selector: selector, value: args[index+1]},
		)
		return index + 1
	case name == "--remote-option":
		if attached {
			recordRsyncRemoteOption(parsed, value)
			return index
		}
		if index+1 >= len(args) {
			recordRsyncRemoteOption(parsed, "")
			parsed.unresolved = true
			return index
		}
		recordRsyncRemoteOption(parsed, args[index+1])
		return index + 1
	}
	if _, ok := rsyncLongValueOptions[name]; ok {
		if attached {
			return index
		}
		if index+1 >= len(args) {
			parsed.unresolved = true
			return index
		}
		return index + 1
	}
	if _, ok := rsyncLongFlagOptions[name]; ok && !attached {
		return index
	}
	parsed.unresolved = true
	return index
}

func rsyncDeleteOption(name string) bool {
	return name == "--del" || name == "--delete" ||
		strings.HasPrefix(name, "--delete-")
}

func rsyncNoDeleteOption(name string) bool {
	return name == "--no-delete" || strings.HasPrefix(name, "--no-delete-")
}

func recordRsyncRemoteOption(parsed *rsyncArguments, value string) {
	parsed.remoteOptions = append(parsed.remoteOptions, value)
	name, _, _ := strings.Cut(value, "=")
	switch {
	case rsyncDeleteOption(name):
		parsed.delete = true
	case rsyncNoDeleteOption(name):
		parsed.delete = false
	}
}

func parseRsyncShortOptions(
	arg string,
	parsed *rsyncArguments,
) (bool, bool, byte, string) {
	for index := 1; index < len(arg); index++ {
		option := arg[index]
		if option == 'n' {
			parsed.dryRun = true
			continue
		}
		if strings.ContainsRune(rsyncShortValueOptions, rune(option)) {
			return index+1 == len(arg), true, option, arg[index+1:]
		}
		if !strings.ContainsRune(rsyncShortFlagOptions, rune(option)) {
			return false, false, 0, ""
		}
	}
	return false, true, 0, ""
}

func rsyncDeleteFinding(parsed rsyncArguments) Finding {
	if len(parsed.positionals) >= 2 {
		receiver := path.Clean(strings.TrimSpace(
			parsed.positionals[len(parsed.positionals)-1],
		))
		if receiver == "." || receiver == "/" {
			return newFinding(
				DecisionDeny, RiskCritical, "dangerous.rsync_delete",
				"rsync deletion targets the current directory or filesystem root",
				"use --dry-run and review a narrowly scoped receiver before deletion",
			)
		}
	}
	return newFinding(
		DecisionNeedsHumanReview, RiskHigh, "dangerous.rsync_delete",
		"rsync deletion removes receiver files that are absent from the source",
		"use --dry-run and review the source, receiver, and deletion filters",
	)
}

var rsyncLongValueOptions = map[string]struct{}{
	"--address": {}, "--backup-dir": {}, "--block-size": {},
	"--bwlimit": {}, "--checksum-choice": {}, "--checksum-seed": {},
	"--chmod": {}, "--chown": {}, "--compare-dest": {},
	"--compress-choice": {}, "--compress-level": {}, "--contimeout": {},
	"--copy-as": {}, "--copy-dest": {}, "--debug": {},
	"--exclude": {}, "--exclude-from": {}, "--files-from": {},
	"--filter": {}, "--groupmap": {}, "--iconv": {}, "--include": {},
	"--include-from": {}, "--info": {}, "--link-dest": {},
	"--log-file": {}, "--log-file-format": {}, "--max-alloc": {},
	"--max-delete": {}, "--max-size": {}, "--min-size": {},
	"--modify-window": {}, "--out-format": {}, "--partial-dir": {},
	"--password-file": {}, "--port": {}, "--protocol": {},
	"--remote-option": {}, "--rsh": {}, "--sockopts": {},
	"--suffix": {}, "--temp-dir": {}, "--timeout": {},
	"--usermap": {}, "--write-batch": {}, "--only-write-batch": {},
}

var rsyncLongFlagOptions = map[string]struct{}{
	"--acls": {}, "--archive": {}, "--backup": {}, "--checksum": {},
	"--compress": {}, "--copy-dirlinks": {}, "--copy-links": {},
	"--copy-unsafe-links": {}, "--crtimes": {}, "--cvs-exclude": {},
	"--delay-updates": {}, "--devices": {}, "--dirs": {},
	"--executability": {}, "--existing": {}, "--fake-super": {},
	"--force": {}, "--from0": {}, "--fuzzy": {}, "--group": {},
	"--hard-links": {}, "--human-readable": {}, "--ignore-errors": {},
	"--ignore-existing": {}, "--ignore-missing-args": {},
	"--ignore-non-existing": {}, "--ignore-times": {}, "--inc-recursive": {},
	"--inplace": {}, "--ipv4": {}, "--ipv6": {}, "--itemize-changes": {},
	"--keep-dirlinks": {}, "--links": {}, "--list-only": {},
	"--mkpath": {}, "--munge-links": {}, "--no-implied-dirs": {},
	"--numeric-ids": {}, "--old-args": {}, "--omit-dir-times": {},
	"--omit-link-times": {}, "--one-file-system": {}, "--owner": {},
	"--partial": {}, "--perms": {}, "--preallocate": {},
	"--progress": {}, "--protect-args": {}, "--prune-empty-dirs": {},
	"--quiet": {}, "--recursive": {}, "--relative": {},
	"--remove-source-files": {}, "--safe-links": {}, "--size-only": {},
	"--sparse": {}, "--specials": {}, "--stats": {}, "--super": {},
	"--times": {}, "--update": {}, "--verbose": {}, "--whole-file": {},
	"--xattrs": {},
}

const (
	rsyncShortValueOptions = "BefMT"
	rsyncShortFlagOptions  = "0468ACDEFIJKLNOPRSUWXabcdefghiklmnopqrstuvxyz"
)
