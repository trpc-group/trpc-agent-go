//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// NewPasswordGeneratorTool creates a password generator tool.
func NewPasswordGeneratorTool() tool.CallableTool {
	return function.NewFunctionTool(
		generatePassword,
		function.WithName("password_generator"),
		function.WithDescription("Generate secure random passwords with customizable options. Can include uppercase, lowercase, numbers, and symbols. Password length between 8-64 characters."),
		function.WithInputSchema(&tool.Schema{
			Type:        "object",
			Description: "Password generation request",
			Required:    []string{"length"},
			Properties: map[string]*tool.Schema{
				"length": {
					Type:        "integer",
					Description: "Length of password to generate (8-64)",
				},
				"include_numbers": {
					Type:        "boolean",
					Description: "Include numbers in password",
					Default:     true,
				},
				"include_symbols": {
					Type:        "boolean",
					Description: "Include symbols in password",
					Default:     true,
				},
				"include_uppercase": {
					Type:        "boolean",
					Description: "Include uppercase letters",
					Default:     true,
				},
				"include_lowercase": {
					Type:        "boolean",
					Description: "Include lowercase letters",
					Default:     true,
				},
			},
		}),
	)
}

type passwordRequest struct {
	Length           int  `json:"length"`
	IncludeNumbers   bool `json:"include_numbers"`
	IncludeSymbols   bool `json:"include_symbols"`
	IncludeUppercase bool `json:"include_uppercase"`
	IncludeLowercase bool `json:"include_lowercase"`
}

type passwordResponse struct {
	Password string `json:"password"`
	Length   int    `json:"length"`
	Strength string `json:"strength"`
	Message  string `json:"message"`
}

func generatePassword(_ context.Context, req passwordRequest) (passwordResponse, error) {
	if req.Length < 8 {
		req.Length = 8
	}
	if req.Length > 64 {
		req.Length = 64
	}

	var chars string
	if req.IncludeUppercase {
		chars += "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	}
	if req.IncludeLowercase {
		chars += "abcdefghijklmnopqrstuvwxyz"
	}
	if req.IncludeNumbers {
		chars += "0123456789"
	}
	if req.IncludeSymbols {
		chars += "!@#$%^&*()_+-=[]{}|;:,.<>?"
	}

	if chars == "" {
		return passwordResponse{
			Password: "",
			Length:   0,
			Strength: "",
			Message:  "Error: At least one character type must be selected",
		}, fmt.Errorf("no character types selected")
	}

	rand.Seed(time.Now().UnixNano())
	password := make([]byte, req.Length)
	for i := range password {
		password[i] = chars[rand.Intn(len(chars))]
	}

	strength := "Weak"
	if req.Length >= 12 && req.IncludeNumbers && req.IncludeSymbols && req.IncludeUppercase && req.IncludeLowercase {
		strength = "Strong"
	} else if req.Length >= 8 && (req.IncludeNumbers || req.IncludeSymbols) {
		strength = "Medium"
	}

	return passwordResponse{
		Password: string(password),
		Length:   req.Length,
		Strength: strength,
		Message:  fmt.Sprintf("Generated %s password with length %d", strength, req.Length),
	}, nil
}
