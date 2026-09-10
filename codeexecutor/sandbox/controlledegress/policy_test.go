//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package controlledegress

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
)

type memoryAuditor struct {
	mu     sync.Mutex
	Events []AuditEvent
}

func (a *memoryAuditor) Record(ctx context.Context, event AuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Events = append(a.Events, event)
}

type stubResolver struct {
	addrs []net.IPAddr
	err   error
}

func (s stubResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return s.addrs, s.err
}

func TestParseTargets(t *testing.T) {
	t.Parallel()
	httpTarget, err := ParseHTTPAbsoluteForm("http://Example.COM:8080/a?b=1")
	if err != nil {
		t.Fatal(err)
	}
	if httpTarget.Host != "example.com" || httpTarget.Port != 8080 || httpTarget.Path != "/a?b=1" {
		t.Fatalf("http target = %#v", httpTarget)
	}
	connect, err := ParseCONNECTTarget("api.github.com:443")
	if err != nil {
		t.Fatal(err)
	}
	if !connect.Connect || connect.Host != "api.github.com" || connect.Port != 443 {
		t.Fatalf("connect target = %#v", connect)
	}
}

func TestValidateDialIPBlocksPrivate(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.1.1",
		"100.64.0.1", "::1", "fec0::1",
	} {
		if err := ValidateDialIP(net.ParseIP(raw)); err == nil {
			t.Fatalf("expected block for %s", raw)
		}
	}
	if err := ValidateDialIP(net.ParseIP("1.1.1.1")); err != nil {
		t.Fatalf("public IP blocked: %v", err)
	}
}

func TestValidateDialIPBlocksNAT64(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"64:ff9b::a00:1",
		"64:ff9b:1::a00:1",
	} {
		if err := ValidateDialIP(net.ParseIP(raw)); err == nil {
			t.Fatalf("NAT64 address %s was not blocked", raw)
		}
	}
}

func TestPolicyDefaultDenyAndAllowlist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	deny := Policy{Resolver: stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}}
	d := deny.Decide(ctx, Target{Host: "example.com", Port: 443})
	if d.Allow {
		t.Fatalf("empty allowlist must deny")
	}

	allow := StaticAllowlist("example.com", "*.github.com")
	allow.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	if !allow.Decide(ctx, Target{Host: "example.com", Port: 443}).Allow {
		t.Fatal("exact host should allow")
	}
	if !allow.Decide(ctx, Target{Host: "api.github.com", Port: 443}).Allow {
		t.Fatal("wildcard host should allow")
	}
	if allow.Decide(ctx, Target{Host: "github.com", Port: 443}).Allow {
		t.Fatal("wildcard host must not allow the apex")
	}
	if allow.Decide(ctx, Target{Host: "evil.com", Port: 443}).Allow {
		t.Fatal("other host should deny")
	}
}

func TestPolicyValidateRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	for _, policy := range []Policy{
		{AllowedHosts: []string{"*.com"}},
		{AllowedHosts: []string{"*example.com"}},
		{AllowedHosts: []string{""}},
		{AllowedHosts: []string{"example.com"}, AllowedPorts: []int{-1}},
		{AllowedHosts: []string{"example.com"}, AllowedPorts: []int{65536}},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate(%#v) succeeded, want error", policy)
		}
	}
	if err := StaticAllowlist("example.com", "*.example.com").Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
}

func TestPolicyDefaultPortsAreHTTPOnly(t *testing.T) {
	t.Parallel()
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	if !policy.Decide(context.Background(), Target{Host: "example.com", Port: 80}).Allow {
		t.Fatal("default policy should allow port 80")
	}
	if !policy.Decide(context.Background(), Target{Host: "example.com", Port: 443}).Allow {
		t.Fatal("default policy should allow port 443")
	}
	decision := policy.Decide(context.Background(), Target{Host: "example.com", Port: 22})
	if decision.Allow || !strings.Contains(decision.Reason, "port") {
		t.Fatalf("default policy decision = %#v, want port deny", decision)
	}
}

func TestPolicyBlocksDNSToPrivate(t *testing.T) {
	t.Parallel()
	p := StaticAllowlist("internal.example")
	p.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}}
	d := p.Decide(context.Background(), Target{Host: "internal.example", Port: 443})
	if d.Allow {
		t.Fatalf("private DNS result must deny: %#v", d)
	}
}

func TestAuthorizerHook(t *testing.T) {
	t.Parallel()
	p := StaticAllowlist("example.com")
	p.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	p.Authorizer = AllowFunc(func(ctx context.Context, target Target) error {
		return ErrDenied
	})
	d := p.Decide(context.Background(), Target{Host: "example.com", Port: 443})
	if d.Allow {
		t.Fatal("authorizer deny ignored")
	}
}
