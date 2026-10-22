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
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestReadTLSClientHelloSNIRejectsMalformedRecords(t *testing.T) {
	oversizedHello := []byte{22, 3, 3, 0, 4, 1, 0xff, 0xff, 0xff}
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "missing header"},
		{name: "not handshake", raw: []byte{23, 3, 3, 0, 1, 0}},
		{name: "empty record", raw: []byte{22, 3, 3, 0, 0}},
		{name: "truncated record", raw: []byte{22, 3, 3, 0, 2, 1}},
		{name: "truncated handshake header", raw: []byte{22, 3, 3, 0, 1, 1}},
		{name: "wrong handshake type", raw: []byte{22, 3, 3, 0, 4, 2, 0, 0, 0}},
		{name: "oversized hello", raw: oversizedHello},
		{name: "truncated hello", raw: []byte{22, 3, 3, 0, 4, 1, 0, 0, 8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := readTLSClientHelloSNI(bytes.NewReader(tt.raw)); err == nil {
				t.Fatal("malformed TLS record was accepted")
			}
		})
	}
}

func TestParseClientHelloSNIRejectsMalformedBodies(t *testing.T) {
	base := minimalClientHelloBody(nil)
	tests := []struct {
		name  string
		hello []byte
	}{
		{name: "short fixed fields", hello: make([]byte, 33)},
		{name: "missing session length", hello: make([]byte, 34)},
		{name: "session overflow", hello: append(make([]byte, 34), 10)},
		{name: "zero ciphers", hello: append(make([]byte, 35), 0, 0, 0)},
		{name: "compression overflow", hello: append(append(make([]byte, 35), 0, 2, 0x13, 0x01), 2, 0)},
		{name: "no extensions", hello: minimalClientHelloBody(nil)[:41]},
		{name: "truncated extension length", hello: append(minimalClientHelloBody(nil)[:41], 0)},
		{name: "extensions overflow", hello: append(minimalClientHelloBody(nil)[:41], 0, 8, 0)},
		{name: "truncated extension", hello: minimalClientHelloBody([]byte{0, 0, 0, 4, 0})},
		{name: "no sni", hello: minimalClientHelloBody([]byte{0, 1, 0, 0})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseClientHelloSNI(tt.hello); err == nil {
				t.Fatalf("malformed ClientHello accepted: %x (base=%d)", tt.hello, len(base))
			}
		})
	}
}

func TestParseServerNameExtensionValidation(t *testing.T) {
	validName := "example.com"
	valid := make([]byte, 2+3+len(validName))
	binary.BigEndian.PutUint16(valid[:2], uint16(3+len(validName)))
	valid[2] = 0
	binary.BigEndian.PutUint16(valid[3:5], uint16(len(validName)))
	copy(valid[5:], validName)
	if got, err := parseServerNameExtension(valid); err != nil || got != validName {
		t.Fatalf("valid SNI got=%q err=%v", got, err)
	}

	tests := [][]byte{
		nil,
		{0, 4, 0},
		{0, 3, 0, 0, 2, 'x'},
		{0, 3, 0, 0, 0},
		{0, 3, 1, 0, 0},
	}
	for _, extension := range tests {
		if _, err := parseServerNameExtension(extension); err == nil {
			t.Fatalf("malformed SNI extension accepted: %x", extension)
		}
	}

	upper := append([]byte(nil), valid...)
	copy(upper[5:], strings.ToUpper(validName))
	if got, err := parseServerNameExtension(upper); err != nil || got != validName {
		t.Fatalf("uppercase SNI got=%q err=%v", got, err)
	}
}

func minimalClientHelloBody(extensions []byte) []byte {
	hello := make([]byte, 34)
	hello = append(hello, 0)
	hello = append(hello, 0, 2, 0x13, 0x01)
	hello = append(hello, 1, 0)
	if extensions == nil {
		return hello
	}
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(extensions)))
	hello = append(hello, length[:]...)
	return append(hello, extensions...)
}
