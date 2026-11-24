//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"
)

const (
	authRequiredErrorCode = -32000
	authSingleflightKey   = "authenticate"
)

var (
	errNoAdvertisedAuthMethods = errors.New("acpagent: authentication required without supported methods")
	errAuthMethodRequired      = errors.New("acpagent: authentication method ID is required")
	errAuthMethodNotFound      = errors.New("acpagent: authentication method is not advertised")
	errTerminalAuthPending     = errors.New("acpagent: terminal authentication is not implemented")
	errSessionAuthOperation    = errors.New("acpagent: unsupported session authentication operation")
)

type sessionAcquisitionOperation string

const (
	sessionAcquisitionNew    sessionAcquisitionOperation = "session/new"
	sessionAcquisitionLoad   sessionAcquisitionOperation = "session/load"
	sessionAcquisitionResume sessionAcquisitionOperation = "session/resume"
)

func (m *connectionManager) withSessionAuthentication(
	ctx context.Context,
	process *processConnection,
	operation sessionAcquisitionOperation,
	run func(context.Context) error,
) error {
	if !operation.valid() {
		return errSessionAuthOperation
	}
	if process == nil {
		return errProcessDone
	}
	if run == nil {
		return errors.New("acpagent: session acquisition operation is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	err := run(ctx)
	if err == nil {
		return nil
	}
	required, ok := authenticationRequiredError(
		string(operation),
		process.agentDescriptor.authMethods,
		err,
	)
	if !ok {
		return err
	}
	if len(required.Methods) == 0 {
		return errors.Join(required, errNoAdvertisedAuthMethods)
	}
	if m.authSelector == nil {
		return required
	}
	challenge := AuthChallenge{
		Operation: required.Operation,
		Methods:   append([]AuthMethod(nil), required.Methods...),
	}
	if err := m.authenticate(ctx, process, challenge); err != nil {
		return err
	}

	if err := run(ctx); err != nil {
		if retryRequired, ok := authenticationRequiredError(
			string(operation),
			process.agentDescriptor.authMethods,
			err,
		); ok {
			return retryRequired
		}
		return err
	}
	return nil
}

func (m *connectionManager) authenticate(
	ctx context.Context,
	process *processConnection,
	challenge AuthChallenge,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result := process.authGroup.DoChan(authSingleflightKey, func() (any, error) {
		return nil, m.performAgentAuthentication(ctx, process, challenge)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-result:
		return result.Err
	}
}

func (m *connectionManager) performAgentAuthentication(
	ctx context.Context,
	process *processConnection,
	challenge AuthChallenge,
) error {
	selection, err := m.authSelector.SelectAuthMethod(ctx, challenge)
	if err != nil {
		return fmt.Errorf("acpagent: select authentication method: %w", err)
	}
	if selection.MethodID == "" {
		return errAuthMethodRequired
	}
	method, ok := findAuthMethod(challenge.Methods, selection.MethodID)
	if !ok {
		return fmt.Errorf("%w: %q", errAuthMethodNotFound, selection.MethodID)
	}
	switch method.Type {
	case AuthMethodTypeAgent:
		_, err := process.connection.Authenticate(ctx, acpsdk.AuthenticateRequest{
			MethodId: acpsdk.AuthMethodId(selection.MethodID),
			Meta:     selection.Meta,
		})
		if err != nil {
			return fmt.Errorf("acpagent: authenticate: %w", err)
		}
		return nil
	case AuthMethodTypeTerminal:
		return errTerminalAuthPending
	default:
		return fmt.Errorf("acpagent: unsupported authentication method type %q", method.Type)
	}
}

func authenticationRequiredError(
	operation string,
	rawMethods []acpsdk.AuthMethod,
	err error,
) (*AuthRequiredError, bool) {
	var requestErr *acpsdk.RequestError
	if !errors.As(err, &requestErr) || requestErr.Code != authRequiredErrorCode {
		return nil, false
	}
	return &AuthRequiredError{
		Operation: operation,
		Methods:   advertisedAuthMethods(rawMethods),
		Data:      requestErr.Data,
		Err:       err,
	}, true
}

func advertisedAuthMethods(rawMethods []acpsdk.AuthMethod) []AuthMethod {
	methods := make([]AuthMethod, 0, len(rawMethods))
	for _, raw := range rawMethods {
		switch {
		case raw.Agent != nil:
			methods = append(methods, AuthMethod{
				ID:          string(raw.Agent.Id),
				Name:        raw.Agent.Name,
				Description: stringValue(raw.Agent.Description),
				Type:        AuthMethodTypeAgent,
			})
		case raw.Terminal != nil:
			methods = append(methods, AuthMethod{
				ID:          string(raw.Terminal.Id),
				Name:        raw.Terminal.Name,
				Description: stringValue(raw.Terminal.Description),
				Type:        AuthMethodTypeTerminal,
			})
		}
	}
	return methods
}

func findAuthMethod(methods []AuthMethod, id string) (AuthMethod, bool) {
	for _, method := range methods {
		if method.ID == id {
			return method, true
		}
	}
	return AuthMethod{}, false
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (o sessionAcquisitionOperation) valid() bool {
	switch o {
	case sessionAcquisitionNew, sessionAcquisitionLoad, sessionAcquisitionResume:
		return true
	default:
		return false
	}
}
