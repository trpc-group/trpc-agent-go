//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// Network target extraction for the R-NET-001 rule. The whitelist check itself
// lives in ruleNetwork (rules.go); this file is the parsing layer that turns a
// download command's arguments into the set of hosts/IPs the command will
// actually talk to. It is the security-critical half: every whitelist bypass
// hides in an option this parser fails to account for, so curl gets a dedicated
// tokenizer (short bundles, connection-redirect/proxy options, opaque config)
// and the other download commands share a per-command option classification.

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

var (
	urlRe      = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s'"]+`)
	userHostRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+)(?::.*)?$`)
	domainRe   = regexp.MustCompile(`(?i)^[a-z0-9.-]+$`)
)

// optClass classifies an option of a non-curl download command for the network
// rule. Unlisted options are treated as boolean, which fails toward flagging: a
// boolean never consumes an operand, so the operand after it is still parsed as
// a potential host.
type optClass int

const (
	// optValue: the option's value is an opaque non-host string (filename,
	// header, credential) carried inline ("--flag=value", "-Xvalue") or in the
	// next argument; it is consumed so it is not mistaken for a bare host
	// (e.g. "wget -O config.yaml" must not treat config.yaml as a host).
	optValue optClass = iota + 1
	// optHost: the option's value names the real connection target(s) — proxy,
	// jump hosts, forwarding specs — and every host/IP in it must be checked
	// against the whitelist.
	optHost
	// optOpaque: the option is an out-of-band egress control the guard cannot
	// audit (a config file, an arbitrary rc directive or client option, a
	// replacement transport program); its mere presence fails closed, mirroring
	// curl -K/--config.
	optOpaque
)

// genericOptions classifies, per non-curl download command, the options the
// network rule must not treat as booleans. Boolean flags (wget -q, ssh -v) are
// NOT listed, so the operand after them is still parsed and "wget -q evil.io"
// cannot bypass the whitelist. Long options match exactly; single-letter
// options are also resolved inside getopt-style bundles and inline values
// ("-qe X", "-Jhost", "-oKey=Val"). curl is not here — its richer option
// surface is handled by extractCurlHosts / curlOpaqueConfigOption.
var genericOptions = map[string]map[string]optClass{
	"wget": {
		"-O": optValue, "--output-document": optValue,
		"-o": optValue, "--output-file": optValue,
		"-a": optValue, "--append-output": optValue,
		"-P": optValue, "--directory-prefix": optValue,
		"-U": optValue, "--user-agent": optValue,
		"--header": optValue, "--proxy-user": optValue, "--proxy-password": optValue,
		// -e/--execute injects an arbitrary .wgetrc directive
		// (http_proxy/https_proxy/use_proxy redirect the real egress);
		// --config points at an opaque config file; -i/--input-file reads the
		// URL list from a file the guard cannot see. All fail closed.
		"-e": optOpaque, "--execute": optOpaque, "--config": optOpaque,
		"-i": optOpaque, "--input-file": optOpaque,
	},
	"ssh": {
		// -J routes through jump hosts; -W/-L/-R carry host:port forwarding
		// targets. Every host in their values is whitelist-checked.
		"-J": optHost, "-W": optHost, "-L": optHost, "-R": optHost,
		// -o sets any client option (ProxyCommand/ProxyJump/Hostname);
		// -F reads an opaque config file. Both fail closed.
		"-o": optOpaque, "-F": optOpaque,
		"-i": optValue, "-l": optValue, "-p": optValue, "-b": optValue,
		"-c": optValue, "-m": optValue, "-e": optValue, "-E": optValue,
		"-D": optValue, "-I": optValue, "-Q": optValue, "-S": optValue,
		"-w": optValue, "-B": optValue,
	},
	"scp": {
		"-J": optHost,
		// -o/-F as for ssh; -S swaps in an arbitrary transport program that
		// then owns the connection, so it is as opaque as a ProxyCommand.
		"-o": optOpaque, "-F": optOpaque, "-S": optOpaque,
		"-i": optValue, "-l": optValue, "-P": optValue, "-c": optValue,
		"-D": optValue, "-X": optValue,
	},
	"sftp": {
		"-J": optHost,
		"-o": optOpaque, "-F": optOpaque, "-S": optOpaque,
		"-i": optValue, "-l": optValue, "-P": optValue, "-c": optValue,
		"-b": optValue, "-B": optValue, "-D": optValue, "-R": optValue,
		"-X": optValue,
	},
	"nc": {
		// -x routes through a SOCKS/HTTP proxy.
		"-x": optHost,
		"-X": optValue, "-p": optValue, "-s": optValue, "-w": optValue,
		"-i": optValue, "-I": optValue, "-O": optValue, "-T": optValue,
	},
}

// curlHostBearingLong are curl long options whose value carries a connection
// target or the request URL that the whitelist must still see. --connect-to and
// --resolve redirect the connection to a host different from the request URL;
// --proxy/--preproxy route all traffic through a proxy; --url sets the request
// URL out of band. Every host/IP in the value is extracted so a redirect such as
// "--connect-to github.com:443:evil.io:443" cannot smuggle evil.io past a
// github.com whitelist.
var curlHostBearingLong = map[string]bool{
	"--connect-to": true, "--resolve": true,
	"--proxy": true, "--preproxy": true, "--url": true,
	"--dns-servers": true, "--doh-url": true,
}

// curlLongValueOptions are curl long options whose value is an opaque
// string/filename to skip (so it is not mistaken for a host). Host-bearing long
// options are handled separately and are intentionally absent here.
var curlLongValueOptions = map[string]bool{
	"--output": true, "--upload-file": true, "--data": true, "--data-binary": true,
	"--data-raw": true, "--data-ascii": true, "--form": true, "--header": true,
	"--user-agent": true, "--referer": true, "--cookie": true, "--cookie-jar": true,
	"--user": true, "--config": true, "--output-dir": true,
}

// curlShortValueBytes are curl short flags that consume a value: the value is the
// remainder of the bundle token if any, else the next argument. 'x' is the proxy
// flag (its value is a host); 'K' is the opaque config file (detected for
// fail-closed by curlOpaqueConfigOption). Boolean flags (s, S, L, f, v, k, ...)
// are absent, so "curl -sSL evil.io" still parses evil.io.
var curlShortValueBytes = map[byte]bool{
	'o': true, 'T': true, 'd': true, 'F': true, 'H': true, 'A': true,
	'e': true, 'b': true, 'c': true, 'u': true, 'K': true, 'x': true,
}

// extractHosts pulls candidate hosts from a download command's arguments. It is
// only called for configured download commands (curl, wget, nc, ssh, scp, ...),
// all of which take a host/URL operand. curl gets a dedicated parser because its
// option surface (short bundles, connection-redirect and proxy options, the
// opaque config file) is where whitelist bypasses hide; other commands share a
// generic parser driven by the per-command genericOptions classification
// (opaque egress controls are handled separately by genericOpaqueOption in
// ruleNetwork).
func extractHosts(cmd string, args []string) []string {
	if cmd == "curl" {
		return extractCurlHosts(args)
	}
	return extractGenericHosts(cmd, args)
}

// extractGenericHosts parses non-curl download commands (wget, nc, ssh, scp,
// ftp, ...). A value-taking option (genericOptions) consumes its following
// operand; a host-bearing option (proxy, jump host, forwarding spec)
// contributes every host in its value; everything else that is a URL,
// user@host or bare domain/IP operand is a host candidate. Short options are
// resolved through getopt-style bundles ("-vJhost", "-qO file") so a bundled
// host-bearing flag cannot hide its value.
func extractGenericHosts(cmd string, args []string) []string {
	var hosts []string
	opts := genericOptions[cmd]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			hosts = append(hosts, operandHosts(a)...)
			continue
		}
		var cl optClass
		var inline string
		var hasInline bool
		if strings.HasPrefix(a, "--") {
			var flag string
			flag, inline, hasInline = splitFlagValue(a)
			cl = opts[flag]
		} else {
			cl, _, inline, hasInline = scanGenericShortBundle(opts, a)
		}
		switch cl {
		case optHost:
			val := inline
			if !hasInline && i+1 < len(args) {
				val, i = args[i+1], i+1
			}
			hosts = append(hosts, hostsFromGenericValue(val)...)
		case optValue, optOpaque:
			if !hasInline {
				i++
			}
		}
	}
	return hosts
}

// scanGenericShortBundle walks a getopt-style short-option token ("-vJhost").
// Letters not in opts are booleans and are skipped; the first classified
// (value-taking) letter wins and its value is the remainder of the token when
// non-empty, else the next argument. A token with no classified letter is all
// booleans: class 0, nothing consumed.
func scanGenericShortBundle(opts map[string]optClass, token string) (cl optClass, flag, inline string, hasInline bool) {
	for j := 1; j < len(token); j++ {
		f := "-" + string(token[j])
		c, ok := opts[f]
		if !ok {
			continue
		}
		rest := token[j+1:]
		return c, f, rest, rest != ""
	}
	return 0, "", "", false
}

// hostsFromGenericValue extracts every host/IP from a host-bearing option
// value: comma-separated [user@]host[:port] hops (ssh/scp -J), a
// [bind:]port:host:hostport forwarding spec (ssh -W/-L/-R) or a proxy address
// (nc -x). Ports and empty fields are dropped; it is deliberately
// over-inclusive so any non-whitelisted destination in the spec trips the
// network rule.
func hostsFromGenericValue(val string) []string {
	var hosts []string
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if m := userHostRe.FindStringSubmatch(part); m != nil {
			hosts = append(hosts, m[1])
			continue
		}
		hosts = append(hosts, hostsFromColonSpec(part)...)
	}
	return hosts
}

// genericOpaqueOption reports whether a non-curl download command carries an
// option classified optOpaque (wget -e/--execute/--config, ssh/scp -o/-F,
// scp/sftp -S): an out-of-band egress control whose effect the guard cannot
// read, so its mere presence fails closed, mirroring curl -K/--config. Short
// options are resolved through getopt bundles ("-qe X", "-oKey=Val") so a
// bundled opaque flag still counts, while an opaque letter inside another
// option's inline value does not.
func genericOpaqueOption(cmd string, args []string) (string, bool) {
	opts := genericOptions[cmd]
	if len(opts) == 0 {
		return "", false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || len(a) == 1 {
			continue
		}
		var cl optClass
		var flag string
		var hasInline bool
		if strings.HasPrefix(a, "--") {
			flag, _, hasInline = splitFlagValue(a)
			cl = opts[flag]
		} else {
			cl, flag, _, hasInline = scanGenericShortBundle(opts, a)
		}
		if cl == optOpaque {
			return flag, true
		}
		// Any value-taking option consumes the next argument, so that value is
		// not itself scanned as an option token.
		if cl != 0 && !hasInline {
			i++
		}
	}
	return "", false
}

// extractCurlHosts parses curl arguments, resolving short-flag bundles, long
// options in both "--flag value" and "--flag=value" forms, connection-redirect
// and proxy options, and the request URL operand. Boolean flags never consume an
// operand, so "curl -sSL evil.io" is still flagged.
func extractCurlHosts(args []string) []string {
	var hosts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--"):
			flag, inlineVal, hasInline := splitFlagValue(a)
			if curlHostBearingLong[flag] {
				val := inlineVal
				if !hasInline && i+1 < len(args) {
					val, i = args[i+1], i+1
				}
				hosts = append(hosts, hostsFromCurlValue(flag, val)...)
				continue
			}
			if curlLongValueOptions[flag] && !hasInline {
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			extra, consumesNext, proxyNext := parseCurlShortBundle(a)
			hosts = append(hosts, extra...)
			if consumesNext && i+1 < len(args) {
				if proxyNext {
					hosts = append(hosts, hostsFromCurlValue("-x", args[i+1])...)
				}
				i++
			}
		default:
			hosts = append(hosts, operandHosts(a)...)
		}
	}
	return hosts
}

// parseCurlShortBundle walks a "-sSx"-style short-flag bundle. curl's first
// value-taking flag consumes the remainder of the token as its value, or the
// next argument when the token ends there. It returns any hosts read from an
// inline proxy value, whether the following argument is consumed as a value, and
// whether that following argument is a proxy host (so the caller parses it).
func parseCurlShortBundle(token string) (hosts []string, consumesNext, proxyNext bool) {
	for j := 1; j < len(token); j++ {
		c := token[j]
		if !curlShortValueBytes[c] {
			continue // boolean flag; keep scanning the bundle
		}
		rest := token[j+1:]
		if c == 'x' { // proxy: value is the rest of the token or the next arg
			if rest != "" {
				return hostsFromCurlValue("-x", rest), false, false
			}
			return nil, true, true
		}
		// Other value flags (incl 'K'): consume the rest of the token, or the
		// next arg when the value is not inline. The value itself is not a host.
		return nil, rest == "", false
	}
	return nil, false, false
}

// operandHosts extracts host candidates from a non-option operand: a full URL, a
// user@host (scp/ssh) form, or a bare domain/IP (with optional port or path).
func operandHosts(a string) []string {
	if h := hostFromURL(a); h != "" {
		return []string{h}
	}
	if mm := userHostRe.FindStringSubmatch(a); mm != nil {
		return []string{mm[1]}
	}
	// user@[2001:db8::1]:/path — a bracketed IPv6 behind a user prefix is
	// outside userHostRe's host charset; hand the bracketed part to bareHost.
	if at := strings.IndexByte(a, '@'); at > 0 && strings.HasPrefix(a[at+1:], "[") {
		return bareHost(a[at+1:])
	}
	return bareHost(a)
}

// hostFromURL returns the host of a URL embedded in a, or "" when there is none.
func hostFromURL(a string) string {
	if m := urlRe.FindString(a); m != "" {
		if u, err := url.Parse(m); err == nil {
			return u.Hostname()
		}
	}
	return ""
}

// bareHost extracts a single host from a "host[:port][/path]" operand. It strips
// the port and path and drops non-host tokens (relative paths, user@path forms,
// pure ports). Both domains and raw IPs are accepted so "ssh 1.2.3.4" cannot
// bypass the whitelist.
func bareHost(a string) []string {
	// A raw IPv6 literal ("nc 2001:db8::1") is all colons: it must parse as a
	// whole before the port stripping below shatters it at the first colon.
	if net.ParseIP(a) != nil {
		return []string{a}
	}
	// Bracketed IPv6, optionally with a port or path: [2001:db8::1]:443.
	if strings.HasPrefix(a, "[") {
		if end := strings.IndexByte(a, ']'); end > 1 {
			if ip := a[1:end]; net.ParseIP(ip) != nil {
				return []string{ip}
			}
		}
		return nil
	}
	host := a
	if j := strings.IndexByte(host, ':'); j > 0 {
		host = host[:j]
	}
	if j := strings.IndexByte(host, '/'); j >= 0 {
		host = host[:j]
	}
	if host == "" || strings.ContainsRune(host, '@') {
		return nil
	}
	if domainLike(host) || net.ParseIP(host) != nil {
		return []string{host}
	}
	return nil
}

// splitFlagValue splits "--flag=value" into its parts. Without an "=" it returns
// the whole flag and hasInline=false.
func splitFlagValue(arg string) (flag, val string, hasInline bool) {
	if i := strings.IndexByte(arg, '='); i >= 0 {
		return arg[:i], arg[i+1:], true
	}
	return arg, "", false
}

// hostsFromCurlValue extracts the real destination host(s)/IP(s) from a curl
// host-bearing option value.
func hostsFromCurlValue(flag, val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil
	}
	switch flag {
	case "-x", "--proxy", "--preproxy":
		// [scheme://]host[:port].
		if strings.Contains(val, "://") {
			if u, err := url.Parse(val); err == nil && u.Hostname() != "" {
				return []string{u.Hostname()}
			}
		}
		return hostsFromColonSpec(val)
	case "--resolve":
		// [+]HOST:PORT:ADDR[,ADDR]. The address tail is everything after the
		// second colon, so a dedicated parser is needed: the generic colon
		// splitter shatters an unbracketed IPv6 addr (2001:db8::1) into port-
		// like fragments that all get dropped, letting the rewrite ride a
		// whitelisted HOST past the network rule.
		return hostsFromResolveSpec(val)
	case "--connect-to", "--dns-servers":
		// HOST1:PORT1:HOST2:PORT2 / IP[,IP]: every host/IP field (an alternate
		// DNS server is itself an egress control). IPv6 here requires brackets,
		// which hostsFromColonSpec already honors.
		return hostsFromColonSpec(val)
	case "--url", "--doh-url":
		if h := hostFromURL(val); h != "" {
			return []string{h}
		}
		return bareHost(val)
	}
	return nil
}

// hostsFromColonSpec extracts host/IP-like tokens from a colon/comma-separated
// spec, honoring bracketed IPv6 literals. Numeric-only fields (ports) are
// dropped. It is deliberately over-inclusive: extracting every host/IP field so
// that any non-whitelisted destination in the spec trips the network rule.
func hostsFromColonSpec(spec string) []string {
	var hosts []string
	rest := spec
	// Pull out bracketed IPv6 literals first so their inner colons survive.
	for {
		open := strings.IndexByte(rest, '[')
		if open < 0 {
			break
		}
		closeRel := strings.IndexByte(rest[open:], ']')
		if closeRel < 0 {
			break
		}
		end := open + closeRel
		if inner := strings.TrimSpace(rest[open+1 : end]); inner != "" {
			hosts = append(hosts, inner)
		}
		rest = rest[:open] + " " + rest[end+1:]
	}
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool {
		return r == ':' || r == ',' || r == ' '
	}) {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if domainLike(f) || net.ParseIP(f) != nil {
			hosts = append(hosts, f)
		}
	}
	return hosts
}

// hostsFromResolveSpec parses a curl --resolve value of the form
// "[+]HOST:PORT:ADDR[,ADDR]...". curl treats everything after the second colon
// as the address list, so ADDR may be an unbracketed IPv6 literal
// ("github.com:443:2001:db8::1") whose inner colons must not be split. Splitting
// on every colon (as hostsFromColonSpec does) drops the IPv6 addr entirely and
// lets the rewrite ride the whitelisted HOST past R-NET-001. Both HOST and every
// address (each optionally bracketed or "+"-prefixed) are returned so a redirect
// to a non-whitelisted endpoint trips the network rule.
func hostsFromResolveSpec(val string) []string {
	val = strings.TrimSpace(val)
	// The optional leading "+" marks a TTL-honoring entry; strip it off HOST.
	val = strings.TrimPrefix(val, "+")
	c1 := strings.IndexByte(val, ':')
	if c1 < 0 {
		// Malformed (no port/addr); fall back to the over-inclusive splitter.
		return hostsFromColonSpec(val)
	}
	var hosts []string
	if h := cleanHostField(val[:c1]); h != "" {
		hosts = append(hosts, h)
	}
	rest := val[c1+1:]
	c2 := strings.IndexByte(rest, ':')
	if c2 < 0 {
		return hosts // "HOST:PORT" with no address list.
	}
	for _, addr := range strings.Split(rest[c2+1:], ",") {
		if h := cleanHostField(addr); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// cleanHostField normalizes a single --resolve host/address field: it strips a
// leading "+", surrounding IPv6 brackets and surrounding whitespace, then keeps
// the value only if it is a domain or a parseable IP (so ports and empty fields
// are dropped). Unbracketed IPv6 literals survive because net.ParseIP accepts
// them.
func cleanHostField(f string) string {
	f = strings.TrimSpace(f)
	f = strings.TrimPrefix(f, "+")
	f = strings.TrimPrefix(f, "[")
	f = strings.TrimSuffix(f, "]")
	f = strings.TrimSpace(f)
	if f == "" {
		return ""
	}
	if domainLike(f) || net.ParseIP(f) != nil {
		return f
	}
	return ""
}

// curlOpaqueConfigOption reports whether a -K/--config option is present, in the
// "-K file", "--config=file" or bundled short-flag ("-sK file") form. Its file
// can define url/proxy/resolve and other egress controls the guard cannot read,
// so its mere presence fails closed. Detection is intentionally conservative: a
// short-flag token containing 'K' anywhere counts, erring toward fail-closed.
func curlOpaqueConfigOption(args []string) (string, bool) {
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			if flag, _, _ := splitFlagValue(a); flag == "--config" {
				return "--config", true
			}
			continue
		}
		// Short-flag bundle: -K may appear anywhere (e.g. -sK).
		if strings.HasPrefix(a, "-") && len(a) > 1 &&
			strings.IndexByte(a[1:], 'K') >= 0 {
			return "-K", true
		}
	}
	return "", false
}

// curlDefaultConfigDisabled reports whether curl's implicit default config is
// suppressed, which is true only when -q or --disable is the very first option.
// curl checks solely the first parameter, so a later or bundled -q (e.g. "-sq")
// does not count; this stays conservative and matches curl's own behavior.
func curlDefaultConfigDisabled(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return args[0] == "-q" || args[0] == "--disable"
}

func domainLike(s string) bool {
	return strings.Contains(s, ".") && domainRe.MatchString(s) &&
		strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
}
