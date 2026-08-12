//go:build linux

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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

const (
	seccompDataOffNR   = 0
	seccompDataOffArch = 4
	seccompDataOffArg0 = 16
	seccompDataOffArg1 = 24

	auditArchX86_64  = 0xc000003e
	auditArchI386    = 0x40000003
	auditArchAARCH64 = 0xc00000b7
	auditArchARM     = 0x40000028

	x32SyscallBit = 0x40000000

	afUNIX       = 1
	sockDGRAM    = 2
	sockTypeMask = 0xf

	seccompRetKillProcess = 0x80000000
	seccompRetAllow       = 0x7fff0000
	seccompRetErrno       = 0x00050000
	seccompRetEPERM       = seccompRetErrno | uint32(1) // EPERM

	sysSocketAMD64          = 41
	sysSocketpairAMD64      = 53
	sysIOUringSetupAMD64    = 425
	sysIOUringEnterAMD64    = 426
	sysIOUringRegisterAMD64 = 427
	sysSocketcallI386       = 102
	sysSocketI386           = 359
	sysSocketpairI386       = 360
	sysIOUringSetupI386     = 425
	sysIOUringEnterI386     = 426
	sysIOUringRegisterI386  = 427
	sysSocketARM64          = 198
	sysSocketpairARM64      = 199
	sysIOUringSetupARM64    = 425
	sysIOUringEnterARM64    = 426
	sysIOUringRegisterARM64 = 427
	sysSocketARM32          = 281
	sysSocketpairARM32      = 288
	sysIOUringSetupARM32    = 425
	sysIOUringEnterARM32    = 426
	sysIOUringRegisterARM32 = 427

	minRestrictedKernelMajor = 4
	minRestrictedKernelMinor = 8
)

type seccompArchPolicy struct {
	name            string
	auditArch       uint32
	rejectX32       bool
	socket          uint32
	socketpair      uint32
	ioUringSetup    uint32
	ioUringEnter    uint32
	ioUringRegister uint32
	compat          *seccompCompatPolicy
}

type seccompCompatPolicy struct {
	name            string
	auditArch       uint32
	socket          uint32
	socketpair      uint32
	socketcall      uint32
	ioUringSetup    uint32
	ioUringEnter    uint32
	ioUringRegister uint32
}

var (
	seccompPolicyI386 = seccompCompatPolicy{
		name:            "i386",
		auditArch:       auditArchI386,
		socket:          sysSocketI386,
		socketpair:      sysSocketpairI386,
		socketcall:      sysSocketcallI386,
		ioUringSetup:    sysIOUringSetupI386,
		ioUringEnter:    sysIOUringEnterI386,
		ioUringRegister: sysIOUringRegisterI386,
	}
	seccompPolicyARM32 = seccompCompatPolicy{
		name:            "arm",
		auditArch:       auditArchARM,
		socket:          sysSocketARM32,
		socketpair:      sysSocketpairARM32,
		ioUringSetup:    sysIOUringSetupARM32,
		ioUringEnter:    sysIOUringEnterARM32,
		ioUringRegister: sysIOUringRegisterARM32,
	}
	seccompPolicyAMD64 = seccompArchPolicy{
		name:            "amd64",
		auditArch:       auditArchX86_64,
		rejectX32:       true,
		socket:          sysSocketAMD64,
		socketpair:      sysSocketpairAMD64,
		ioUringSetup:    sysIOUringSetupAMD64,
		ioUringEnter:    sysIOUringEnterAMD64,
		ioUringRegister: sysIOUringRegisterAMD64,
		compat:          &seccompPolicyI386,
	}
	seccompPolicyARM64 = seccompArchPolicy{
		name:            "arm64",
		auditArch:       auditArchAARCH64,
		rejectX32:       false,
		socket:          sysSocketARM64,
		socketpair:      sysSocketpairARM64,
		ioUringSetup:    sysIOUringSetupARM64,
		ioUringEnter:    sysIOUringEnterARM64,
		ioUringRegister: sysIOUringRegisterARM64,
		compat:          &seccompPolicyARM32,
	}
)

