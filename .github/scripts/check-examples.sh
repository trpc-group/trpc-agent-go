#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

echo "::group::Checking examples go.mod, go.sum and build"

cd examples

# Find all go.mod files in examples directory
go_mod_files=()
while IFS= read -r -d '' mod_file; do
    go_mod_files+=("$mod_file")
done < <(find . -name "go.mod" -print0 | sort -z)

if [ "${#go_mod_files[@]}" -eq 0 ]; then
    echo "No go.mod files found in examples directory"
    exit 1
fi

has_mod_issues=false
has_build_issues=false

for mod_file in "${go_mod_files[@]}"; do
    mod_dir="$(dirname "$mod_file")"
    rel_path="${mod_dir#./}"
    if [ "$rel_path" = "." ]; then
        rel_path=""
    fi

    echo "::group::Checking ${rel_path:-examples}"

    cd "$repo_root/examples/$rel_path"

    # Store original content
    original_go_mod="$(cat go.mod)"
    original_go_sum=""
    has_go_sum=false
    if [ -f "go.sum" ]; then
        original_go_sum="$(cat go.sum)"
        has_go_sum=true
    fi

    # Run go mod tidy
    go mod tidy

    # Check if files changed
    mod_changed=false
    sum_changed=false

    if ! diff -q <(echo "${original_go_mod}") go.mod >/dev/null; then
        mod_changed=true
    fi

    if [ "$has_go_sum" = true ]; then
        if ! diff -q <(echo "${original_go_sum}") go.sum >/dev/null; then
            sum_changed=true
        fi
    else
        # Check if go.sum was created
        if [ -f "go.sum" ]; then
            sum_changed=true
        fi
    fi

    if [ "$mod_changed" = true ] || [ "$sum_changed" = true ]; then
        has_mod_issues=true
        if [ "$mod_changed" = true ]; then
            echo "::error::examples/${rel_path:-root}/go.mod is not up-to-date. Run 'go mod tidy' in examples/${rel_path:-root} directory."
        fi
        if [ "$sum_changed" = true ]; then
            echo "::error::examples/${rel_path:-root}/go.sum is not up-to-date. Run 'go mod tidy' in examples/${rel_path:-root} directory."
        fi
        # Skip build check if go.mod is not up-to-date
        echo "::endgroup::"
        continue
    else
        echo "examples/${rel_path:-root}/go.mod and go.sum are up-to-date"
    fi

    # Check if the module can build successfully
    echo "Building examples/${rel_path:-root}..."

    # Try standard build first
    if go build ./... 2>/dev/null; then
        echo "examples/${rel_path:-root} builds successfully"
    else
        # If standard build fails, try building to a temporary directory
        temp_dir="/tmp/go-build-check-${rel_path:-root}-$$"
        mkdir -p "$temp_dir"

        if go build -o "$temp_dir/" ./... 2>/dev/null; then
            echo "examples/${rel_path:-root} builds successfully"
            rm -rf "$temp_dir"
        else
            has_build_issues=true
            echo "::error::examples/${rel_path:-root} failed to build. Please check dependencies and imports."
            rm -rf "$temp_dir" 2>/dev/null || true
        fi
    fi

    echo "::endgroup::"
done

cd "$repo_root"

# Report issues with specific error messages
if [ "$has_mod_issues" = true ] && [ "$has_build_issues" = true ]; then
    echo "::error::Some examples modules have go.mod/go.sum issues and some have build failures"
    exit 1
elif [ "$has_mod_issues" = true ]; then
    echo "::error::Some examples modules have go.mod/go.sum files that are not up-to-date"
    exit 1
elif [ "$has_build_issues" = true ]; then
    echo "::error::Some examples modules failed to build"
    exit 1
fi

echo "All examples modules are up-to-date and build successfully"
echo "::endgroup::"
