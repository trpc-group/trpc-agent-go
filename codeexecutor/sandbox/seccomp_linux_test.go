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
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestAssembleSeccompFilterSemantics(t *testing.T) {
	t.Parallel()
	for _, policy := range []seccompArchPolicy{seccompPolicyAMD64, seccompPolicyARM64} {
		policy := policy
		t.Run(policy.name, func(t *testing.T) {
			t.Parallel()
			raw, err := assembleSeccompFilter(policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) == 0 {
				t.Fatal("empty filter")
			}

			assertFilter := func(name string, nr int32, arch uint32, arg0 uint64, want uint32, extraArgs ...uint64) {
				t.Helper()
				args := append([]uint64{arg0}, extraArgs...)
				got, err := evaluateSeccompFilterLE(raw, packSeccompDataLE(nr, arch, args...))
				if err != nil {
					t.Fatalf("%s: evaluate: %v", name, err)
				}
				if got != want {
					t.Fatalf("%s: got %#x, want %#x", name, got, want)
				}
			}

			assertFilter("allow AF_INET socket", int32(policy.socket), policy.auditArch, 2, seccompRetAllow)
			assertFilter("deny AF_UNIX socket", int32(policy.socket), policy.auditArch, afUNIX, seccompRetEPERM)
			assertFilter("deny AF_UNIX with high bits", int32(policy.socket), policy.auditArch, afUNIX|uint64(0xffffffff)<<32, seccompRetEPERM)
			assertFilter(
				"allow AF_UNIX stream socketpair",
				int32(policy.socketpair),
				policy.auditArch,
				afUNIX,
				seccompRetAllow,
				uint64(unix.SOCK_STREAM),
			)
			assertFilter(
				"deny AF_UNIX datagram socketpair",
				int32(policy.socketpair),
				policy.auditArch,
				afUNIX,
				seccompRetEPERM,
				uint64(unix.SOCK_DGRAM),
			)
			assertFilter(
				"deny flagged AF_UNIX datagram socketpair",
				int32(policy.socketpair),
				policy.auditArch,
				afUNIX,
				seccompRetEPERM,
				uint64(unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK),
			)
			assertFilter(
				"allow non-AF_UNIX datagram socketpair",
				int32(policy.socketpair),
				policy.auditArch,
				uint64(unix.AF_INET),
				seccompRetAllow,
				uint64(unix.SOCK_DGRAM),
			)
			assertFilter("deny io_uring_setup", int32(policy.ioUringSetup), policy.auditArch, 0, seccompRetEPERM)
			assertFilter("deny io_uring_enter", int32(policy.ioUringEnter), policy.auditArch, 0, seccompRetEPERM)
			assertFilter("deny io_uring_register", int32(policy.ioUringRegister), policy.auditArch, 0, seccompRetEPERM)
			assertFilter("allow unrelated syscall", 1, policy.auditArch, 0, seccompRetAllow)
			assertFilter("kill wrong arch", int32(policy.socket), policy.auditArch^0xff, afUNIX, seccompRetKillProcess)

			if policy.rejectX32 {
				assertFilter(
					"kill x32 socket",
					int32(uint32(policy.socket)|x32SyscallBit),
					policy.auditArch,
					afUNIX,
					seccompRetKillProcess,
				)
			}
		})
	}
}

func TestSeccompPolicyForGOARCH(t *testing.T) {
	t.Parallel()
	if _, err := seccompPolicyForGOARCH("amd64"); err != nil {
		t.Fatal(err)
	}
	if _, err := seccompPolicyForGOARCH("arm64"); err != nil {
		t.Fatal(err)
	}
	if _, err := seccompPolicyForGOARCH("386"); err == nil {
		t.Fatal("expected unsupported GOARCH error")
	}
}

