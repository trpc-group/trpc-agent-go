//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsafety

import (
	"regexp"
	"strconv"
	"strings"
)

// rule is a single safety check applied during scanning.
type rule struct {
	id            string
	category      string
	riskLevel     RiskLevel
	check         func(ctx *scanContext) *RuleFinding
	recommendText string
}

// scanContext carries the raw command and parsed metadata available
// to every rule.
type scanContext struct {
	command     string
	commandArgs []string
	workDir     string
	envKeys     []string
	backend     string
	timeoutSec  int
	outputBytes int64
	parsed      *parsedCommand
	policy      *SafetyPolicy
}

// parsedCommand holds the shellsafe-parsed form if available.
// nil when shellsafe rejected the command.
type parsedCommand struct {
	segments [][]string // [[argv...], [argv...]] per pipeline segment
}

// --- Rule registry ---

var allRules = []rule{
	//
	// R1: dangerous commands
	//
	{
		id: "R1-DANGEROUS-DELETE", category: CatDangerousCmd,
		riskLevel:     RiskCritical,
		check:         checkDangerousDelete,
		recommendText: "Replace destructive deletion with safer alternatives (e.g. move to trash, use versioned storage).",
	},
	{
		id: "R1-DANGEROUS-OVERWRITE", category: CatDangerousCmd,
		riskLevel:     RiskCritical,
		check:         checkDangerousOverwrite,
		recommendText: "Avoid overwriting system files or block devices.",
	},
	{
		id: "R1-SENSITIVE-PATH", category: CatDangerousCmd,
		riskLevel:     RiskHigh,
		check:         checkSensitivePath,
		recommendText: "Do not access credential files or system secrets. Use a dedicated secrets service.",
	},
	{
		id: "R1-DENIED-COMMAND", category: CatDangerousCmd,
		riskLevel:     RiskHigh,
		check:         checkDeniedCommand,
		recommendText: "This command is on the global deny list. Use an allowed alternative or request an exception.",
	},

	//
	// R2: network
	//
	{
		id: "R2-BLOCKED-NETWORK-TOOL", category: CatNetwork,
		riskLevel:     RiskHigh,
		check:         checkBlockedNetworkTool,
		recommendText: "Use an approved network tool or add the domain to the allowlist.",
	},
	{
		id: "R2-NON-WHITELIST-DOMAIN", category: CatNetwork,
		riskLevel:     RiskHigh,
		check:         checkNonWhitelistDomain,
		recommendText: "The target domain is not in the network whitelist. Add it to allowed_domains or use an approved endpoint.",
	},

	//
	// R3: shell bypass (supplement to shellsafe)
	//
	{
		id: "R3-SHELL-REEXEC", category: CatShellBypass,
		riskLevel:     RiskCritical,
		check:         checkShellReexec,
		recommendText: "Shell re-execution primitives can run arbitrary code. Use a direct executable.",
	},
	{
		id: "R3-SHELL-WRAPPER", category: CatShellBypass,
		riskLevel:     RiskHigh,
		check:         checkShellWrapper,
		recommendText: "Command wrappers can bypass command-name checks. Use a direct executable or provide extra justification.",
	},
	{
		id: "R3-BASE64-BYPASS", category: CatShellBypass,
		riskLevel:     RiskHigh,
		check:         checkBase64Bypass,
		recommendText: "Base64-encoded payloads can hide malicious commands. Decode and review before allowing.",
	},
	{
		id: "R3-HEX-BYPASS", category: CatShellBypass,
		riskLevel:     RiskMedium,
		check:         checkHexBypass,
		recommendText: "Hex-encoded payloads can hide malicious commands. Decode and review before allowing.",
	},

	//
	// R4: host execution risks
	//
	{
		id: "R4-HOST-PRIVILEGE-ESCALATION", category: CatHostRisk,
		riskLevel:     RiskCritical,
		check:         checkPrivilegeEscalation,
		recommendText: "Privilege escalation commands should be avoided in automated agent execution.",
	},
	{
		id: "R4-HOST-BACKGROUND-PROCESS", category: CatHostRisk,
		riskLevel:     RiskHigh,
		check:         checkBackgroundProcess,
		recommendText: "Background processes in hostexec can lead to process leaks. Use workspaceexec with proper lifecycle management.",
	},
	{
		id: "R4-HOST-PTY-LONG-SESSION", category: CatHostRisk,
		riskLevel:     RiskMedium,
		check:         checkPTYLongSession,
		recommendText: "PTY long sessions in hostexec risk process residue. Prefer workspaceexec for interactive commands.",
	},

	//
	// R5: dependency / env changes
	//
	{
		id: "R5-DEPENDENCY-INSTALL", category: CatInstall,
		riskLevel:     RiskHigh,
		check:         checkDependencyInstall,
		recommendText: "Package installation should be pre-approved. Add the package to the allowlist or use a pre-built environment.",
	},
	{
		id: "R5-CURL-PIPE-BASH", category: CatInstall,
		riskLevel:     RiskCritical,
		check:         checkCurlPipeBash,
		recommendText: "curl|bash bypasses all safety checks. Download the script, inspect it, then run with explicit approval.",
	},
	{
		id: "R5-ENV-MODIFICATION", category: CatInstall,
		riskLevel:     RiskMedium,
		check:         checkEnvModification,
		recommendText: "Modifying environment variables can affect subsequent tool calls. Use env isolation.",
	},

	//
	// R6: resource abuse
	//
	{
		id: "R6-EXCESSIVE-TIMEOUT", category: CatResource,
		riskLevel:     RiskHigh,
		check:         checkExcessiveTimeout,
		recommendText: "Reduce the timeout to within the configured limit.",
	},
	{
		id: "R6-LONG-SLEEP", category: CatResource,
		riskLevel:     RiskHigh,
		check:         checkLongSleep,
		recommendText: "Long sleep commands waste resources. Reduce or remove.",
	},
	{
		id: "R6-FORK-BOMB", category: CatResource,
		riskLevel:     RiskCritical,
		check:         checkForkBomb,
		recommendText: "Fork bombs can crash the host. This pattern is unconditionally blocked.",
	},
	{
		id: "R6-EXCESSIVE-OUTPUT", category: CatResource,
		riskLevel:     RiskMedium,
		check:         checkExcessiveOutput,
		recommendText: "The command may produce large output. Add output size limits or filter the result.",
	},

	//
	// R7: sensitive output leak
	//
	{
		id: "R7-SENSITIVE-OUTPUT", category: CatSensitive,
		riskLevel:     RiskHigh,
		check:         checkSensitiveOutput,
		recommendText: "Command output may contain secrets. Redact sensitive data before returning to the model.",
	},
}

