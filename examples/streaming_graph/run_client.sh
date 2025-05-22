#!/bin/bash

# Set server URL (if provided)
export SERVER_URL=${SERVER_URL:-"http://localhost:8080"}

# Build the client
echo "Building client..."
cd cmd/client
go build -o client .

# Run the client
echo "Starting client..."
echo "Connected to: $SERVER_URL"
echo "Type 'exit' or 'quit' to end the session"
echo "Type 'stream' to enable streaming mode or 'nostream' to disable it"
./client 