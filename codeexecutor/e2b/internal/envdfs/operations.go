//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdfs

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	filesystem "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdfs/spec"
)

// Stat returns envd's entry metadata, including size, mode, and symlink target.
// Paths are resolved by envd; this client does not enforce workspace containment.
func (c *Client) Stat(ctx context.Context, path string) (*filesystem.EntryInfo, error) {
	if err := c.validate(ctx, path); err != nil {
		return nil, err
	}
	req := connect.NewRequest(&filesystem.StatRequest{Path: path})
	c.addHeaders(req.Header())
	resp, err := c.rpc.Stat(ctx, req)
	if err != nil {
		return nil, operationError(ctx, "stat", err)
	}
	if resp.Msg.Entry == nil {
		return nil, errors.New("envd filesystem: stat returned no entry")
	}
	return resp.Msg.Entry, nil
}

// MakeDir creates a directory and missing parents using envd's user and modes.
// It returns envd errors unchanged through wrapping, including AlreadyExists.
func (c *Client) MakeDir(ctx context.Context, path string) error {
	if err := c.validate(ctx, path); err != nil {
		return err
	}
	req := connect.NewRequest(&filesystem.MakeDirRequest{Path: path})
	c.addHeaders(req.Header())
	if _, err := c.rpc.MakeDir(ctx, req); err != nil {
		return operationError(ctx, "make directory", err)
	}
	return nil
}

// Remove removes a file or directory tree. Missing paths succeed on the pinned
// envd implementation. No workspace containment or root-path guard is applied.
func (c *Client) Remove(ctx context.Context, path string) error {
	if err := c.validate(ctx, path); err != nil {
		return err
	}
	req := connect.NewRequest(&filesystem.RemoveRequest{Path: path})
	c.addHeaders(req.Header())
	if _, err := c.rpc.Remove(ctx, req); err != nil {
		return operationError(ctx, "remove", err)
	}
	return nil
}

// Move renames source to destination using envd's rename semantics, creating
// destination parents if necessary. Same-filesystem replacement is atomic on
// the pinned envd implementation. An error or lost response does not imply that
// the rename did not happen. Move never retries or copies across filesystems.
func (c *Client) Move(ctx context.Context, source, destination string) error {
	if err := c.validate(ctx, source, destination); err != nil {
		return err
	}
	req := connect.NewRequest(&filesystem.MoveRequest{Source: source, Destination: destination})
	c.addHeaders(req.Header())
	if _, err := c.rpc.Move(ctx, req); err != nil {
		return operationError(ctx, "move", err)
	}
	return nil
}
