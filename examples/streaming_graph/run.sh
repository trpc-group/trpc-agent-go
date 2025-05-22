#!/bin/bash

# Set environment variables
export OPENAI_API_KEY=${OPENAI_API_KEY:-"your_api_key_here"}
export OPENAI_MODEL_NAME=${OPENAI_MODEL_NAME:-"gpt-4o"}
export OPENAI_BASE_URL=${OPENAI_BASE_URL:-"https://api.openai.com/v1"}
export API_TIMEOUT_SECONDS=${API_TIMEOUT_SECONDS:-120}
export PORT=${PORT:-8080}

# Check if API key is set
if [ "$OPENAI_API_KEY" = "your_api_key_here" ]; then
  echo "Please set your OPENAI_API_KEY environment variable"
  exit 1
fi

# Build the server
echo "Building server..."
go build -o server .

# Build the client
echo "Building client..."
cd cmd/client
go build -o client .
cd ../..

# Run the server
echo "Starting server on port $PORT..."
echo "OpenAI Model: $OPENAI_MODEL_NAME"
echo "API Timeout: ${API_TIMEOUT_SECONDS}s"
echo "Press Ctrl+C to stop"
./server 