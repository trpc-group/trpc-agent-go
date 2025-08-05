package main

import (
	"context"
	"flag"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

func main() {
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	flag.Parse()
	a := newLogQueryAgent("log_app", "log_agent", *modelName)
	userMessage := []string{
		"Calculate 123 + 456 * 789",
		"What day of the week is today?",
		"'Hello World' to uppercase",
		"Create a test file in the current directory",
		"Find information about Tesla company",
	}

	for _, msg := range userMessage {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			err := a.processMessage(ctx, msg)
			if err != nil {
				log.Errorf("Chat system failed to run: %v", err)
			}
		}()
	}
}
