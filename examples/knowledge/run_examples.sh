#!/bin/bash
#
# Developer script for batch running knowledge examples.
# This script is intended for developers to test examples across different vector stores.
# Regular users can ignore this script and run examples directly.
#
# Usage:
#   ./run_examples.sh [OPTIONS]
#
# Options:
#   -v, --vectorstore TYPE   Vector store type: inmemory|pgvector|tcvector|elasticsearch|milvus (default: inmemory)
#   -o, --output DIR         Output directory for results (default: ./output)
#   -e, --examples LIST      Comma-separated list of examples to run (default: all)
#                            Available examples:
#                              - basic
#                              - features/agentic-filter
#                              - features/metadata-filter
#                              - features/management
#                              - sources/auto-source
#                              - sources/directory-source
#                              - sources/file-source
#                              - sources/url-source
#                            Example: -e "basic,features/management"
#   -r, --randomtable        Generate random collection/table name for each run
#   -a, --all-stores         Run with all vector stores (inmemory, pgvector, tcvector, elasticsearch)
#   -h, --help               Show this help message
#
# Environment Variables (auto-detected):
#   TCVector:      TCVECTOR_URL, TCVECTOR_USERNAME, TCVECTOR_PASSWORD, TCVECTOR_COLLECTION
#   PGVector:      PGVECTOR_HOST, PGVECTOR_PORT, PGVECTOR_USER, PGVECTOR_PASSWORD, PGVECTOR_DATABASE, PGVECTOR_TABLE
#   Elasticsearch: ELASTICSEARCH_HOSTS, ELASTICSEARCH_USERNAME, ELASTICSEARCH_PASSWORD, ELASTICSEARCH_INDEX_NAME
#   Milvus:        MILVUS_ADDRESS, MILVUS_USERNAME, MILVUS_PASSWORD, MILVUS_DB_NAME, MILVUS_COLLECTION
#   OpenAI:        OPENAI_API_KEY, OPENAI_BASE_URL, MODEL_NAME
#

# Don't use set -e as it causes issues with arithmetic operations returning 0

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
VECTORSTORE="inmemory"
OUTPUT_DIR="./output"
EXAMPLES=""
RANDOMTABLE=false
ALL_STORES=false
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# All available vector stores
ALL_VECTORSTORES=("inmemory" "tcvector" "pgvector" "elasticsearch" "milvus")

# Generate random table/collection name
generate_random_name() {
    local prefix=$1
    local random_suffix=$(date +%s%N | md5sum | head -c 8)
    echo "${prefix}_${random_suffix}"
}

# Available examples (excluding OCR which requires special build tag)
BASIC_EXAMPLES=(
    "basic"
)

FEATURE_EXAMPLES=(
    "features/agentic-filter"
    "features/metadata-filter"
    "features/management"
)

SOURCE_EXAMPLES=(
    "sources/auto-source"
    "sources/directory-source"
    "sources/file-source"
    "sources/url-source"
)

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--vectorstore)
            VECTORSTORE="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -e|--examples)
            EXAMPLES="$2"
            shift 2
            ;;
        -r|--randomtable)
            RANDOMTABLE=true
            shift
            ;;
        -a|--all-stores)
            ALL_STORES=true
            shift
            ;;
        -h|--help)
            head -25 "$0" | tail -20
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

# Validate vector store type
case $VECTORSTORE in
    inmemory|pgvector|tcvector|elasticsearch|milvus)
        ;;
    *)
        echo -e "${RED}Invalid vector store type: $VECTORSTORE${NC}"
        echo "Valid types: inmemory, pgvector, tcvector, elasticsearch, milvus"
        exit 1
        ;;
esac

