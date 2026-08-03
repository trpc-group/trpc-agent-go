//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package controlledegress implements host-side L4/L7 policy and transport
// helpers for sandbox controlled network egress.
package controlledegress

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Target is a normalized outbound destination observed by the proxy.
type Target struct {
	Host       string // hostname or IP literal (no brackets)
	Port       int
	Scheme     string // http, https, or empty for CONNECT
	Path       string // path+query for HTTP; empty for CONNECT
	Connect    bool
	Original   string
	HostHeader string
}

// Authority returns host:port suitable for dialing / CONNECT.
func (t Target) Authority() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// ParseHTTPAbsoluteForm parses an HTTP absolute-form request URL.
func ParseHTTPAbsoluteForm(raw string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("parse absolute URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Target{}, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return Target{}, fmt.Errorf("missing host in absolute URL")
	}
	host, port, err := splitHostPort(u.Host, defaultPort(u.Scheme))
	if err != nil {
		return Target{}, err
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return Target{
		Host:     host,
		Port:     port,
		Scheme:   u.Scheme,
		Path:     path,
		Original: raw,
	}, nil
}

// ParseCONNECTTarget parses a CONNECT authority (host:port).
func ParseCONNECTTarget(authority string) (Target, error) {
	host, port, err := splitHostPort(authority, 443)
	if err != nil {
		return Target{}, err
	}
	return Target{
		Host:     host,
		Port:     port,
		Scheme:   "https",
		Connect:  true,
		Original: authority,
	}, nil
}

func defaultPort(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}

func splitHostPort(hostport string, fallbackPort int) (string, int, error) {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return "", 0, fmt.Errorf("empty host")
	}
	if _, _, err := net.SplitHostPort(hostport); err != nil {
		if ip := net.ParseIP(hostport); ip != nil && ip.To4() == nil {
			return "", 0, fmt.Errorf(
				"invalid host:port %q: IPv6 requires brackets",
				hostport,
			)
		}
		return strings.ToLower(hostport), fallbackPort, nil
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", 0, fmt.Errorf("invalid host:port %q: %w", hostport, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return strings.ToLower(host), port, nil
}

// RunIdentity is trusted host-side correlation metadata for one proxy owner.
// It must not be populated from guest-controlled request headers or payloads.
type RunIdentity struct {
	Principal string
	Session   string
	Request   string
}

// AuditEvent records a policy decision without bodies or credentials.
type AuditEvent struct {
	Principal string
	Session   string
	Request   string
	Allow     bool
	Reason    string
	Target    Target
	IPs       []string
}

// Auditor receives decision events. Implementations must support concurrent
// calls.
type Auditor interface {
	Record(ctx context.Context, event AuditEvent)
}

type runIdentityContextKey struct{}

func withRunIdentity(ctx context.Context, identity RunIdentity) context.Context {
	return context.WithValue(ctx, runIdentityContextKey{}, identity)
}

func runIdentityFromContext(ctx context.Context) RunIdentity {
	identity, _ := ctx.Value(runIdentityContextKey{}).(RunIdentity)
	return identity
}

// Decision is the result of a policy evaluation.
type Decision struct {
	Allow   bool
	Reason  string
	Target  Target
	DialIPs []net.IP
}

// Authorizer is an optional per-request gate (e.g. human approval in PR C).
// Implementations must support concurrent calls.
type Authorizer interface {
	Allow(ctx context.Context, target Target) error
}

// AllowFunc adapts a function to Authorizer.
type AllowFunc func(ctx context.Context, target Target) error

// Allow implements Authorizer.
func (f AllowFunc) Allow(ctx context.Context, target Target) error {
	return f(ctx, target)
}

// Policy evaluates whether a target may be dialed.
type Policy struct {
	// AllowedHosts are exact hosts or suffix patterns like "*.example.com".
	// Empty means deny all hosts (fail closed).
	AllowedHosts []string
	// AllowedPorts, when non-empty, restricts destination ports.
	AllowedPorts []int
	Resolver     Resolver
	Authorizer   Authorizer
	Auditor      Auditor
}

// Validate checks policy syntax before a proxy starts serving.
func (p Policy) Validate() error {
	for _, raw := range p.AllowedHosts {
		pattern := strings.ToLower(strings.TrimSpace(raw))
		if pattern == "" {
			return fmt.Errorf("allowed host pattern is empty")
		}
		if !strings.Contains(pattern, "*") {
			continue
		}
		if !strings.HasPrefix(pattern, "*.") ||
			strings.Count(pattern, "*") != 1 {
			return fmt.Errorf("invalid wildcard host pattern %q", raw)
		}
		base := strings.TrimPrefix(pattern, "*.")
		if _, err := publicsuffix.EffectiveTLDPlusOne(base); err != nil {
			return fmt.Errorf("wildcard host pattern %q is too broad", raw)
		}
	}
	for _, port := range p.AllowedPorts {
		if port <= 0 || port > 65535 {
			return fmt.Errorf("invalid allowed port %d", port)
		}
	}
	return nil
}

// Decide validates L7 host/port rules, optional authorizer, then L4/DNS.
func (p Policy) Decide(ctx context.Context, target Target) Decision {
	d := Decision{Target: target}
	if target.Host == "" || target.Port <= 0 {
		d.Reason = "invalid target"
		p.audit(ctx, d)
		return d
	}
	if !p.hostAllowed(target.Host) {
		d.Reason = "host not allowlisted"
		p.audit(ctx, d)
		return d
	}
	if !p.portAllowed(target.Port) {
		d.Reason = "port not allowlisted"
		p.audit(ctx, d)
		return d
	}
	if p.Authorizer != nil {
		if err := p.Authorizer.Allow(ctx, target); err != nil {
			d.Reason = "authorizer denied"
			p.audit(ctx, d)
			return d
		}
	}
	ips, err := resolveAndValidate(ctx, p.Resolver, target.Host)
	if err != nil {
		d.Reason = err.Error()
		p.audit(ctx, d)
		return d
	}
	d.Allow = true
	d.Reason = "allow"
	d.DialIPs = ips
	p.audit(ctx, d)
	return d
}

func (p Policy) hostAllowed(host string) bool {
	host = strings.ToLower(host)
	if len(p.AllowedHosts) == 0 {
		return false
	}
	for _, pattern := range p.AllowedHosts {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if pattern == host {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := pattern[1:]
			if strings.HasSuffix(host, suffix) &&
				host != strings.TrimPrefix(suffix, ".") {
				return true
			}
			if host == strings.TrimPrefix(pattern, "*.") {
				return true
			}
		}
	}
	return false
}

func (p Policy) portAllowed(port int) bool {
	if len(p.AllowedPorts) == 0 {
		return port == 80 || port == 443
	}
	for _, allowed := range p.AllowedPorts {
		if allowed == port {
			return true
		}
	}
	return false
}

func (p Policy) audit(ctx context.Context, d Decision) {
	if p.Auditor == nil {
		return
	}
	identity := runIdentityFromContext(ctx)
	target := d.Target
	target.Path = ""
	target.Original = ""
	target.HostHeader = ""
	p.Auditor.Record(ctx, AuditEvent{
		Principal: identity.Principal,
		Session:   identity.Session,
		Request:   identity.Request,
		Allow:     d.Allow,
		Reason:    d.Reason,
		Target:    target,
		IPs:       formatIPs(d.DialIPs),
	})
}

func formatIPs(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

// StaticAllowlist is a convenience constructor.
func StaticAllowlist(hosts ...string) Policy {
	return Policy{AllowedHosts: append([]string(nil), hosts...)}
}

// ErrDenied is returned by Authorizer implementations for a hard deny.
var ErrDenied = fmt.Errorf("controlled egress denied")
