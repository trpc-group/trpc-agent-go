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
	auditArchAARCH64 = 0xc00000b7

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
	sysSocketARM64          = 198
	sysSocketpairARM64      = 199
	sysIOUringSetupARM64    = 425
	sysIOUringEnterARM64    = 426
	sysIOUringRegisterARM64 = 427

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
}

var (
	seccompPolicyAMD64 = seccompArchPolicy{
		name:            "amd64",
		auditArch:       auditArchX86_64,
		rejectX32:       true,
		socket:          sysSocketAMD64,
		socketpair:      sysSocketpairAMD64,
		ioUringSetup:    sysIOUringSetupAMD64,
		ioUringEnter:    sysIOUringEnterAMD64,
		ioUringRegister: sysIOUringRegisterAMD64,
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

func buildAFUNIXBlockFilter(policy seccompArchPolicy) ([]bpf.Instruction, error) {
	if policy.auditArch == 0 || policy.socket == 0 || policy.socketpair == 0 {
		return nil, errors.New("invalid seccomp architecture policy")
	}

	// Program layout (classic BPF, little-endian seccomp_data):
	//  0: ld arch
	//  1: jeq expectedArch -> +1 else +0
	//  2: kill
	//  [optional x32 reject]
	//  n: ld nr
	//  n+1: jeq socket -> socket_domain_check else continue
	//  n+2: jeq socketpair -> socketpair_domain_check else continue
	//  ... io_uring denials ...
	//  allow
	//  socket_domain_check: ld arg0_low32; jeq AF_UNIX -> deny else allow
	//  socketpair_domain_check: ld arg0_low32; non-AF_UNIX -> allow
	//  socketpair_type_check: ld arg1_low32; mask flags; SOCK_DGRAM -> deny
	//  deny: errno(EPERM)
	insns := []bpf.Instruction{
		bpf.LoadAbsolute{Off: seccompDataOffArch, Size: 4},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       policy.auditArch,
			SkipTrue:  1,
			SkipFalse: 0,
		},
		bpf.RetConstant{Val: seccompRetKillProcess},
	}
	if policy.rejectX32 {
		insns = append(insns,
			bpf.LoadAbsolute{Off: seccompDataOffNR, Size: 4},
			bpf.JumpIf{
				Cond:      bpf.JumpBitsSet,
				Val:       x32SyscallBit,
				SkipTrue:  0,
				SkipFalse: 1,
			},
			bpf.RetConstant{Val: seccompRetKillProcess},
		)
	}

	// From jeq SOCKET the next instructions are:
	//  0 jeq SOCKETPAIR, 1 jeq SETUP, 2 jeq ENTER, 3 jeq REGISTER,
	//  4 ret ALLOW, 5 ret EPERM, 6 ld socket arg0, ...
	// Socket and socketpair jump to their forward-only argument checks.
	// io_uring matches jump to the shared ret EPERM. Other syscalls allow.
	insns = append(insns,
		bpf.LoadAbsolute{Off: seccompDataOffNR, Size: 4},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       policy.socket,
			SkipTrue:  6,
			SkipFalse: 0,
		},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       policy.socketpair,
			SkipTrue:  9,
			SkipFalse: 0,
		},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       policy.ioUringSetup,
			SkipTrue:  3,
			SkipFalse: 0,
		},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       policy.ioUringEnter,
			SkipTrue:  2,
			SkipFalse: 0,
		},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       policy.ioUringRegister,
			SkipTrue:  1,
			SkipFalse: 0,
		},
		bpf.RetConstant{Val: seccompRetAllow},
		bpf.RetConstant{Val: seccompRetEPERM},
		bpf.LoadAbsolute{Off: seccompDataOffArg0, Size: 4},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       afUNIX,
			SkipTrue:  0,
			SkipFalse: 1,
		},
		bpf.RetConstant{Val: seccompRetEPERM},
		bpf.RetConstant{Val: seccompRetAllow},
		bpf.LoadAbsolute{Off: seccompDataOffArg0, Size: 4},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       afUNIX,
			SkipTrue:  1,
			SkipFalse: 0,
		},
		bpf.RetConstant{Val: seccompRetAllow},
		bpf.LoadAbsolute{Off: seccompDataOffArg1, Size: 4},
		bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: sockTypeMask},
		bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       sockDGRAM,
			SkipTrue:  0,
			SkipFalse: 1,
		},
		bpf.RetConstant{Val: seccompRetEPERM},
		bpf.RetConstant{Val: seccompRetAllow},
	)
	return insns, nil
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