func TestParseKernelReleaseAndSupport(t *testing.T) {
	t.Parallel()
	major, minor, err := parseKernelRelease("5.4.241-1-tlinux4-0025.1")
	if err != nil || major != 5 || minor != 4 {
		t.Fatalf("parse = %d.%d err=%v", major, minor, err)
	}
	if err := kernelSupportsRestrictedSeccomp("4.8.0"); err != nil {
		t.Fatalf("4.8 should pass: %v", err)
	}
	if err := kernelSupportsRestrictedSeccomp("5.0"); err != nil {
		t.Fatalf("5.0 should pass: %v", err)
	}
	if err := kernelSupportsRestrictedSeccomp("4.7.10"); err == nil {
		t.Fatal("4.7 should fail")
	}
	if err := kernelSupportsRestrictedSeccomp("3.10.0"); err == nil {
		t.Fatal("3.10 should fail")
	}
	if _, _, err := parseKernelRelease("not-a-version"); err == nil {
		t.Fatal("expected malformed release error")
	}
	if _, _, err := parseKernelRelease(""); err == nil {
		t.Fatal("expected empty release error")
	}
	if _, _, err := parseKernelRelease(" .4"); err == nil {
		t.Fatal("expected missing major digits error")
	}
	if _, _, err := parseKernelRelease("4."); err == nil {
		t.Fatal("expected missing minor digits error")
	}
	if err := kernelSupportsRestrictedSeccomp(""); err == nil {
		t.Fatal("expected kernel support parse failure")
	}
}

func TestBuildAFUNIXBlockFilterRejectsInvalidPolicy(t *testing.T) {
	t.Parallel()
	if _, err := buildAFUNIXBlockFilter(seccompArchPolicy{}); err == nil {
		t.Fatal("expected invalid policy error")
	}
	if _, err := assembleSeccompFilter(seccompArchPolicy{auditArch: 1}); err == nil {
		t.Fatal("expected assemble to fail for incomplete policy")
	}
}

func TestEvaluateSeccompFilterLEErrorPaths(t *testing.T) {
	t.Parallel()
	if _, err := evaluateSeccompFilterLE(nil, make([]byte, 8)); err == nil {
		t.Fatal("expected short seccomp_data error")
	}

	data := packSeccompDataLE(1, auditArchX86_64, 0)
	if _, err := evaluateSeccompFilterLE([]bpf.RawInstruction{{Op: 0x00}}, data); err == nil {
		t.Fatal("expected unsupported LD mode error")
	}
	if _, err := evaluateSeccompFilterLE([]bpf.RawInstruction{{Op: 0x20 | 0x18}}, data); err == nil {
		t.Fatal("expected unsupported LD size error")
	}
	if _, err := evaluateSeccompFilterLE([]bpf.RawInstruction{{Op: 0x20, K: 60}}, data); err == nil {
		t.Fatal("expected LD out-of-bounds error")
	}
	if _, err := evaluateSeccompFilterLE([]bpf.RawInstruction{{Op: 0x05 | 0x20}}, data); err == nil {
		t.Fatal("expected unsupported JMP condition error")
	}
	if _, err := evaluateSeccompFilterLE([]bpf.RawInstruction{{Op: 0x04 | 0x00}}, data); err == nil {
		t.Fatal("expected unsupported ALU op error")
	}
	if _, err := evaluateSeccompFilterLE([]bpf.RawInstruction{{Op: 0x02}}, data); err == nil {
		t.Fatal("expected unsupported op class error")
	}
	if _, err := evaluateSeccompFilterLE([]bpf.RawInstruction{{Op: 0x05}}, data); err == nil {
		t.Fatal("expected filter fall-off error for bare JA")
	}

	// Cover LD half/byte and ALU with X source, plus JA that lands on RET.
	raw := []bpf.RawInstruction{
		{Op: 0x28, K: 0},               // LDH ABS
		{Op: 0x30, K: 0},               // LDB ABS
		{Op: 0x04 | 0x50 | 0x08, K: 0}, // AND X
		{Op: 0x05, K: 0},               // JA +0
		{Op: 0x06, K: seccompRetAllow},
	}
	got, err := evaluateSeccompFilterLE(raw, data)
	if err != nil {
		t.Fatal(err)
	}
	if got != seccompRetAllow {
		t.Fatalf("got %#x, want allow", got)
	}
}

func TestPackSeccompDataLETruncatesExtraArgs(t *testing.T) {
	t.Parallel()
	args := make([]uint64, 8)
	for i := range args {
		args[i] = uint64(i + 1)
	}
	buf := packSeccompDataLE(41, auditArchX86_64, args...)
	if len(buf) != 64 {
		t.Fatalf("len=%d", len(buf))
	}
	// seccomp_data has room for args 0..5 starting at offset 16.
	if got := binary.LittleEndian.Uint64(buf[16:]); got != 1 {
		t.Fatalf("arg0=%d", got)
	}
	if got := binary.LittleEndian.Uint64(buf[16+5*8:]); got != 6 {
		t.Fatalf("arg5=%d", got)
	}
}

func TestCurrentKernelReleaseReadable(t *testing.T) {
	t.Parallel()
	release, err := currentKernelRelease()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(release) == "" {
		t.Fatal("empty kernel release")
	}
}

