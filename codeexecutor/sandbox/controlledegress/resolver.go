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
	"fmt"
	"net"
	"net/netip"
)

var blockedPrefixes = mustParsePrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"198.18.0.0/15",
	"192.0.2.0/24",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"fec0::/10",
)

// Resolver looks up host addresses. Implementations must support concurrent
// calls and return promptly when ctx is canceled. Proxy.Close cancels the
// context and waits for in-flight lookups to return. Tests may inject fakes.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type defaultResolver struct{}

func (defaultResolver) LookupIPAddr(
	ctx context.Context,
	host string,
) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func resolveAndValidate(
	ctx context.Context,
	resolver Resolver,
	host string,
) ([]net.IP, error) {
	if resolver == nil {
		resolver = defaultResolver{}
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := ValidateDialIP(ip); err != nil {
			return nil, err
		}
		return []net.IP{ip}, nil
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("dns lookup %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("dns lookup %s: no addresses", host)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if err := ValidateDialIP(addr.IP); err != nil {
			return nil, fmt.Errorf("dns %s: %w", host, err)
		}
		ips = append(ips, addr.IP)
	}
	return ips, nil
}

func mustParsePrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			panic(err)
		}
		out = append(out, prefix)
	}
	return out
}

// ValidateDialIP rejects private, loopback, link-local, CGNAT, and TEST-NET
// addresses (including IPv4-mapped IPv6 after unmapping).
func ValidateDialIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("nil IP")
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return fmt.Errorf("invalid IP %v", ip)
	}
	addr = addr.Unmap()
	if !addr.IsValid() ||
		addr.IsUnspecified() ||
		addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() {
		return fmt.Errorf("blocked address %s", addr)
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return fmt.Errorf("blocked address %s", addr)
		}
	}
	return nil
}