func openSeccompFilterMemfd() (*os.File, error) {
	policy, err := nativeSeccompPolicy()
	if err != nil {
		return nil, err
	}
	raw, err := assembleSeccompFilter(policy)
	if err != nil {
		return nil, err
	}
	payload := serializeSeccompFilterLE(raw)
	fd, err := unix.MemfdCreate("trpc-agent-seccomp", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("create seccomp memfd: %w", err)
	}
	f := os.NewFile(uintptr(fd), "trpc-agent-seccomp")
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write seccomp memfd: %w", err)
	}
	if written, err := f.Seek(0, io.SeekStart); err != nil || written != 0 {
		_ = f.Close()
		if err == nil {
			err = fmt.Errorf("seccomp memfd seek returned %d", written)
		}
		return nil, fmt.Errorf("rewind seccomp memfd: %w", err)
	}
	seals := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, seals); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("seal seccomp memfd: %w", err)
	}
	got, err := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("read seccomp memfd seals: %w", err)
	}
	if got&seals != seals {
		_ = f.Close()
		return nil, fmt.Errorf("seccomp memfd seals = %#x, want %#x", got, seals)
	}
	return f, nil
}

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
	if err := unix.Uname(&uts); err != nil {
		return "", err
	}
	return unix.ByteSliceToString(uts.Release[:]), nil
}

// evaluateSeccompFilterLE evaluates a little-endian seccomp filter against a
// native little-endian seccomp_data image. It exists for tests and must not be
// confused with bpf.NewVM, which reads absolute loads as network endian.
func evaluateSeccompFilterLE(raw []bpf.RawInstruction, data []byte) (uint32, error) {
	if len(data) < seccompDataOffArg0+8 {
		return 0, errors.New("seccomp_data too short")
	}
	regA := uint32(0)
	regX := uint32(0)
	for pc := 0; pc < len(raw); pc++ {
		insn := raw[pc]
		opClass := insn.Op & 0x07
		switch opClass {
		case 0x00: // BPF_LD
			size := insn.Op & 0x18
			mode := insn.Op & 0xe0
			if mode != 0x20 { // BPF_ABS
				return 0, fmt.Errorf("unsupported LD mode %#x at %d", mode, pc)
			}
			var width int
			switch size {
			case 0x00:
				width = 4
			case 0x08:
				width = 2
			case 0x10:
				width = 1
			default:
				return 0, fmt.Errorf("unsupported LD size %#x at %d", size, pc)
			}
			off := int(insn.K)
			if off < 0 || off+width > len(data) {
				return 0, fmt.Errorf("LD out of bounds at %d", pc)
			}
			switch width {
			case 4:
				regA = binary.LittleEndian.Uint32(data[off : off+4])
			case 2:
				regA = uint32(binary.LittleEndian.Uint16(data[off : off+2]))
			case 1:
				regA = uint32(data[off])
			}
		case 0x05: // BPF_JMP
			cond := insn.Op & 0xf0
			src := insn.Op & 0x08
			k := insn.K
			if src != 0 {
				k = regX
			}
			match := false
			switch cond {
			case 0x10: // JEQ
				match = regA == k
			case 0x40: // JSET
				match = regA&k != 0
			case 0x00: // JA
				pc += int(insn.K)
				continue
			default:
				return 0, fmt.Errorf("unsupported JMP cond %#x at %d", cond, pc)
			}
			if match {
				pc += int(insn.Jt)
			} else {
				pc += int(insn.Jf)
			}
		case 0x04: // BPF_ALU
			src := insn.Op & 0x08
			k := insn.K
			if src != 0 {
				k = regX
			}
			switch op := insn.Op & 0xf0; op {
			case 0x50: // BPF_AND
				regA &= k
			default:
				return 0, fmt.Errorf("unsupported ALU op %#x at %d", op, pc)
			}
		case 0x06: // BPF_RET
			return insn.K, nil
		default:
			return 0, fmt.Errorf("unsupported op class %#x at %d", opClass, pc)
		}
	}
	return 0, errors.New("filter fell off the end")
}

func packSeccompDataLE(nr int32, arch uint32, args ...uint64) []byte {
	buf := make([]byte, 64)
	binary.LittleEndian.PutUint32(buf[seccompDataOffNR:], uint32(nr))
	binary.LittleEndian.PutUint32(buf[seccompDataOffArch:], arch)
	for i, arg := range args {
		off := seccompDataOffArg0 + i*8
		if off+8 > len(buf) {
			break
		}
		binary.LittleEndian.PutUint64(buf[off:], arg)
	}
	return buf
}
