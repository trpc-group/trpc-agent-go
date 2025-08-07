package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/filetoolset/project/counter"
)

func main() {
	content, err := os.ReadFile("input.txt")
	if err != nil {
		log.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		log.Fatal(err)
	}
	counter := counter.GetCounter(n)
	os.WriteFile("output.txt", []byte(strconv.Itoa(counter)), 0644)
}
