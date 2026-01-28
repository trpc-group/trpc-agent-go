#!/bin/bash
#
# Download evaluation datasets for session summary testing.
#
# MT-Bench-101 is a multi-turn dialogue benchmark with user-assistant pairs,
# ideal for evaluating session summary effectiveness.
#
# Reference: MT-Bench-101: A Fine-Grained Benchmark for Evaluating Large
# Language Models in Multi-Turn Dialogues (ACL 2024).
#

set -e

# Default to current directory (benchmark/summary/data/).
DATA_DIR="${1:-.}"
mkdir -p "$DATA_DIR"

echo "=== Downloading MT-Bench-101 dataset ==="
echo "Data directory: $DATA_DIR"
echo

MTBENCH_DIR="$DATA_DIR/mt-bench-101"
if [ -d "$MTBENCH_DIR" ] && [ -f "$MTBENCH_DIR/subjective/mtbench101.jsonl" ]; then
    echo "MT-Bench-101 already exists, skipping..."
else
    mkdir -p "$MTBENCH_DIR"
    echo "Cloning MT-Bench-101 repository..."
    # Clone the repository (sparse checkout for data only).
    git clone --depth 1 --filter=blob:none --sparse \
        https://github.com/mtbench101/mt-bench-101.git "$MTBENCH_DIR/repo" 2>/dev/null || true
    cd "$MTBENCH_DIR/repo"
    git sparse-checkout set data 2>/dev/null || true
    cd -
    # Copy data files.
    if [ -d "$MTBENCH_DIR/repo/data" ]; then
        cp -r "$MTBENCH_DIR/repo/data"/* "$MTBENCH_DIR/"
        rm -rf "$MTBENCH_DIR/repo"
        echo "MT-Bench-101 downloaded successfully."
    else
        echo "Warning: Could not download MT-Bench-101. Please download manually from:"
        echo "  https://github.com/mtbench101/mt-bench-101"
        rm -rf "$MTBENCH_DIR/repo"
    fi
fi
echo

# Show dataset info.
if [ -f "$MTBENCH_DIR/subjective/mtbench101.jsonl" ]; then
    CASE_COUNT=$(wc -l < "$MTBENCH_DIR/subjective/mtbench101.jsonl")
    echo "=== Dataset Info ==="
    echo "Location: $MTBENCH_DIR"
    echo "Test cases: $CASE_COUNT"
    echo
fi

echo "=== Usage ==="
echo "cd benchmark/summary/trpc-agent-go-impl"
echo "go run . -num-cases 10"
echo "go run . -num-cases 10 -verbose"