func TestOpenSeccompFilterMemfdSealed(t *testing.T) {
	t.Parallel()
	if _, err := nativeSeccompPolicy(); err != nil {
		t.Skip(err)
	}
	f, err := openSeccompFilterMemfd()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	seals, err := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if seals&want != want {
		t.Fatalf("seals=%#x want %#x", seals, want)
	}
	if _, err := f.Write([]byte("x")); err == nil {
		t.Fatal("expected write to sealed memfd to fail")
	}
}

func TestSerializeSeccompFilterLELayout(t *testing.T) {
	t.Parallel()
	raw, err := assembleSeccompFilter(seccompPolicyAMD64)
	if err != nil {
		t.Fatal(err)
	}
	buf := serializeSeccompFilterLE(raw)
	if len(buf) != len(raw)*8 {
		t.Fatalf("len=%d want %d", len(buf), len(raw)*8)
	}
	// First instruction is ld [4]; classic BPF code for BPF_LD|BPF_W|BPF_ABS is 0x20.
	if op := uint16(buf[0]) | uint16(buf[1])<<8; op != 0x20 {
		t.Fatalf("first opcode = %#x, want 0x20", op)
	}
	if k := uint32(buf[4]) | uint32(buf[5])<<8 | uint32(buf[6])<<16 | uint32(buf[7])<<24; k != seccompDataOffArch {
		t.Fatalf("first k = %d, want %d", k, seccompDataOffArch)
	}
}

const (
	linuxSeccompHelperEnv  = "TRPC_LINUX_SECCOMP_HELPER"
	linuxSeccompHelperMode = "TRPC_LINUX_SECCOMP_MODE"
	linuxSeccompHelperPath = "TRPC_LINUX_SECCOMP_PATH"
)

func TestLinuxSeccompHelper(t *testing.T) {
	mode := os.Getenv(linuxSeccompHelperEnv)
	if mode == "" {
		return
	}
	switch os.Getenv(linuxSeccompHelperMode) {
	case "dial-unix":
		conn, err := net.DialTimeout("unix", os.Getenv(linuxSeccompHelperPath), time.Second)
		if err != nil {
			fmt.Fprintf(os.Stdout, "DIAL_ERR %v\n", err)
			os.Exit(0)
		}
		_ = conn.Close()
		fmt.Fprintln(os.Stdout, "DIAL_OK")
		os.Exit(0)
	case "listen-pathname":
		path := os.Getenv(linuxSeccompHelperPath)
		ln, err := net.Listen("unix", path)
		if err != nil {
			fmt.Fprintf(os.Stdout, "LISTEN_ERR %v\n", err)
			os.Exit(0)
		}
		_ = ln.Close()
		fmt.Fprintln(os.Stdout, "LISTEN_OK")
		os.Exit(0)
	case "listen-abstract":
		ln, err := net.Listen("unix", "@trpc-agent-seccomp-test")
		if err != nil {
			fmt.Fprintf(os.Stdout, "ABSTRACT_ERR %v\n", err)
			os.Exit(0)
		}
		_ = ln.Close()
		fmt.Fprintln(os.Stdout, "ABSTRACT_OK")
		os.Exit(0)
	case "socketpair":
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			fmt.Fprintf(os.Stdout, "SOCKETPAIR_ERR %v\n", err)
			os.Exit(2)
		}
		_ = unix.Close(fds[0])
		_ = unix.Close(fds[1])
		fmt.Fprintln(os.Stdout, "SOCKETPAIR_OK")
		os.Exit(0)
	case "socketpair-dgram-connect":
		fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			fmt.Fprintf(os.Stdout, "SOCKETPAIR_DGRAM_ERR %v\n", err)
			os.Exit(2)
		}
		defer unix.Close(fds[0])
		defer unix.Close(fds[1])
		if err := unix.Connect(fds[0], &unix.SockaddrUnix{Name: os.Getenv(linuxSeccompHelperPath)}); err != nil {
			fmt.Fprintf(os.Stdout, "SOCKETPAIR_CONNECT_ERR %v\n", err)
			os.Exit(0)
		}
		if _, err := unix.Write(fds[0], []byte("socketpair-bypass")); err != nil {
			fmt.Fprintf(os.Stdout, "SOCKETPAIR_WRITE_ERR %v\n", err)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stdout, "SOCKETPAIR_CONNECT_OK")
		os.Exit(0)
	case "inet-socket":
		fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
		if err != nil {
			fmt.Fprintf(os.Stdout, "INET_ERR %v\n", err)
			os.Exit(2)
		}
		_ = unix.Close(fd)
		fmt.Fprintln(os.Stdout, "INET_OK")
		os.Exit(0)
	case "io-uring":
		for _, nr := range []uintptr{
			uintptr(unix.SYS_IO_URING_SETUP),
			uintptr(unix.SYS_IO_URING_ENTER),
			uintptr(unix.SYS_IO_URING_REGISTER),
		} {
			_, _, errno := unix.Syscall(nr, 0, 0, 0)
			if errno != unix.EPERM {
				fmt.Fprintf(os.Stdout, "IO_URING_UNEXPECTED %d %v\n", nr, errno)
				os.Exit(2)
			}
		}
		fmt.Fprintln(os.Stdout, "IO_URING_EPERM")
		os.Exit(0)
	case "check-extra-fds":
		var st unix.Stat_t
		if err := unix.Fstat(3, &st); err == nil {
			fmt.Fprintln(os.Stdout, "FD3_PRESENT")
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "FD3_ABSENT")
		var nullStat unix.Stat_t
		if err := unix.Fstat(4, &st); err == nil &&
			unix.Stat("/dev/null", &nullStat) == nil &&
			st.Mode&unix.S_IFMT == unix.S_IFCHR &&
			st.Rdev == nullStat.Rdev {
			fmt.Fprintln(os.Stdout, "FD4_NULL_PRESENT")
			os.Exit(2)
		}
		fmt.Fprintln(os.Stdout, "FD4_NULL_ABSENT")
		os.Exit(0)
	case "fork-exec-dial":
		cmd := exec.Command(os.Args[0], "-test.run=TestLinuxSeccompHelper")
		cmd.Env = append(os.Environ(),
			linuxSeccompHelperEnv+"=1",
			linuxSeccompHelperMode+"=dial-unix",
			linuxSeccompHelperPath+"="+os.Getenv(linuxSeccompHelperPath),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stdout, "FORK_ERR %v %s\n", err, out)
			os.Exit(2)
		}
		fmt.Print(string(out))
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", os.Getenv(linuxSeccompHelperMode))
		os.Exit(2)
	}
}