# Print environment status
print_env_status() {
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}Environment Configuration${NC}"
    echo -e "${BLUE}========================================${NC}"
    
    # OpenAI
    if [[ -n "$OPENAI_API_KEY" ]]; then
        echo -e "OpenAI API Key: ${GREEN}configured${NC}"
    else
        echo -e "OpenAI API Key: ${RED}not set${NC}"
    fi
    
    if [[ -n "$OPENAI_BASE_URL" ]]; then
        echo -e "OpenAI Base URL: ${GREEN}$OPENAI_BASE_URL${NC}"
    else
        echo -e "OpenAI Base URL: ${YELLOW}default${NC}"
    fi
    
    # TCVector
    echo ""
    echo "TCVector:"
    if [[ -n "$TCVECTOR_URL" ]]; then
        echo -e "  URL: ${GREEN}$TCVECTOR_URL${NC}"
    else
        echo -e "  URL: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$TCVECTOR_USERNAME" ]]; then
        echo -e "  Username: ${GREEN}configured${NC}"
    else
        echo -e "  Username: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$TCVECTOR_PASSWORD" ]]; then
        echo -e "  Password: ${GREEN}configured${NC}"
    else
        echo -e "  Password: ${YELLOW}not set${NC}"
    fi
    
    # PGVector
    echo ""
    echo "PGVector:"
    if [[ -n "$PGVECTOR_HOST" ]]; then
        echo -e "  Host: ${GREEN}$PGVECTOR_HOST${NC}"
    else
        echo -e "  Host: ${YELLOW}default (127.0.0.1)${NC}"
    fi
    if [[ -n "$PGVECTOR_PORT" ]]; then
        echo -e "  Port: ${GREEN}$PGVECTOR_PORT${NC}"
    else
        echo -e "  Port: ${YELLOW}default (5432)${NC}"
    fi
    if [[ -n "$PGVECTOR_USER" ]]; then
        echo -e "  User: ${GREEN}$PGVECTOR_USER${NC}"
    else
        echo -e "  User: ${YELLOW}default (root)${NC}"
    fi
    if [[ -n "$PGVECTOR_PASSWORD" ]]; then
        echo -e "  Password: ${GREEN}configured${NC}"
    else
        echo -e "  Password: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$PGVECTOR_DATABASE" ]]; then
        echo -e "  Database: ${GREEN}$PGVECTOR_DATABASE${NC}"
    else
        echo -e "  Database: ${YELLOW}default (vectordb)${NC}"
    fi
    if [[ -n "$PGVECTOR_TABLE" ]]; then
        echo -e "  Table: ${GREEN}$PGVECTOR_TABLE${NC}"
    else
        echo -e "  Table: ${YELLOW}default (trpc-agent-go)${NC}"
    fi
    
    # Elasticsearch
    echo ""
    echo "Elasticsearch:"
    if [[ -n "$ELASTICSEARCH_HOSTS" ]]; then
        echo -e "  Hosts: ${GREEN}$ELASTICSEARCH_HOSTS${NC}"
    else
        echo -e "  Hosts: ${YELLOW}default (http://localhost:9200)${NC}"
    fi
    if [[ -n "$ELASTICSEARCH_USERNAME" ]]; then
        echo -e "  Username: ${GREEN}configured${NC}"
    else
        echo -e "  Username: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$ELASTICSEARCH_PASSWORD" ]]; then
        echo -e "  Password: ${GREEN}configured${NC}"
    else
        echo -e "  Password: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$ELASTICSEARCH_INDEX_NAME" ]]; then
        echo -e "  Index: ${GREEN}$ELASTICSEARCH_INDEX_NAME${NC}"
    else
        echo -e "  Index: ${YELLOW}default (trpc_agent_go)${NC}"
    fi
    
    # Milvus
    echo ""
    echo "Milvus:"
    if [[ -n "$MILVUS_ADDRESS" ]]; then
        echo -e "  Address: ${GREEN}$MILVUS_ADDRESS${NC}"
    else
        echo -e "  Address: ${YELLOW}default (localhost:19530)${NC}"
    fi
    if [[ -n "$MILVUS_USERNAME" ]]; then
        echo -e "  Username: ${GREEN}configured${NC}"
    else
        echo -e "  Username: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$MILVUS_PASSWORD" ]]; then
        echo -e "  Password: ${GREEN}configured${NC}"
    else
        echo -e "  Password: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$MILVUS_DB_NAME" ]]; then
        echo -e "  DB Name: ${GREEN}$MILVUS_DB_NAME${NC}"
    else
        echo -e "  DB Name: ${YELLOW}not set${NC}"
    fi
    if [[ -n "$MILVUS_COLLECTION" ]]; then
        echo -e "  Collection: ${GREEN}$MILVUS_COLLECTION${NC}"
    else
        echo -e "  Collection: ${YELLOW}default (trpc_agent_go)${NC}"
    fi
    
    echo -e "${BLUE}========================================${NC}"
    echo ""
}

