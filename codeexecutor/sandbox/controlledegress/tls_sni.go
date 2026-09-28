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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxTLSClientHelloBytes = 128 << 10

var errTLSClientHelloNoSNI = errors.New("TLS ClientHello has no SNI")

func readTLSClientHelloSNI(r io.Reader) (string, []byte, error) {
	var raw []byte
	var handshake []byte
	for len(raw) < maxTLSClientHelloBytes {
		header := make([]byte, 5)
		if _, err := io.ReadFull(r, header); err != nil {
			return "", raw, fmt.Errorf("read TLS record header: %w", err)
		}
		if header[0] != 22 {
			return "", raw, fmt.Errorf("CONNECT payload is not a TLS handshake")
		}
		recordLen := int(binary.BigEndian.Uint16(header[3:5]))
		if recordLen <= 0 || len(raw)+5+recordLen > maxTLSClientHelloBytes {
			return "", raw, fmt.Errorf("TLS ClientHello exceeds %d bytes", maxTLSClientHelloBytes)
		}
		payload := make([]byte, recordLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return "", raw, fmt.Errorf("read TLS record: %w", err)
		}
		raw = append(raw, header...)
		raw = append(raw, payload...)
		handshake = append(handshake, payload...)
		if len(handshake) < 4 {
			continue
		}
		if handshake[0] != 1 {
			return "", raw, fmt.Errorf("first TLS handshake message is not ClientHello")
		}
		helloLen := int(handshake[1])<<16 |
			int(handshake[2])<<8 |
			int(handshake[3])
		if helloLen+4 > maxTLSClientHelloBytes {
			return "", raw, fmt.Errorf("TLS ClientHello exceeds %d bytes", maxTLSClientHelloBytes)
		}
		if len(handshake) < helloLen+4 {
			continue
		}
		sni, err := parseClientHelloSNI(handshake[4 : helloLen+4])
		return sni, raw, err
	}
	return "", raw, fmt.Errorf("TLS ClientHello exceeds %d bytes", maxTLSClientHelloBytes)
}

func parseClientHelloSNI(hello []byte) (string, error) {
	// legacy_version + random
	if len(hello) < 34 {
		return "", fmt.Errorf("truncated TLS ClientHello")
	}
	pos := 34
	if pos >= len(hello) {
		return "", fmt.Errorf("truncated TLS session id")
	}
	sessionLen := int(hello[pos])
	pos++
	if pos+sessionLen+2 > len(hello) {
		return "", fmt.Errorf("truncated TLS session id")
	}
	pos += sessionLen
	cipherLen := int(binary.BigEndian.Uint16(hello[pos : pos+2]))
	pos += 2
	if cipherLen == 0 || pos+cipherLen+1 > len(hello) {
		return "", fmt.Errorf("truncated TLS cipher suites")
	}
	pos += cipherLen
	compressionLen := int(hello[pos])
	pos++
	if pos+compressionLen > len(hello) {
		return "", fmt.Errorf("truncated TLS compression methods")
	}
	pos += compressionLen
	if pos == len(hello) {
		return "", errTLSClientHelloNoSNI
	}
	if pos+2 > len(hello) {
		return "", fmt.Errorf("truncated TLS extensions")
	}
	extensionsLen := int(binary.BigEndian.Uint16(hello[pos : pos+2]))
	pos += 2
	if pos+extensionsLen > len(hello) {
		return "", fmt.Errorf("truncated TLS extensions")
	}
	end := pos + extensionsLen
	for pos+4 <= end {
		extensionType := binary.BigEndian.Uint16(hello[pos : pos+2])
		extensionLen := int(binary.BigEndian.Uint16(hello[pos+2 : pos+4]))
		pos += 4
		if pos+extensionLen > end {
			return "", fmt.Errorf("truncated TLS extension")
		}
		if extensionType == 0 {
			return parseServerNameExtension(hello[pos : pos+extensionLen])
		}
		pos += extensionLen
	}
	return "", errTLSClientHelloNoSNI
}

func parseServerNameExtension(extension []byte) (string, error) {
	if len(extension) < 2 {
		return "", fmt.Errorf("truncated TLS server name extension")
	}
	listLen := int(binary.BigEndian.Uint16(extension[:2]))
	if listLen+2 > len(extension) {
		return "", fmt.Errorf("truncated TLS server name list")
	}
	pos := 2
	end := pos + listLen
	for pos+3 <= end {
		nameType := extension[pos]
		nameLen := int(binary.BigEndian.Uint16(extension[pos+1 : pos+3]))
		pos += 3
		if pos+nameLen > end {
			return "", fmt.Errorf("truncated TLS server name")
		}
		if nameType == 0 {
			if nameLen == 0 {
				return "", fmt.Errorf("empty TLS server name")
			}
			return strings.ToLower(string(extension[pos : pos+nameLen])), nil
		}
		pos += nameLen
	}
	return "", errTLSClientHelloNoSNI
}