func requireLinuxBwrap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap not available")
	}
}

func newLinuxSeccompRuntime(t *testing.T, profile PermissionProfile) (*Runtime, codeexecutor.Workspace) {
	t.Helper()
	requireLinuxBwrap(t)
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
	)
	if _, _, err := rt.linuxPreflight(); err != nil {
		t.Skipf("bubblewrap preflight unavailable: %v", err)
	}
	if profile.network.Mode == NetworkRestricted {
		if err := rt.linuxRestrictedPreflight(rt.bwrapPath, rt.bwrapMountProc); err != nil {
			t.Skipf("restricted seccomp preflight unavailable: %v", err)
		}
	}
	ws, err := rt.CreateWorkspace(context.Background(), t.Name(), codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return rt, ws
}

func runLinuxSeccompHelper(
	t *testing.T,
	rt *Runtime,
	ws codeexecutor.Workspace,
	mode string,
	path string,
	opts ...func(context.Context) context.Context,
) codeexecutor.RunResult {
	t.Helper()
	ctx := context.Background()
	for _, opt := range opts {
		ctx = opt(ctx)
	}
	env := map[string]string{
		linuxSeccompHelperEnv:  "1",
		linuxSeccompHelperMode: mode,
	}
	if path != "" {
		env[linuxSeccompHelperPath] = path
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:     os.Args[0],
		Args:    []string{"-test.run=^TestLinuxSeccompHelper$"},
		Env:     env,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("run helper %s: %v stderr=%s", mode, err, res.Stderr)
	}
	return res
}

