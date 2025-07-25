package main

import (
	"os"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

type readSpecArgs struct {
}

type readSpecResult struct {
	Spec string `json:"spec"`
}

func readSpecFile(args readSpecArgs) readSpecResult {
	log.Infof("reading spec file")
	spec, err := os.ReadFile("./spec.txt")
	if err != nil {
		log.Errorf("failed to read spec file: %v", err)
		return readSpecResult{}
	}
	return readSpecResult{
		Spec: string(spec),
	}
}