func seccompPolicyForGOARCH(goarch string) (seccompArchPolicy, error) {
	switch goarch {
	case "amd64":
		return seccompPolicyAMD64, nil
	case "arm64":
		return seccompPolicyARM64, nil
	default:
		return seccompArchPolicy{}, fmt.Errorf(
			"linux restricted AF_UNIX seccomp is unsupported on GOARCH %s",
			goarch,
		)
	}
}

func nativeSeccompPolicy() (seccompArchPolicy, error) {
	return seccompPolicyForGOARCH(runtime.GOARCH)
}

// seccompArgMatchKind selects how a syscall argument is compared.
type seccompArgMatchKind int

const (
	// seccompArgEqual compares the low 32 bits of argN to Value.
	seccompArgEqual seccompArgMatchKind = iota
	// seccompArgMaskedEqual compares (argN_low32 & Mask) to Value.
	seccompArgMaskedEqual
)

// seccompArgMatch is one AND-ed argument constraint for a rule.
type seccompArgMatch struct {
	Arg   int
	Kind  seccompArgMatchKind
	Mask  uint32
	Value uint32
}

// seccompRule denies one syscall number with EPERM when every match succeeds.
// Match is an AND chain. An empty Match means unconditional EPERM.
// Only one rule per syscall number is allowed; OR of alternate parameter
// combinations is intentionally unsupported.
type seccompRule struct {
	NR    uint32
	Match []seccompArgMatch
}

const (
	labelNativeArch   seccompLabel = "arch_native"
	labelCompatArch   seccompLabel = "arch_compat"
	labelAfterX32Kill seccompLabel = "native_after_x32_kill"
	bpfMaxInsns                    = 4096
)

type seccompLabel string

type labelFixup struct {
	insnIdx    int
	trueLabel  seccompLabel
	setTrue    bool
	falseLabel seccompLabel
	setFalse   bool
}

type filterBuilder struct {
	insns  []bpf.Instruction
	labels map[seccompLabel]int
	fixups []labelFixup
}

func newFilterBuilder() *filterBuilder {
	return &filterBuilder{
		labels: make(map[seccompLabel]int),
	}
}

func (b *filterBuilder) emit(insn bpf.Instruction) int {
	idx := len(b.insns)
	b.insns = append(b.insns, insn)
	return idx
}

func (b *filterBuilder) define(label seccompLabel) error {
	if _, exists := b.labels[label]; exists {
		return fmt.Errorf("duplicate seccomp label %q", label)
	}
	b.labels[label] = len(b.insns)
	return nil
}

func (b *filterBuilder) jumpIf(
	cond bpf.JumpTest,
	val uint32,
	trueLabel *seccompLabel,
	falseLabel *seccompLabel,
) {
	idx := b.emit(bpf.JumpIf{Cond: cond, Val: val})
	fix := labelFixup{insnIdx: idx}
	if trueLabel != nil {
		fix.trueLabel = *trueLabel
		fix.setTrue = true
	}
	if falseLabel != nil {
		fix.falseLabel = *falseLabel
		fix.setFalse = true
	}
	b.fixups = append(b.fixups, fix)
}

func (b *filterBuilder) skipTo(from int, label seccompLabel) (uint8, error) {
	target, ok := b.labels[label]
	if !ok {
		return 0, fmt.Errorf("undefined seccomp label %q", label)
	}
	if target <= from {
		return 0, fmt.Errorf("backward seccomp jump to %q", label)
	}
	skip := target - from - 1
	if skip < 0 || skip > 255 {
		return 0, fmt.Errorf("seccomp jump to %q exceeds uint8 skip (%d)", label, skip)
	}
	// skip is validated into [0,255] above.
	return uint8(skip), nil //nolint:gosec // G115: bounded by the check above
}

func (b *filterBuilder) resolve() error {
	for _, fix := range b.fixups {
		jump, ok := b.insns[fix.insnIdx].(bpf.JumpIf)
		if !ok {
			return fmt.Errorf("seccomp fixup at %d is not JumpIf", fix.insnIdx)
		}
		if fix.setTrue {
			skip, err := b.skipTo(fix.insnIdx, fix.trueLabel)
			if err != nil {
				return err
			}
			jump.SkipTrue = skip
		}
		if fix.setFalse {
			skip, err := b.skipTo(fix.insnIdx, fix.falseLabel)
			if err != nil {
				return err
			}
			jump.SkipFalse = skip
		}
		b.insns[fix.insnIdx] = jump
	}
	return nil
}