func TestLinuxRestrictedBlocksHostUnixDial(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "host.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
			accepted <- struct{}{}
		}
	}()

	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	res := runLinuxSeccompHelper(t, rt, ws, "dial-unix", socketPath)
	if !strings.Contains(res.Stdout, "DIAL_ERR") || strings.Contains(res.Stdout, "DIAL_OK") {
		t.Fatalf("restricted dial = %#v, want EPERM-style failure", res)
	}
	if !strings.Contains(strings.ToLower(res.Stdout), "permission denied") &&
		!strings.Contains(res.Stdout, "operation not permitted") {
		t.Fatalf("restricted dial error = %q, want permission denied", res.Stdout)
	}
	select {
	case <-accepted:
		t.Fatal("host listener unexpectedly accepted a connection")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLinuxRestrictedBlocksLocalAndAbstractUnix(t *testing.T) {
	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	localPath := filepath.Join(ws.Path, "work", "local.sock")
	res := runLinuxSeccompHelper(t, rt, ws, "listen-pathname", localPath)
	if !strings.Contains(res.Stdout, "LISTEN_ERR") {
		t.Fatalf("pathname listen = %#v, want denial", res)
	}
	res = runLinuxSeccompHelper(t, rt, ws, "listen-abstract", "")
	if !strings.Contains(res.Stdout, "ABSTRACT_ERR") {
		t.Fatalf("abstract listen = %#v, want denial", res)
	}
}

func TestLinuxRestrictedAllowsSocketpairAndInet(t *testing.T) {
	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	res := runLinuxSeccompHelper(t, rt, ws, "socketpair", "")
	if !strings.Contains(res.Stdout, "SOCKETPAIR_OK") {
		t.Fatalf("socketpair = %#v, want success", res)
	}
	res = runLinuxSeccompHelper(t, rt, ws, "inet-socket", "")
	if !strings.Contains(res.Stdout, "INET_OK") {
		t.Fatalf("inet socket = %#v, want success", res)
	}
}

func TestLinuxRestrictedBlocksDatagramSocketpairReconnect(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "host-dgram.sock")
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan bool, 1)
	go func() {
		_ = listener.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 64)
		n, _, err := listener.ReadFromUnix(buf)
		received <- err == nil && string(buf[:n]) == "socketpair-bypass"
	}()

	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	res := runLinuxSeccompHelper(t, rt, ws, "socketpair-dgram-connect", socketPath)
	if !strings.Contains(res.Stdout, "SOCKETPAIR_DGRAM_ERR") {
		t.Fatalf("datagram socketpair = %#v, want seccomp denial", res)
	}
	if !strings.Contains(strings.ToLower(res.Stdout), "operation not permitted") {
		t.Fatalf("datagram socketpair error = %q, want EPERM", res.Stdout)
	}
	if <-received {
		t.Fatal("host datagram listener received data from restricted sandbox")
	}
}

func TestLinuxRestrictedBlocksIOUring(t *testing.T) {
	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	res := runLinuxSeccompHelper(t, rt, ws, "io-uring", "")
	if !strings.Contains(res.Stdout, "IO_URING_EPERM") {
		t.Fatalf("io_uring = %#v, want EPERM for all three syscalls", res)
	}
}

func TestLinuxNetworkEnabledAllowsHostUnixDial(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "enabled.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_, _ = conn.Write([]byte("hi"))
			_ = conn.Close()
		}
	}()

	profile := WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})
	rt, ws := newLinuxSeccompRuntime(t, profile)
	res := runLinuxSeccompHelper(t, rt, ws, "dial-unix", socketPath)
	if !strings.Contains(res.Stdout, "DIAL_OK") {
		t.Fatalf("enabled dial = %#v, want success", res)
	}
}

func TestLinuxAdditionalPermissionsNetworkEnabledAllowsUnixDial(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "grant.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	res := runLinuxSeccompHelper(t, rt, ws, "dial-unix", socketPath, func(ctx context.Context) context.Context {
		enabled := NetworkEnabled
		return WithAdditionalPermissions(ctx, AdditionalPermissions{
			Network: &NetworkPolicy{Mode: enabled},
		})
	})
	if !strings.Contains(res.Stdout, "DIAL_OK") {
		t.Fatalf("temporary enabled dial = %#v, want success", res)
	}
	res = runLinuxSeccompHelper(t, rt, ws, "dial-unix", socketPath)
	if !strings.Contains(res.Stdout, "DIAL_ERR") {
		t.Fatalf("follow-up restricted dial = %#v, want denial", res)
	}
}

func TestLinuxRestrictedHidesSeccompExtraFDs(t *testing.T) {
	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	res := runLinuxSeccompHelper(t, rt, ws, "check-extra-fds", "")
	if !strings.Contains(res.Stdout, "FD3_ABSENT") {
		t.Fatalf("extra fds = %#v, want seccomp FD 3 absent after bwrap consumes it", res)
	}
}

func TestLinuxRestrictedForkExecKeepsFilter(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "fork.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	res := runLinuxSeccompHelper(t, rt, ws, "fork-exec-dial", socketPath)
	if !strings.Contains(res.Stdout, "DIAL_ERR") {
		t.Fatalf("fork/exec dial = %#v, want denial in child", res)
	}
}