# Check required environment for selected vector store
check_vectorstore_env() {
    local store=$1
    local missing=0
    
    case $store in
        tcvector)
            if [[ -z "$TCVECTOR_URL" ]]; then
                echo -e "${RED}Error: TCVECTOR_URL is required for tcvector${NC}"
                missing=1
            fi
            if [[ -z "$TCVECTOR_USERNAME" ]]; then
                echo -e "${RED}Error: TCVECTOR_USERNAME is required for tcvector${NC}"
                missing=1
            fi
            if [[ -z "$TCVECTOR_PASSWORD" ]]; then
                echo -e "${RED}Error: TCVECTOR_PASSWORD is required for tcvector${NC}"
                missing=1
            fi
            ;;
        pgvector)
            # PGVector has defaults, but warn if password not set
            if [[ -z "$PGVECTOR_PASSWORD" ]]; then
                echo -e "${YELLOW}Warning: PGVECTOR_PASSWORD not set, using empty password${NC}"
            fi
            ;;
        elasticsearch)
            # Elasticsearch has defaults, just info
            if [[ -z "$ELASTICSEARCH_HOSTS" ]]; then
                echo -e "${YELLOW}Info: Using default ELASTICSEARCH_HOSTS (http://localhost:9200)${NC}"
            fi
            ;;
        milvus)
            # Milvus has defaults, just info
            if [[ -z "$MILVUS_ADDRESS" ]]; then
                echo -e "${YELLOW}Info: Using default MILVUS_ADDRESS (localhost:19530)${NC}"
            fi
            ;;
    esac
    
    if [[ -z "$OPENAI_API_KEY" ]]; then
        echo -e "${RED}Error: OPENAI_API_KEY is required${NC}"
        missing=1
    fi
    
    return $missing
}