func validateSeccompPolicy(policy seccompArchPolicy) error {
	if policy.auditArch == 0 ||
		policy.socket == 0 ||
		policy.socketpair == 0 ||
		policy.ioUringSetup == 0 ||
		policy.ioUringEnter == 0 ||
		policy.ioUringRegister == 0 {
		return errors.New("invalid seccomp architecture policy")
	}
	if policy.compat != nil {
		compat := policy.compat
		if compat.auditArch == 0 ||
			compat.socket == 0 ||
			compat.socketpair == 0 ||
			compat.ioUringSetup == 0 ||
			compat.ioUringEnter == 0 ||
			compat.ioUringRegister == 0 {
			return errors.New("invalid compat seccomp architecture policy")
		}
		if compat.auditArch == policy.auditArch {
			return errors.New("compat seccomp audit architecture duplicates native")
		}
	}
	return nil
}

func rulesForPolicy(policy seccompArchPolicy) ([]seccompRule, error) {
	if err := validateSeccompPolicy(policy); err != nil {
		return nil, err
	}
	// One rule per syscall; Match is AND. Do not encode OR by stacking
	// alternate parameter shapes into a single rule.
	rules := []seccompRule{
		{
			NR: policy.socket,
			Match: []seccompArgMatch{{
				Arg:   0,
				Kind:  seccompArgEqual,
				Value: afUNIX,
			}},
		},
		{
			NR: policy.socketpair,
			Match: []seccompArgMatch{
				{
					Arg:   0,
					Kind:  seccompArgEqual,
					Value: afUNIX,
				},
				{
					Arg:   1,
					Kind:  seccompArgMaskedEqual,
					Mask:  sockTypeMask,
					Value: sockDGRAM,
				},
			},
		},
		{NR: policy.ioUringSetup},
		{NR: policy.ioUringEnter},
		{NR: policy.ioUringRegister},
	}
	if err := validateSeccompRules(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func rulesForCompatPolicy(policy seccompCompatPolicy) ([]seccompRule, error) {
	rules := []seccompRule{
		{
			NR: policy.socket,
			Match: []seccompArgMatch{{
				Arg:   0,
				Kind:  seccompArgEqual,
				Value: afUNIX,
			}},
		},
		{
			NR: policy.socketpair,
			Match: []seccompArgMatch{
				{
					Arg:   0,
					Kind:  seccompArgEqual,
					Value: afUNIX,
				},
				{
					Arg:   1,
					Kind:  seccompArgMaskedEqual,
					Mask:  sockTypeMask,
					Value: sockDGRAM,
				},
			},
		},
	}
	if policy.socketcall != 0 {
		// i386 socketcall stores socket arguments behind a userspace pointer,
		// which classic seccomp BPF cannot dereference safely. Deny the entire
		// legacy multiplexor; modern direct socket syscalls retain the same
		// AF_UNIX behavior as the native ABI.
		rules = append(rules, seccompRule{NR: policy.socketcall})
	}
	rules = append(rules,
		seccompRule{NR: policy.ioUringSetup},
		seccompRule{NR: policy.ioUringEnter},
		seccompRule{NR: policy.ioUringRegister},
	)
	if err := validateSeccompRules(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func validateSeccompRules(rules []seccompRule) error {
	if len(rules) == 0 {
		return errors.New("seccomp rule table is empty")
	}
	seen := make(map[uint32]struct{}, len(rules))
	for i, rule := range rules {
		if rule.NR == 0 {
			return fmt.Errorf("seccomp rule %d has zero syscall number", i)
		}
		if _, dup := seen[rule.NR]; dup {
			return fmt.Errorf("duplicate seccomp syscall number %d", rule.NR)
		}
		seen[rule.NR] = struct{}{}
		for j, match := range rule.Match {
			if match.Arg != 0 && match.Arg != 1 {
				return fmt.Errorf("seccomp rule %d match %d: unsupported arg index %d", i, j, match.Arg)
			}
			switch match.Kind {
			case seccompArgEqual:
				if match.Mask != 0 {
					return fmt.Errorf("seccomp rule %d match %d: equal match must not set mask", i, j)
				}
			case seccompArgMaskedEqual:
				if match.Mask == 0 {
					return fmt.Errorf("seccomp rule %d match %d: masked equal requires non-zero mask", i, j)
				}
			default:
				return fmt.Errorf("seccomp rule %d match %d: unknown match kind %d", i, j, match.Kind)
			}
		}
	}
	return nil
}

func argOffset(arg int) (uint32, error) {
	switch arg {
	case 0:
		return seccompDataOffArg0, nil
	case 1:
		return seccompDataOffArg1, nil
	default:
		return 0, fmt.Errorf("unsupported seccomp arg index %d", arg)
	}
}

func ruleLabel(prefix string, i int) seccompLabel {
	return seccompLabel(fmt.Sprintf("%s_rule_%d", prefix, i))
}

func ruleAllowLabel(prefix string, i int) seccompLabel {
	return seccompLabel(fmt.Sprintf("%s_rule_%d_allow", prefix, i))
}

func emitArgMatch(b *filterBuilder, match seccompArgMatch, nextTrue, onFail seccompLabel) error {
	off, err := argOffset(match.Arg)
	if err != nil {
		return err
	}
	b.emit(bpf.LoadAbsolute{Off: off, Size: 4})
	switch match.Kind {
	case seccompArgEqual:
		b.jumpIf(bpf.JumpEqual, match.Value, &nextTrue, &onFail)
	case seccompArgMaskedEqual:
		b.emit(bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: match.Mask})
		b.jumpIf(bpf.JumpEqual, match.Value, &nextTrue, &onFail)
	default:
		return fmt.Errorf("unknown seccomp match kind %d", match.Kind)
	}
	return nil
}

func buildAFUNIXBlockFilter(policy seccompArchPolicy) ([]bpf.Instruction, error) {
	rules, err := rulesForPolicy(policy)
	if err != nil {
		return nil, err
	}
	insns, err := linuxCompileSeccompFilter(policy, rules)
	if err != nil {
		return nil, err
	}
	if err := linuxValidateCompiledFilter(insns); err != nil {
		return nil, err
	}
	return insns, nil
}

var (
	linuxCompileSeccompFilter   = compileSeccompFilter
	linuxValidateCompiledFilter = validateCompiledFilter
)

func compileSeccompFilter(policy seccompArchPolicy, rules []seccompRule) ([]bpf.Instruction, error) {
	if err := validateSeccompRules(rules); err != nil {
		return nil, err
	}
	var compatRules []seccompRule
	if policy.compat != nil {
		var err error
		compatRules, err = rulesForCompatPolicy(*policy.compat)
		if err != nil {
			return nil, err
		}
	}
	b := newFilterBuilder()

	b.emit(bpf.LoadAbsolute{Off: seccompDataOffArch, Size: 4})
	nativeArch := labelNativeArch
	b.jumpIf(bpf.JumpEqual, policy.auditArch, &nativeArch, nil)
	if policy.compat != nil {
		compatArch := labelCompatArch
		b.jumpIf(bpf.JumpEqual, policy.compat.auditArch, &compatArch, nil)
	}
	b.emit(bpf.RetConstant{Val: seccompRetKillProcess})
	if err := b.define(labelNativeArch); err != nil {
		return nil, err
	}

	if policy.rejectX32 {
		b.emit(bpf.LoadAbsolute{Off: seccompDataOffNR, Size: 4})
		afterX32 := labelAfterX32Kill
		b.jumpIf(bpf.JumpBitsSet, x32SyscallBit, nil, &afterX32)
		b.emit(bpf.RetConstant{Val: seccompRetKillProcess})
		if err := b.define(labelAfterX32Kill); err != nil {
			return nil, err
		}
	}

	if err := emitSeccompRuleProgram(b, "native", rules); err != nil {
		return nil, err
	}
	if policy.compat != nil {
		if err := b.define(labelCompatArch); err != nil {
			return nil, err
		}
		if err := emitSeccompRuleProgram(b, "compat", compatRules); err != nil {
			return nil, err
		}
	}

	if err := b.resolve(); err != nil {
		return nil, err
	}
	return b.insns, nil
}

func emitSeccompRuleProgram(
	b *filterBuilder,
	prefix string,
	rules []seccompRule,
) error {
	b.emit(bpf.LoadAbsolute{Off: seccompDataOffNR, Size: 4})
	for i := range rules {
		target := ruleLabel(prefix, i)
		b.jumpIf(bpf.JumpEqual, rules[i].NR, &target, nil)
	}
	b.emit(bpf.RetConstant{Val: seccompRetAllow})

	for i, rule := range rules {
		if err := b.define(ruleLabel(prefix, i)); err != nil {
			return err
		}
		allow := ruleAllowLabel(prefix, i)
		if len(rule.Match) == 0 {
			b.emit(bpf.RetConstant{Val: seccompRetEPERM})
			continue
		}
		for j, match := range rule.Match {
			var nextTrue seccompLabel
			if j+1 < len(rule.Match) {
				nextTrue = seccompLabel(
					fmt.Sprintf("%s_rule_%d_m%d", prefix, i, j+1),
				)
			} else {
				nextTrue = seccompLabel(prefix + "_deny_eperm")
			}
			if j > 0 {
				matchLabel := seccompLabel(
					fmt.Sprintf("%s_rule_%d_m%d", prefix, i, j),
				)
				if err := b.define(matchLabel); err != nil {
					return err
				}
			}
			if err := emitArgMatch(b, match, nextTrue, allow); err != nil {
				return err
			}
		}
		if err := b.define(allow); err != nil {
			return err
		}
		b.emit(bpf.RetConstant{Val: seccompRetAllow})
	}

	if err := b.define(seccompLabel(prefix + "_deny_eperm")); err != nil {
		return err
	}
	b.emit(bpf.RetConstant{Val: seccompRetEPERM})
	return nil
}

func validateCompiledFilter(insns []bpf.Instruction) error {
	if len(insns) == 0 {
		return errors.New("compiled AF_UNIX seccomp filter is empty")
	}
	if len(insns) > bpfMaxInsns {
		return fmt.Errorf("compiled AF_UNIX seccomp filter has %d insns, max %d", len(insns), bpfMaxInsns)
	}
	reachable := make([]bool, len(insns))
	var walkErr error
	var walk func(pc int)
	walk = func(pc int) {
		if walkErr != nil {
			return
		}
		if pc < 0 || pc >= len(insns) {
			walkErr = fmt.Errorf("seccomp jump target %d out of range [0,%d)", pc, len(insns))
			return
		}
		if reachable[pc] {
			return
		}
		reachable[pc] = true
		switch insn := insns[pc].(type) {
		case bpf.RetConstant, bpf.RetA:
			return
		case bpf.JumpIf:
			walk(pc + 1 + int(insn.SkipTrue))
			walk(pc + 1 + int(insn.SkipFalse))
			return
		case bpf.Jump:
			walk(pc + 1 + int(insn.Skip))
			return
		default:
			walk(pc + 1)
		}
	}
	walk(0)
	if walkErr != nil {
		return walkErr
	}

	hasRet := false
	for pc, ok := range reachable {
		if !ok {
			continue
		}
		switch insns[pc].(type) {
		case bpf.RetConstant, bpf.RetA:
			hasRet = true
		case bpf.JumpIf, bpf.Jump:
			// Terminal control-flow instructions; targets already walked.
		default:
			if pc+1 >= len(insns) {
				return fmt.Errorf("seccomp filter falls off end at insn %d", pc)
			}
		}
	}
	if !hasRet {
		return errors.New("seccomp filter has no reachable return")
	}
	return nil
}

func assembleSeccompFilter(policy seccompArchPolicy) ([]bpf.RawInstruction, error) {
	insns, err := buildAFUNIXBlockFilter(policy)
	if err != nil {
		return nil, err
	}
	raw, err := bpf.Assemble(insns)
	if err != nil {
		return nil, fmt.Errorf("assemble AF_UNIX seccomp filter: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("assembled AF_UNIX seccomp filter is empty")
	}
	return raw, nil
}

func serializeSeccompFilterLE(raw []bpf.RawInstruction) []byte {
	buf := make([]byte, len(raw)*8)
	for i, insn := range raw {
		off := i * 8
		binary.LittleEndian.PutUint16(buf[off:off+2], insn.Op)
		buf[off+2] = insn.Jt
		buf[off+3] = insn.Jf
		binary.LittleEndian.PutUint32(buf[off+4:off+8], insn.K)
	}
	return buf
}

// Overridable assemble/seal hooks so memfd setup fail-closed paths are testable.
var (
	linuxAssembleSeccompFilter = assembleSeccompFilter
	linuxWriteAndSealSeccomp   = writeAndSealSeccompMemfd
)

func openSeccompFilterMemfd() (*os.File, error) {
	policy, err := linuxNativeSeccompPolicy()
	if err != nil {
		return nil, err
	}
	raw, err := linuxAssembleSeccompFilter(policy)
	if err != nil {
		return nil, err
	}
	payload := serializeSeccompFilterLE(raw)
	fd, err := unix.MemfdCreate("trpc-agent-seccomp", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("create seccomp memfd: %w", err)
	}
	f := os.NewFile(uintptr(fd), "trpc-agent-seccomp")
	if err := linuxWriteAndSealSeccomp(f, payload); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// writeAndSealSeccompMemfd writes the classic BPF blob, rewinds, and applies
// write/grow/shrink/seal seals so bubblewrap cannot observe a mutable filter.
func writeAndSealSeccompMemfd(f *os.File, payload []byte) error {
	if _, err := f.Write(payload); err != nil {
		return fmt.Errorf("write seccomp memfd: %w", err)
	}
	if written, err := f.Seek(0, io.SeekStart); err != nil || written != 0 {
		if err == nil {
			err = fmt.Errorf("seccomp memfd seek returned %d", written)
		}
		return fmt.Errorf("rewind seccomp memfd: %w", err)
	}
	seals := seccompMemfdSeals
	fd := f.Fd()
	if _, err := linuxFcntlInt(fd, unix.F_ADD_SEALS, seals); err != nil {
		return fmt.Errorf("seal seccomp memfd: %w", err)
	}
	got, err := linuxFcntlInt(fd, unix.F_GET_SEALS, 0)
	if err != nil {
		return fmt.Errorf("read seccomp memfd seals: %w", err)
	}
	if got&seals != seals {
		return fmt.Errorf("seccomp memfd seals = %#x, want %#x", got, seals)
	}
	runtime.KeepAlive(f)
	return nil
}

// seccompMemfdSeals / linuxFcntlInt are overridable for seal verification tests.
var (
	seccompMemfdSeals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	linuxFcntlInt     = unix.FcntlInt
)

func parseKernelRelease(release string) (major, minor int, err error) {
	release = strings.TrimSpace(release)
	if release == "" {
		return 0, 0, errors.New("empty kernel release")
	}
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("malformed kernel release %q", release)
	}
	major, err = strconv.Atoi(leadingDigits(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse kernel major from %q: %w", release, err)
	}
	minor, err = strconv.Atoi(leadingDigits(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("parse kernel minor from %q: %w", release, err)
	}
	return major, minor, nil
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func kernelSupportsRestrictedSeccomp(release string) error {
	major, minor, err := parseKernelRelease(release)
	if err != nil {
		return err
	}
	if major > minRestrictedKernelMajor ||
		(major == minRestrictedKernelMajor && minor >= minRestrictedKernelMinor) {
		return nil
	}
	return fmt.Errorf(
		"linux %d.%d is below required %d.%d for restricted AF_UNIX seccomp",
		major,
		minor,
		minRestrictedKernelMajor,
		minRestrictedKernelMinor,
	)
}

func currentKernelRelease() (string, error) {
	var uts unix.Utsname
	if err := linuxUname(&uts); err != nil {
		return "", err
	}
	return unix.ByteSliceToString(uts.Release[:]), nil
}

var linuxUname = unix.Uname
