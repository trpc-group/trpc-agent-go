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
	debugContext(
		ctx,
		"acpagent: authentication required",
		append(
			append(
				processLogFields(process),
				"operation", string(operation),
				"method_count", len(required.Methods),
			),
			authMethodLogFields(required.Methods)...,
		)...,
	)
	if len(required.Methods) == 0 {
		warnContext(
			ctx,
			"acpagent: authentication required without advertised methods",
			append(processLogFields(process), "operation", string(operation))...,
		)
		return errors.Join(required, errNoAdvertisedAuthMethods)
	}
	if m.authSelector == nil {
		warnContext(
			ctx,
			"acpagent: authentication required without a selector",
			append(processLogFields(process), "operation", string(operation))...,
		)
		return required
	}
	challenge := AuthChallenge{
		Operation: required.Operation,
		Methods:   append([]AuthMethod(nil), required.Methods...),
	}
	if err := m.authenticate(ctx, process, challenge); err != nil {
		return err
	}

	debugContext(
		ctx,
		"acpagent: retrying operation after authentication",
		append(processLogFields(process), "operation", string(operation))...,
	)
	if err := run(ctx); err != nil {
		if retryRequired, ok := authenticationRequiredError(
			string(operation),
			process.agentDescriptor.authMethods,
			err,
		); ok {
			warnContext(
				ctx,
				"acpagent: operation still requires authentication after retry",
				append(processLogFields(process), "operation", string(operation), "error", err)...,
			)
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
	authCtx, cancel := mergeContexts(ctx, process.ctx)
	defer cancel()
	if err := authCtx.Err(); err != nil {
		return err
	}
	result := process.authGroup.DoChan(authSingleflightKey, func() (any, error) {
		return nil, m.performAgentAuthentication(authCtx, process, challenge)
	})
	select {
	case <-authCtx.Done():
		return authCtx.Err()
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
		err = fmt.Errorf("acpagent: select authentication method: %w", err)
		errorContext(
			ctx,
			"acpagent: select authentication method failed",
			append(processLogFields(process), "operation", challenge.Operation, "error", err)...,
		)
		return err
	}
	if selection.MethodID == "" {
		warnContext(
			ctx,
			"acpagent: authentication selector returned an empty method id",
			append(processLogFields(process), "operation", challenge.Operation)...,
		)
		return errAuthMethodRequired
	}
	method, ok := findAuthMethod(challenge.Methods, selection.MethodID)
	if !ok {
		err = fmt.Errorf("%w: %q", errAuthMethodNotFound, selection.MethodID)
		warnContext(
			ctx,
			"acpagent: selected authentication method was not advertised",
			append(
				processLogFields(process),
				"operation", challenge.Operation,
				"method_id", selection.MethodID,
				"error", err,
			)...,
		)
		return err
	}
	infoContext(
		ctx,
		"acpagent: selected authentication method",
		append(
			processLogFields(process),
			"operation", challenge.Operation,
			"method_id", method.ID,
			"method_type", method.Type,
		)...,
	)
	switch method.Type {
	case AuthMethodTypeAgent:
		_, err := process.connection.Authenticate(ctx, acpsdk.AuthenticateRequest{
			MethodId: acpsdk.AuthMethodId(selection.MethodID),
			Meta:     selection.Meta,
		})
		if err != nil {
			err = fmt.Errorf("acpagent: authenticate: %w", err)
			errorContext(
				ctx,
				"acpagent: agent authentication request failed",
				append(
					processLogFields(process),
					"operation", challenge.Operation,
					"method_id", method.ID,
					"error", err,
				)...,
			)
			return err
		}
		infoContext(
			ctx,
			"acpagent: agent authentication completed",
			append(
				processLogFields(process),
				"operation", challenge.Operation,
				"method_id", method.ID,
			)...,
		)
		return nil
	case AuthMethodTypeTerminal:
		warnContext(
			ctx,
			"acpagent: terminal authentication is not implemented",
			append(
				processLogFields(process),
				"operation", challenge.Operation,
				"method_id", method.ID,
			)...,
		)
		return errTerminalAuthPending
	default:
		err := fmt.Errorf("acpagent: unsupported authentication method type %q", method.Type)
		warnContext(
			ctx,
			"acpagent: unsupported authentication method type",
			append(
				processLogFields(process),
				"operation", challenge.Operation,
				"method_id", method.ID,
				"method_type", method.Type,
				"error", err,
			)...,
		)
		return err
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