# Run a single example
run_example() {
    local example_path=$1
    local example_name=$(basename "$example_path")

    # Resolve output directory and per-vectorstore subdir
    local abs_output_dir="$SCRIPT_DIR/output"
    if [[ "$OUTPUT_DIR" == /* ]]; then
        abs_output_dir="$OUTPUT_DIR"
    else
        abs_output_dir="$SCRIPT_DIR/$OUTPUT_DIR"
    fi
    local vector_output_dir="$abs_output_dir/$VECTORSTORE"
    mkdir -p "$vector_output_dir"

    local output_file="$vector_output_dir/${example_path//\//_}.log"
    
    echo -e "${BLUE}Running: ${NC}$example_path"
    echo -e "${BLUE}Vector Store: ${NC}$VECTORSTORE"
    echo -e "${BLUE}Output: ${NC}$output_file"
    
    # Change to example directory and run
    cd "$SCRIPT_DIR/$example_path"
    
    local start_time=$(date +%s)
    
    # Run the example and capture output
    if go run main.go -vectorstore "$VECTORSTORE" > "$output_file" 2>&1; then
        local end_time=$(date +%s)
        local duration=$((end_time - start_time))
        echo -e "${GREEN}✓ Success${NC} (${duration}s)"
        echo "---"
        # Show last few lines of output
        tail -10 "$output_file"
        echo ""
    else
        local end_time=$(date +%s)
        local duration=$((end_time - start_time))
        echo -e "${RED}✗ Failed${NC} (${duration}s)"
        echo "---"
        # Show error output
        tail -20 "$output_file"
        echo ""
    fi
    
    cd "$SCRIPT_DIR"
    echo ""
}

# Main execution
main() {
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}Knowledge Examples Runner${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    
    print_env_status
    
    # Check environment for selected vector store
    if ! check_vectorstore_env "$VECTORSTORE"; then
        echo ""
        echo -e "${RED}Please set the required environment variables and try again.${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}Selected Vector Store: $VECTORSTORE${NC}"
    echo -e "${GREEN}Output Directory: $OUTPUT_DIR${NC}"
    echo -e "${GREEN}Random Table: $RANDOMTABLE${NC}"
    echo ""
    
    # Setup random table names if enabled
    if [[ "$RANDOMTABLE" == "true" ]]; then
        # Generate random names for all vector stores
        export TCVECTOR_COLLECTION=$(generate_random_name "kb")
        export PGVECTOR_TABLE=$(generate_random_name "kb")
        export ELASTICSEARCH_INDEX_NAME=$(generate_random_name "kb")
        export MILVUS_COLLECTION=$(generate_random_name "kb")
        
        echo -e "${YELLOW}Generated random names:${NC}"
        echo -e "  TCVECTOR_COLLECTION: $TCVECTOR_COLLECTION"
        echo -e "  PGVECTOR_TABLE: $PGVECTOR_TABLE"
        echo -e "  ELASTICSEARCH_INDEX_NAME: $ELASTICSEARCH_INDEX_NAME"
        echo -e "  MILVUS_COLLECTION: $MILVUS_COLLECTION"
        echo ""
    fi
    
    # Resolve output directory to absolute path and create per-vectorstore subdir
    local abs_output_dir="$SCRIPT_DIR/output"
    if [[ "$OUTPUT_DIR" == /* ]]; then
        abs_output_dir="$OUTPUT_DIR"
    else
        abs_output_dir="$SCRIPT_DIR/$OUTPUT_DIR"
    fi
    local vector_output_dir="$abs_output_dir/$VECTORSTORE"
    mkdir -p "$vector_output_dir"
    
    # Determine which examples to run
    local examples_to_run=()
    
    if [[ -n "$EXAMPLES" ]]; then
        # Parse comma-separated list
        IFS=',' read -ra examples_to_run <<< "$EXAMPLES"
    else
        # Run all examples (basic + features)
        examples_to_run=("${BASIC_EXAMPLES[@]}" "${FEATURE_EXAMPLES[@]}")
    fi
    
    # Summary file
    local summary_file="$vector_output_dir/summary_${VECTORSTORE}.txt"
    echo "Knowledge Examples Run Summary" > "$summary_file"
    echo "==============================" >> "$summary_file"
    echo "Vector Store: $VECTORSTORE" >> "$summary_file"
    echo "Random Table: $RANDOMTABLE" >> "$summary_file"
    if [[ "$RANDOMTABLE" == "true" ]]; then
        echo "  TCVECTOR_COLLECTION: $TCVECTOR_COLLECTION" >> "$summary_file"
        echo "  PGVECTOR_TABLE: $PGVECTOR_TABLE" >> "$summary_file"
        echo "  ELASTICSEARCH_INDEX_NAME: $ELASTICSEARCH_INDEX_NAME" >> "$summary_file"
        echo "  MILVUS_COLLECTION: $MILVUS_COLLECTION" >> "$summary_file"
    fi
    echo "Date: $(date)" >> "$summary_file"
    echo "" >> "$summary_file"
    
    local total=0
    local passed=0
    local failed=0
    
    # Run each example
    for example in "${examples_to_run[@]}"; do
        example=$(echo "$example" | xargs)  # Trim whitespace
        
        if [[ ! -d "$SCRIPT_DIR/$example" ]]; then
            echo -e "${YELLOW}Warning: Example not found: $example${NC}"
            continue
        fi
        
        # Generate unique table name for each example when RANDOMTABLE is enabled
        # This prevents data mixing between different examples
        if [[ "$RANDOMTABLE" == "true" ]]; then
            local example_suffix=$(echo "$example" | tr '/' '_' | tr '-' '_')
            export TCVECTOR_COLLECTION=$(generate_random_name "kb_${example_suffix}")
            export PGVECTOR_TABLE=$(generate_random_name "kb_${example_suffix}")
            export ELASTICSEARCH_INDEX_NAME=$(generate_random_name "kb_${example_suffix}")
            export MILVUS_COLLECTION=$(generate_random_name "kb_${example_suffix}")
        fi
        
        ((total++)) || true
        
        echo -e "${BLUE}----------------------------------------${NC}"
        
        local output_file="$vector_output_dir/${example//\//_}.log"
        
        cd "$SCRIPT_DIR/$example"
        
        local start_time=$(date +%s)
        
        if go run main.go -vectorstore "$VECTORSTORE" > "$output_file" 2>&1; then
            local end_time=$(date +%s)
            local duration=$((end_time - start_time))
            echo -e "${GREEN}✓ $example${NC} (${duration}s)"
            echo "✓ $example (${duration}s)" >> "$summary_file"
            passed=$((passed + 1))
            
            # Show brief output
            echo "  Output preview:"
            tail -5 "$output_file" | sed 's/^/    /'
        else
            local end_time=$(date +%s)
            local duration=$((end_time - start_time))
            echo -e "${RED}✗ $example${NC} (${duration}s)"
            echo "✗ $example (${duration}s) - FAILED" >> "$summary_file"
            failed=$((failed + 1))
            
            # Show error
            echo "  Error:"
            tail -10 "$output_file" | sed 's/^/    /'
        fi
        
        cd "$SCRIPT_DIR"
        echo ""
    done
    
    # Print summary
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}Summary${NC}"
    echo -e "${BLUE}========================================${NC}"
    echo -e "Total: $total"
    echo -e "${GREEN}Passed: $passed${NC}"
    if [[ $failed -gt 0 ]]; then
        echo -e "${RED}Failed: $failed${NC}"
    else
        echo -e "Failed: $failed"
    fi
    echo ""
    echo "Results saved to: $vector_output_dir"
    echo "Summary: $summary_file"
    
    # Append to summary file
    echo "" >> "$summary_file"
    echo "==============================" >> "$summary_file"
    echo "Total: $total, Passed: $passed, Failed: $failed" >> "$summary_file"
    
    # Exit with error if any failed
    if [[ $failed -gt 0 ]]; then
        exit 1
    fi
}

# Run examples for a single vector store (used by all-stores mode)
run_for_store() {
    local store=$1
    VECTORSTORE=$store
    
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}Running with Vector Store: $VECTORSTORE${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    
    # Check environment for selected vector store
    if ! check_vectorstore_env "$VECTORSTORE"; then
        echo -e "${YELLOW}Skipping $VECTORSTORE due to missing environment variables${NC}"
        echo ""
        return 1
    fi
    
    # Setup random table names if enabled
    if [[ "$RANDOMTABLE" == "true" ]]; then
        export TCVECTOR_COLLECTION=$(generate_random_name "kb")
        export PGVECTOR_TABLE=$(generate_random_name "kb")
        export ELASTICSEARCH_INDEX_NAME=$(generate_random_name "kb")
        export MILVUS_COLLECTION=$(generate_random_name "kb")
        
        echo -e "${YELLOW}Generated random names:${NC}"
        echo -e "  TCVECTOR_COLLECTION: $TCVECTOR_COLLECTION"
        echo -e "  PGVECTOR_TABLE: $PGVECTOR_TABLE"
        echo -e "  ELASTICSEARCH_INDEX_NAME: $ELASTICSEARCH_INDEX_NAME"
        echo -e "  MILVUS_COLLECTION: $MILVUS_COLLECTION"
        echo ""
    fi
    
    # Determine which examples to run
    local examples_to_run=()
    if [[ -n "$EXAMPLES" ]]; then
        IFS=',' read -ra examples_to_run <<< "$EXAMPLES"
    else
        examples_to_run=("${BASIC_EXAMPLES[@]}" "${FEATURE_EXAMPLES[@]}")
    fi
    
    local store_passed=0
    local store_failed=0
    
    # Create output directory (use absolute path) and per-vectorstore subdir
    local abs_output_dir="$SCRIPT_DIR/output"
    if [[ "$OUTPUT_DIR" == /* ]]; then
        abs_output_dir="$OUTPUT_DIR"
    else
        abs_output_dir="$SCRIPT_DIR/$OUTPUT_DIR"
    fi
    local vector_output_dir="$abs_output_dir/$VECTORSTORE"
    mkdir -p "$vector_output_dir"
    
    for example in "${examples_to_run[@]}"; do
        example=$(echo "$example" | xargs)
        
        if [[ ! -d "$SCRIPT_DIR/$example" ]]; then
            echo -e "${YELLOW}Warning: Example not found: $example${NC}"
            continue
        fi
        
        # Generate unique table name for each example when RANDOMTABLE is enabled
        # This prevents data mixing between different examples
        if [[ "$RANDOMTABLE" == "true" ]]; then
            local example_suffix=$(echo "$example" | tr '/' '_' | tr '-' '_')
            export TCVECTOR_COLLECTION=$(generate_random_name "kb_${example_suffix}")
            export PGVECTOR_TABLE=$(generate_random_name "kb_${example_suffix}")
            export ELASTICSEARCH_INDEX_NAME=$(generate_random_name "kb_${example_suffix}")
            export MILVUS_COLLECTION=$(generate_random_name "kb_${example_suffix}")
        fi
        
        echo -e "${BLUE}----------------------------------------${NC}"
        
        local output_file="$vector_output_dir/${example//\//_}.log"
        
        cd "$SCRIPT_DIR/$example"
        
        local start_time=$(date +%s)
        
        if go run main.go -vectorstore "$VECTORSTORE" > "$output_file" 2>&1; then
            local end_time=$(date +%s)
            local duration=$((end_time - start_time))
            echo -e "${GREEN}✓ [$VECTORSTORE] $example${NC} (${duration}s)"
            store_passed=$((store_passed + 1))
            
            echo "  Output preview:"
            tail -5 "$output_file" | sed 's/^/    /'
        else
            local end_time=$(date +%s)
            local duration=$((end_time - start_time))
            echo -e "${RED}✗ [$VECTORSTORE] $example${NC} (${duration}s)"
            store_failed=$((store_failed + 1))
            
            echo "  Error:"
            tail -10 "$output_file" | sed 's/^/    /'
        fi
        
        cd "$SCRIPT_DIR"
        echo ""
    done
    
    echo -e "${BLUE}Store $VECTORSTORE: Passed=$store_passed, Failed=$store_failed${NC}"
    echo ""
    
    # Return counts via global vars
    STORE_PASSED=$store_passed
    STORE_FAILED=$store_failed
    return 0
}

# Main entry point
if [[ "$ALL_STORES" == "true" ]]; then
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}Knowledge Examples Runner (All Stores)${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo ""
    
    print_env_status
    
    # Check OpenAI key first
    if [[ -z "$OPENAI_API_KEY" ]]; then
        echo -e "${RED}Error: OPENAI_API_KEY is required${NC}"
        exit 1
    fi
    
    # Resolve base output directory (no per-store here, each run_for_store handles its own subdir)
    base_output_dir="$SCRIPT_DIR/output"
    if [[ "$OUTPUT_DIR" == /* ]]; then
        base_output_dir="$OUTPUT_DIR"
    else
        base_output_dir="$SCRIPT_DIR/$OUTPUT_DIR"
    fi
    mkdir -p "$base_output_dir"
    
    total_passed=0
    total_failed=0
    stores_run=0
    
    for store in "${ALL_VECTORSTORES[@]}"; do
        if run_for_store "$store"; then
            total_passed=$((total_passed + STORE_PASSED))
            total_failed=$((total_failed + STORE_FAILED))
            stores_run=$((stores_run + 1))
        fi
    done
    
    echo -e "${GREEN}========================================${NC}"
    echo -e "${GREEN}Final Summary (All Stores)${NC}"
    echo -e "${GREEN}========================================${NC}"
    echo -e "Stores Run: $stores_run"
    echo -e "${GREEN}Total Passed: $total_passed${NC}"
    if [[ $total_failed -gt 0 ]]; then
        echo -e "${RED}Total Failed: $total_failed${NC}"
    else
        echo -e "Total Failed: $total_failed"
    fi
    echo ""
    echo "Results saved to: $base_output_dir"
    
    if [[ $total_failed -gt 0 ]]; then
        exit 1
    fi
else
    main
fi