func TestLinuxRestrictedStartProcessBlocksUnixDial(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "process.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	rt, ws := newLinuxSeccompRuntime(t, WorkspaceWriteProfile())
	proc, err := rt.StartProcess(context.Background(), ws, ProcessSpec{
		Cmd:  os.Args[0],
		Args: []string{"-test.run=^TestLinuxSeccompHelper$"},
		Env: map[string]string{
			linuxSeccompHelperEnv:  "1",
			linuxSeccompHelperMode: "dial-unix",
			linuxSeccompHelperPath: socketPath,
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(proc.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(string(out), "DIAL_ERR") {
		t.Fatalf("StartProcess dial = %q, want denial", out)
	}
}

func TestLinuxRestrictedSeccompArgsAndEnabledSkip(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "seccomp-args", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := rt.linuxSandboxArgs(
		WorkspaceWriteProfile(),
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArgPair(restricted, "--seccomp", "3") || !hasArg(restricted, "--unshare-net") {
		t.Fatalf("restricted args = %#v", restricted)
	}

	enabled, err := rt.linuxSandboxArgs(
		WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled}),
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasArg(enabled, "--seccomp") || hasArg(enabled, "--unshare-net") {
		t.Fatalf("enabled args = %#v, want no seccomp/netns", enabled)
	}
}

func TestLinuxRestrictedPreflightIndependentFromEnabled(t *testing.T) {
	requireLinuxBwrap(t)
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled})),
	)
	bwrap, mountProc, err := rt.linuxPreflight()
	if err != nil {
		t.Skipf("bwrap unavailable: %v", err)
	}
	ws, err := rt.CreateWorkspace(context.Background(), "preflight-enabled-first", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, cleanup, err := rt.osSandboxCommand(
		context.Background(),
		WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{Mode: NetworkEnabled}),
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		cleanup()
	}
	if rt.restrictedPreflightErr != nil {
		t.Fatalf("enabled run set restricted preflight err unexpectedly: %v", rt.restrictedPreflightErr)
	}

	if err := rt.linuxRestrictedPreflight(bwrap, mountProc); err != nil {
		t.Fatalf("restricted preflight after enabled run: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rt.linuxRestrictedPreflight(bwrap, mountProc)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent restricted preflight: %v", err)
		}
	}
}

func TestLinuxRestrictedDenyReadUsesFD4WithSeccomp(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "seccomp-deny-fd", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/missing.txt")
	setup, err := rt.linuxSandboxSetup(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !setup.needsSeccompFD || !setup.needsDenyReadDataFD {
		t.Fatalf("setup flags = %+v", setup)
	}
	if setup.denyReadBindDataFD != "4" {
		t.Fatalf("deny-read fd = %q, want 4", setup.denyReadBindDataFD)
	}
	if !hasArgPair(setup.args, "--seccomp", "3") {
		t.Fatalf("args = %#v, missing seccomp fd 3", setup.args)
	}
	missing := filepath.Join(ws.Path, "work", "missing.txt")
	if !hasArgSequence(setup.args, "--perms", "000", "--ro-bind-data", "4", missing) {
		t.Fatalf("args = %#v, missing deny-read on fd 4", setup.args)
	}
}

func TestLinuxRestrictedDenyReadFDsConsumedAndCleanedAfterWait(t *testing.T) {
	profile := WorkspaceWriteProfile().WithNoAccessPaths("work/missing.txt")
	rt, ws := newLinuxSeccompRuntime(t, profile)
	proc, err := rt.StartProcess(context.Background(), ws, ProcessSpec{
		Cmd:  os.Args[0],
		Args: []string{"-test.run=^TestLinuxSeccompHelper$"},
		Env: map[string]string{
			linuxSeccompHelperEnv:  "1",
			linuxSeccompHelperMode: "check-extra-fds",
		},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(proc.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatal(err)
	}
	stdout := string(out)
	if !strings.Contains(stdout, "FD3_ABSENT") || !strings.Contains(stdout, "FD4_NULL_ABSENT") {
		t.Fatalf("combined extra fds = %q, want seccomp and deny-read fds consumed", stdout)
	}
	if _, err := os.Stat(filepath.Join(ws.Path, "work", "missing.txt")); !os.IsNotExist(err) {
		t.Fatalf("synthetic deny-read target was not cleaned after Wait: %v", err)
	}
}
