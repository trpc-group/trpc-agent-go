#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

echo "::group::Checking examples go.mod and go.sum files"

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

all_up_to_date=true

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
        all_up_to_date=false
        if [ "$mod_changed" = true ]; then
            echo "::error::examples/${rel_path:-root}/go.mod is not up-to-date. Run 'go mod tidy' in examples/${rel_path:-root} directory."
        fi
        if [ "$sum_changed" = true ]; then
            echo "::error::examples/${rel_path:-root}/go.sum is not up-to-date. Run 'go mod tidy' in examples/${rel_path:-root} directory."
        fi
    else
        echo "examples/${rel_path:-root}/go.mod and go.sum are up-to-date"
    fi

    echo "::endgroup::"
done

cd "$repo_root"

if [ "$all_up_to_date" = false ]; then
    echo "::error::Some examples go.mod/go.sum files are not up-to-date"
    exit 1
fi

echo "All examples go.mod and go.sum files are up-to-date"
echo "::endgroup::"
