package main

import (
	"context"
	"errors"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

type readSpecArgs struct {
}

type readSpecResult struct {
	Spec string `json:"spec"`
}

func readSpecFile(ctx context.Context, args readSpecArgs) (readSpecResult, error) {
	log.Infof("reading spec file")
	spec, err := os.ReadFile("./spec.txt")
	if err != nil {
		log.Errorf("failed to read spec file: %v", err)
		return readSpecResult{}, errors.New("failed to read spec file")
	}
	return readSpecResult{
		Spec: string(spec),
	}, nil
}