// --- Pattern definitions ---

var (
	// R1 patterns
	reRmRf        = regexp.MustCompile(`\brm\s+.*-(?:[a-z]*r[a-z]*f|f[a-z]*r)`)
	reMkfs        = regexp.MustCompile(`\bmkfs\.?\w*`)
	reDdOf        = regexp.MustCompile(`\bdd\s+.*of=/(dev|sys|proc)`)
	reRedirectDev = regexp.MustCompile(`[>|]\s*/dev/(sd|hd|nvme|loop|dm-)`)
	reChmod777    = regexp.MustCompile(`\bchmod\s+.*777\b`)
	reShutdown    = regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff|init\s+[06])\b`)
	reKillall     = regexp.MustCompile(`\b(killall|pkill)\b`)

	// Sensitive path patterns
	reSSHPath     = regexp.MustCompile(`(?:^|[\s/])(?:~/\.ssh/|/\.ssh/|/etc/ssh/|/\S+\.ssh/)`)
	reAWSPath     = regexp.MustCompile(`(?:^|[\s/])(?:~/\.aws/|/\.aws/|/root/\.aws/|/home/\S+\.aws/)`)
	reGCloudPath  = regexp.MustCompile(`(?:^|[\s/])(?:~/\.gcloud/|/\.gcloud/)`)
	reEnvFile     = regexp.MustCompile(`\.env\b`)
	rePemFile     = regexp.MustCompile(`\.pem\b`)
	reKeyFile     = regexp.MustCompile(`(?i)id_rsa|id_ed25519|id_ecdsa`)
	reCredentials = regexp.MustCompile(`(?i)credentials?\.(json|yaml|yml|ini|conf|env)`)
	reEtcShadow   = regexp.MustCompile(`/etc/(shadow|passwd|group|gshadow)`)
	reKubeConfig  = regexp.MustCompile(`(?:kubeconfig|kube/config|\.kube/config)`)

	// R2 patterns
	reURL = regexp.MustCompile(`https?://([^/\s"'\x60<>|&;$]+)`)

	// R3 patterns
	reBase64Encode = regexp.MustCompile(`\bbase64\s+(?:-d|--decode)\b`)
	reBase64Pipe   = regexp.MustCompile(`\|\s*base64\s.*\|\s*(?:sh|bash|zsh)`)
	reHexEncode    = regexp.MustCompile(`(?:\\x[0-9a-fA-F]{2}){4,}`)
	reXXDDecode    = regexp.MustCompile(`\bxxd\s+-[rp]\b`)

	// R4 patterns
	reSudo       = regexp.MustCompile(`\b(sudo|su|doas|runuser)\b`)
	reBackground = regexp.MustCompile(`\b(nohup|disown)\b|(?:^|\s)(?:[^&]+&)\s*$`)
	reSystemctl  = regexp.MustCompile(`\b(systemctl|service)\s+(stop|disable|mask)`)

	// R5 patterns
	rePipInstall    = regexp.MustCompile(`\b(?:pip\d*|pip3?)\s+install\b`)
	reNpmInstall    = regexp.MustCompile(`\bnpm\s+(?:i|install)\b`)
	reGoInstall     = regexp.MustCompile(`\bgo\s+install\b`)
	reAptInstall    = regexp.MustCompile(`\bapt(?:-get)?\s+install\b`)
	reCargoInstall  = regexp.MustCompile(`\bcargo\s+install\b`)
	reBrewInstall   = regexp.MustCompile(`\bbrew\s+install\b`)
	reCurlPipeShell = regexp.MustCompile(`\bcurl\b.*\|\s*(?:sh|bash|zsh|dash|fish)\b`)
	reWgetPipeShell = regexp.MustCompile(`\bwget\b.*\|\s*(?:sh|bash|zsh|dash|fish)\b`)
	reExportEnv     = regexp.MustCompile(`\bexport\s+\w+=\S+`)

	// R6 patterns
	reSleep    = regexp.MustCompile(`\bsleep\s+(\d+)`)
	reForkBomb = regexp.MustCompile(`(?i)(?::\(\)\s*\{|fork\s*bomb|:\(\)\s*\{)`)
	reFindRoot = regexp.MustCompile(`\bfind\s+/`)

	// R7 patterns
	reAWSKey         = regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)
	reGitHubToken    = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36}`)
	rePrivateKeyHdr  = regexp.MustCompile(`-----BEGIN\s+(?:RSA|EC|DSA|OPENSSH|PGP)\s+PRIVATE\s+KEY`)
	reJWT            = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	rePasswordAssign = regexp.MustCompile(`(?i)password\s*[=:]\s*\S+`)
	reAPITokenAssign = regexp.MustCompile(`(?i)(?:api[_-]?key|api[_-]?token|auth[_-]?token|secret[_-]?key)\s*[=:]\s*\S+`)
)

// --- Rule implementations ---

func checkDangerousDelete(ctx *scanContext) *RuleFinding {
	for _, m := range []struct {
		re  *regexp.Regexp
		msg string
	}{
		{reRmRf, "recursive force remove detected"},
		{reKillall, "indiscriminate process termination detected"},
		{reShutdown, "system shutdown/reboot command detected"},
	} {
		if loc := m.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R1-DANGEROUS-DELETE",
				RiskLevel: RiskCritical,
				Category:  CatDangerousCmd,
				Evidence:  snippet,
			}
		}
	}
	return nil
}

func checkDangerousOverwrite(ctx *scanContext) *RuleFinding {
	for _, m := range []struct {
		re  *regexp.Regexp
		msg string
	}{
		{reMkfs, "filesystem format command detected"},
		{reDdOf, "dd writing to block device detected"},
		{reRedirectDev, "redirect to block device detected"},
		{reChmod777, "world-writable permission change detected"},
	} {
		if loc := m.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R1-DANGEROUS-OVERWRITE",
				RiskLevel: RiskCritical,
				Category:  CatDangerousCmd,
				Evidence:  snippet,
			}
		}
	}
	return nil
}

func checkSensitivePath(ctx *scanContext) *RuleFinding {
	type pathCheck struct {
		re   *regexp.Regexp
		desc string
	}
	checks := []pathCheck{
		{reSSHPath, "SSH key directory access"},
		{reAWSPath, "AWS credentials directory access"},
		{reGCloudPath, "Google Cloud credentials access"},
		{reEtcShadow, "system authentication file access"},
		{reKubeConfig, "Kubernetes config access"},
		{reEnvFile, ".env file access (may contain secrets)"},
		{rePemFile, ".pem certificate/key file access"},
		{reKeyFile, "private key file access"},
		{reCredentials, "credential file access"},
	}
	for _, c := range checks {
		if loc := c.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R1-SENSITIVE-PATH",
				RiskLevel: RiskHigh,
				Category:  CatDangerousCmd,
				Evidence:  snippet + " (" + c.desc + ")",
			}
		}
	}
	// Also check user-configured denied path patterns.
	for _, pat := range ctx.policy.DeniedPathPatterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		if loc := re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R1-SENSITIVE-PATH",
				RiskLevel: RiskHigh,
				Category:  CatDangerousCmd,
				Evidence:  snippet + " (matches deny pattern: " + pat + ")",
			}
		}
	}
	return nil
}

func checkDeniedCommand(ctx *scanContext) *RuleFinding {
	if ctx.parsed == nil {
		return nil
	}
	denied := ctx.policy.DeniedCommands
	if len(denied) == 0 {
		return nil
	}
	for _, seg := range ctx.parsed.segments {
		if len(seg) == 0 {
			continue
		}
		cmdName := strings.ToLower(seg[0])
		for _, d := range denied {
			if strings.EqualFold(d, cmdName) || strings.EqualFold(d, seg[0]) {
				return &RuleFinding{
					RuleID:    "R1-DENIED-COMMAND",
					RiskLevel: RiskHigh,
					Category:  CatDangerousCmd,
					Evidence:  "command '" + seg[0] + "' is on the global deny list",
				}
			}
		}
	}
	return nil
}

// --- R2: network ---

func checkBlockedNetworkTool(ctx *scanContext) *RuleFinding {
	blocked := ctx.policy.BlockedNetworkTools
	if len(blocked) == 0 {
		return nil
	}
	if ctx.parsed == nil {
		// Without parsing, do rough substring match.
		for _, bt := range blocked {
			if containsWord(ctx.command, bt) {
				return &RuleFinding{
					RuleID:    "R2-BLOCKED-NETWORK-TOOL",
					RiskLevel: RiskHigh,
					Category:  CatNetwork,
					Evidence:  "blocked network tool '" + bt + "' detected in command",
				}
			}
		}
		return nil
	}
	for _, seg := range ctx.parsed.segments {
		if len(seg) == 0 {
			continue
		}
		for _, bt := range blocked {
			if strings.EqualFold(seg[0], bt) {
				return &RuleFinding{
					RuleID:    "R2-BLOCKED-NETWORK-TOOL",
					RiskLevel: RiskHigh,
					Category:  CatNetwork,
					Evidence:  "blocked network tool '" + seg[0] + "' detected",
				}
			}
		}
	}
	return nil
}

func checkNonWhitelistDomain(ctx *scanContext) *RuleFinding {
	allowlist := ctx.policy.AllowedDomains
	// If no allowlist is configured, we don't block unknown domains.
	if len(allowlist) == 0 {
		return nil
	}
	urls := reURL.FindAllStringSubmatch(ctx.command, -1)
	for _, match := range urls {
		if len(match) < 2 {
			continue
		}
		domain := match[1]
		// Strip port if present.
		if idx := strings.LastIndex(domain, ":"); idx > 0 {
			domain = domain[:idx]
		}
		if !isDomainAllowed(domain, allowlist) {
			return &RuleFinding{
				RuleID:    "R2-NON-WHITELIST-DOMAIN",
				RiskLevel: RiskHigh,
				Category:  CatNetwork,
				Evidence:  "domain '" + domain + "' is not in the network whitelist",
			}
		}
	}
	return nil
}

func isDomainAllowed(domain string, allowlist []string) bool {
	domain = strings.ToLower(domain)
	for _, a := range allowlist {
		a = strings.ToLower(a)
		if domain == a {
			return true
		}
		// Allow subdomains: "*.example.com" matches "api.example.com".
		if strings.HasPrefix(a, "*.") {
			suffix := a[1:] // .example.com
			if strings.HasSuffix(domain, suffix) {
				return true
			}
		}
	}
	return false
}

// --- R3: shell bypass ---

// shellReexecCmds are true shell re-execution primitives: they launch a
// child shell and execute arbitrary code under a different argv[0].
// These are RiskCritical because they can trivially bypass any
// command-name check (e.g. "sh -c 'rm -rf /'" passes a deny on "rm").
var shellReexecCmds = map[string]struct{}{
	"sh": {}, "bash": {}, "zsh": {}, "ash": {}, "dash": {},
	"ksh": {}, "mksh": {}, "fish": {}, "pwsh": {}, "powershell": {},
	"cmd": {}, "busybox": {}, "toybox": {},
	"eval": {}, "exec": {}, "command": {}, "source": {}, ".": {},
}

// shellWrapperCmds are utility wrappers that take a command argument and
// exec it — still dangerous (they can be used to launder a blocked
// command name) but less trivial to weaponise than a raw shell.
// RiskHigh so they are still auto-denied by default.
var shellWrapperCmds = map[string]struct{}{
	"xargs": {}, "env": {}, "nohup": {}, "timeout": {},
	"sudo": {}, "su": {}, "doas": {},
	"setsid": {}, "unshare": {}, "chroot": {}, "runuser": {},
	"script": {}, "flock": {},
}

func checkShellReexec(ctx *scanContext) *RuleFinding {
	if ctx.parsed == nil {
		return nil
	}
	for _, seg := range ctx.parsed.segments {
		if len(seg) == 0 {
			continue
		}
		name := strings.ToLower(seg[0])
		if _, ok := shellReexecCmds[name]; ok {
			return &RuleFinding{
				RuleID:    "R3-SHELL-REEXEC",
				RiskLevel: RiskCritical,
				Category:  CatShellBypass,
				Evidence: "command '" + seg[0] + "' is a shell re-execution " +
					"primitive that can bypass command-name checks",
			}
		}
	}
	return nil
}

func checkShellWrapper(ctx *scanContext) *RuleFinding {
	if ctx.parsed == nil {
		return nil
	}
	for _, seg := range ctx.parsed.segments {
		if len(seg) == 0 {
			continue
		}
		name := strings.ToLower(seg[0])
		if _, ok := shellWrapperCmds[name]; ok {
			return &RuleFinding{
				RuleID:    "R3-SHELL-WRAPPER",
				RiskLevel: RiskHigh,
				Category:  CatShellBypass,
				Evidence: "command '" + seg[0] + "' is a wrapper that can " +
					"bypass command-name checks",
			}
		}
	}
	return nil
}

func checkBase64Bypass(ctx *scanContext) *RuleFinding {
	for _, m := range []struct {
		re   *regexp.Regexp
		desc string
	}{
		{reBase64Encode, "base64 decode pipeline"},
		{reBase64Pipe, "base64 pipe to shell"},
	} {
		if loc := m.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R3-BASE64-BYPASS",
				RiskLevel: RiskHigh,
				Category:  CatShellBypass,
				Evidence:  snippet + " (" + m.desc + ")",
			}
		}
	}
	return nil
}

func checkHexBypass(ctx *scanContext) *RuleFinding {
	for _, m := range []struct {
		re   *regexp.Regexp
		desc string
	}{
		{reHexEncode, "hex-encoded string (possible obfuscated payload)"},
		{reXXDDecode, "xxd decode pipeline"},
	} {
		if loc := m.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R3-HEX-BYPASS",
				RiskLevel: RiskMedium,
				Category:  CatShellBypass,
				Evidence:  snippet + " (" + m.desc + ")",
			}
		}
	}
	return nil
}

// --- R4: host execution risks ---

func checkPrivilegeEscalation(ctx *scanContext) *RuleFinding {
	for _, m := range []struct {
		re   *regexp.Regexp
		desc string
	}{
		{reSudo, "privilege escalation command"},
		{reSystemctl, "system service manipulation"},
	} {
		if loc := m.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			level := RiskCritical
			if m.re == reSystemctl {
				level = RiskHigh
			}
			return &RuleFinding{
				RuleID:    "R4-HOST-PRIVILEGE-ESCALATION",
				RiskLevel: level,
				Category:  CatHostRisk,
				Evidence:  snippet + " (" + m.desc + ")",
			}
		}
	}
	return nil
}

func checkBackgroundProcess(ctx *scanContext) *RuleFinding {
	if ctx.backend != "hostexec" {
		return nil // workspaceexec manages processes via Engine
	}
	if loc := reBackground.FindStringIndex(ctx.command); loc != nil {
		snippet := safeSnippet(ctx.command, loc[0], 80)
		return &RuleFinding{
			RuleID:    "R4-HOST-BACKGROUND-PROCESS",
			RiskLevel: RiskHigh,
			Category:  CatHostRisk,
			Evidence:  snippet + " (background process in hostexec may leave residue)",
		}
	}
	return nil
}

func checkPTYLongSession(ctx *scanContext) *RuleFinding {
	if ctx.backend != "hostexec" {
		return nil
	}
	// Check for actual PTY indicators: device paths, tty flags,
	// script(1) with terminal, not substring matches on "tty"/"pty"
	// which would match ordinary words like "pretty" or "empty".
	hasPTY := regexp.MustCompile(
		`(?:/dev/(?:pts|tty|ptmx))|(?:^|\s)(?:-t|--tty|--pty)\b|` +
			`\bscript\s+-q`,
	).MatchString(ctx.command)
	hasBackground := strings.Contains(ctx.command, "background") ||
		strings.Contains(ctx.command, "nohup") ||
		strings.Contains(ctx.command, "disown")
	hasNoTimeout := ctx.timeoutSec <= 0 || ctx.timeoutSec > 300
	if hasPTY && (hasBackground || hasNoTimeout) {
		return &RuleFinding{
			RuleID:    "R4-HOST-PTY-LONG-SESSION",
			RiskLevel: RiskMedium,
			Category:  CatHostRisk,
			Evidence:  "PTY session combined with background or no timeout in hostexec",
		}
	}
	return nil
}

// --- R5: dependency / env changes ---

func checkDependencyInstall(ctx *scanContext) *RuleFinding {
	type installCheck struct {
		re   *regexp.Regexp
		desc string
	}
	checks := []installCheck{
		{rePipInstall, "pip install detected"},
		{reNpmInstall, "npm install detected"},
		{reGoInstall, "go install detected"},
		{reAptInstall, "apt-get install detected"},
		{reCargoInstall, "cargo install detected"},
		{reBrewInstall, "brew install detected"},
	}
	for _, c := range checks {
		if loc := c.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R5-DEPENDENCY-INSTALL",
				RiskLevel: RiskHigh,
				Category:  CatInstall,
				Evidence:  snippet + " (" + c.desc + ")",
			}
		}
	}
	return nil
}

func checkCurlPipeBash(ctx *scanContext) *RuleFinding {
	for _, m := range []struct {
		re   *regexp.Regexp
		desc string
	}{
		{reCurlPipeShell, "curl piped to shell"},
		{reWgetPipeShell, "wget piped to shell"},
	} {
		if loc := m.re.FindStringIndex(ctx.command); loc != nil {
			snippet := safeSnippet(ctx.command, loc[0], 80)
			return &RuleFinding{
				RuleID:    "R5-CURL-PIPE-BASH",
				RiskLevel: RiskCritical,
				Category:  CatInstall,
				Evidence:  snippet + " (" + m.desc + ")",
			}
		}
	}
	return nil
}

func checkEnvModification(ctx *scanContext) *RuleFinding {
	if loc := reExportEnv.FindStringIndex(ctx.command); loc != nil {
		snippet := safeSnippet(ctx.command, loc[0], 80)
		return &RuleFinding{
			RuleID:    "R5-ENV-MODIFICATION",
			RiskLevel: RiskMedium,
			Category:  CatInstall,
			Evidence:  snippet + " (environment variable modification)",
		}
	}
	return nil
}

// --- R6: resource abuse ---

func checkExcessiveTimeout(ctx *scanContext) *RuleFinding {
	maxTimeout := ctx.policy.EffectiveMaxTimeout(ctx.backend)
	if maxTimeout <= 0 || ctx.timeoutSec <= 0 {
		return nil
	}
	if ctx.timeoutSec > maxTimeout {
		return &RuleFinding{
			RuleID:    "R6-EXCESSIVE-TIMEOUT",
			RiskLevel: RiskHigh,
			Category:  CatResource,
			Evidence:  "requested timeout " + itoa(ctx.timeoutSec) + "s exceeds max " + itoa(maxTimeout) + "s",
		}
	}
	return nil
}

func checkLongSleep(ctx *scanContext) *RuleFinding {
	match := reSleep.FindStringSubmatch(ctx.command)
	if len(match) < 2 {
		return nil
	}
	secs, err := strconv.Atoi(match[1])
	if err != nil || secs < 0 {
		// Overflow or unparsable: treat as long sleep.
		return &RuleFinding{
			RuleID:    "R6-LONG-SLEEP",
			RiskLevel: RiskHigh,
			Category:  CatResource,
			Evidence:  "sleep " + match[1] + " — unparsable or overflow duration, treating as excessive",
		}
	}
	if secs > 60 {
		return &RuleFinding{
			RuleID:    "R6-LONG-SLEEP",
			RiskLevel: RiskHigh,
			Category:  CatResource,
			Evidence:  "sleep " + match[1] + "s wastes resources",
		}
	}
	return nil
}

func checkForkBomb(ctx *scanContext) *RuleFinding {
	if loc := reForkBomb.FindStringIndex(ctx.command); loc != nil {
		snippet := safeSnippet(ctx.command, loc[0], 80)
		return &RuleFinding{
			RuleID:    "R6-FORK-BOMB",
			RiskLevel: RiskCritical,
			Category:  CatResource,
			Evidence:  snippet + " (fork bomb pattern detected)",
		}
	}
	return nil
}

func checkExcessiveOutput(ctx *scanContext) *RuleFinding {
	maxOut := ctx.policy.EffectiveMaxOutput(ctx.backend)
	if maxOut > 0 && ctx.outputBytes > 0 && ctx.outputBytes > maxOut {
		return &RuleFinding{
			RuleID:    "R6-EXCESSIVE-OUTPUT",
			RiskLevel: RiskMedium,
			Category:  CatResource,
			Evidence:  "output size " + itoa(int(ctx.outputBytes)) + " bytes exceeds max " + itoa(int(maxOut)) + " bytes",
		}
	}
	// Detect commands likely to produce huge output regardless of
	// whether the caller specified outputBytes.
	if loc := reFindRoot.FindStringIndex(ctx.command); loc != nil {
		snippet := safeSnippet(ctx.command, loc[0], 80)
		return &RuleFinding{
			RuleID:    "R6-EXCESSIVE-OUTPUT",
			RiskLevel: RiskMedium,
			Category:  CatResource,
			Evidence:  snippet + " (recursive root search may produce large output)",
		}
	}
	if strings.Contains(ctx.command, "/dev/zero") || strings.Contains(ctx.command, "/dev/urandom") {
		return &RuleFinding{
			RuleID:    "R6-EXCESSIVE-OUTPUT",
			RiskLevel: RiskMedium,
			Category:  CatResource,
			Evidence:  "reading from " + safeSnippet(ctx.command, 0, 80) + " may produce unbounded output",
		}
	}
	return nil
}

// --- R7: sensitive output ---

func checkSensitiveOutput(ctx *scanContext) *RuleFinding {
	// Check built-in patterns. All built-in matches are redacted in
	// Evidence to avoid leaking secrets into scan reports and audit.
	patterns := []struct {
		re   *regexp.Regexp
		desc string
	}{
		{reAWSKey, "AWS access key"},
		{reGitHubToken, "GitHub personal access token"},
		{rePrivateKeyHdr, "private key header"},
		{reJWT, "JWT token"},
		{rePasswordAssign, "password assignment"},
		{reAPITokenAssign, "API token/key assignment"},
	}
	for _, p := range patterns {
		if p.re.MatchString(ctx.command) {
			return &RuleFinding{
				RuleID:    "R7-SENSITIVE-OUTPUT",
				RiskLevel: RiskHigh,
				Category:  CatSensitive,
				Evidence:  "***REDACTED*** (potential " + p.desc + " leak)",
			}
		}
	}
	// Check user-configured sensitive patterns.
	for _, sp := range ctx.policy.SensitivePatterns {
		re, err := regexp.Compile(sp.Pattern)
		if err != nil {
			continue
		}
		if re.MatchString(ctx.command) {
			return &RuleFinding{
				RuleID:    "R7-SENSITIVE-OUTPUT",
				RiskLevel: RiskHigh,
				Category:  CatSensitive,
				Evidence:  "***REDACTED*** (matches sensitive pattern: " + sp.Name + ")",
			}
		}
	}
	return nil
}

// --- helpers ---

func safeSnippet(s string, start, maxLen int) string {
	if start < 0 {
		start = 0
	}
	if start >= len(s) {
		return ""
	}
	end := start + maxLen
	if end > len(s) {
		end = len(s)
	}
	snippet := s[start:end]
	// Redact potential secrets in evidence snippets.
	snippet = reAWSKey.ReplaceAllString(snippet, "***REDACTED***")
	snippet = reGitHubToken.ReplaceAllString(snippet, "***REDACTED***")
	snippet = reJWT.ReplaceAllString(snippet, "***REDACTED***")
	return snippet
}

func containsWord(s, word string) bool {
	lower := strings.ToLower(s)
	w := strings.ToLower(word)
	idx := strings.Index(lower, w)
	if idx < 0 {
		return false
	}
	end := idx + len(w)
	leftOK := idx == 0 || lower[idx-1] == ' ' || lower[idx-1] == '|' ||
		lower[idx-1] == ';' || lower[idx-1] == '&'
	rightOK := end >= len(lower) || lower[end] == ' ' ||
		lower[end] == '|' || lower[end] == ';' || lower[end] == '&' ||
		lower[end] == '\n' || lower[end] == '\r'
	return leftOK && rightOK
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
