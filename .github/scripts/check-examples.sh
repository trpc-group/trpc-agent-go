#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/examples-modules.sh"
cd "${EXAMPLES_REPO_ROOT}"

echo "::group::Checking examples go.mod, go.sum and build"
go_mod_files=()
select_examples_modules_for_current_shard go_mod_files
echo "Shard: $(examples_shard_label)"
echo "Selected modules: ${#go_mod_files[@]}"
for mod_file in "${go_mod_files[@]}"; do
    echo "  - $(examples_module_path "${mod_file}")"
done

has_mod_issues=false
has_build_issues=false
has_internal_imports=false
mod_issue_modules=()
build_issue_modules=()
internal_import_modules=()

for mod_file in "${go_mod_files[@]}"; do
    mod_dir="$(dirname "${mod_file}")"
    rel_path="$(examples_module_rel_path "${mod_file}")"
    module_key="${rel_path:-root}"
    module_path="$(examples_module_path "${mod_file}")"
    group_name="$(examples_module_group_name "${mod_file}")"
    echo "::group::Checking ${group_name}"
    cd "${mod_dir}"
    if grep -r --include="*.go" \
        "trpc.group/trpc-go/trpc-agent-go/internal" . >/dev/null 2>&1; then
        has_internal_imports=true
        internal_import_modules+=("${module_path}")
        echo "::error::${module_path} contains imports of internal packages. Examples must not use internal packages."
        grep -rn --include="*.go" \
            "trpc.group/trpc-go/trpc-agent-go/internal" . || true
        echo "::endgroup::"
        continue
    fi

    original_go_mod="$(cat go.mod)"
    original_go_sum=""
    has_go_sum=false
    if [ -f "go.sum" ]; then
        original_go_sum="$(cat go.sum)"
        has_go_sum=true
    fi

    go mod tidy

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
        if [ -f "go.sum" ]; then
            sum_changed=true
        fi
    fi

    if [ "$mod_changed" = true ] || [ "$sum_changed" = true ]; then
        has_mod_issues=true
        mod_issue_modules+=("${module_path}")
        if [ "$mod_changed" = true ]; then
            echo "::error::${module_path}/go.mod is not up-to-date. Run 'go mod tidy' in ${module_path} directory."
        fi
        if [ "$sum_changed" = true ]; then
            echo "::error::${module_path}/go.sum is not up-to-date. Run 'go mod tidy' in ${module_path} directory."
        fi
        echo "::endgroup::"
        continue
    else
        echo "${module_path}/go.mod and go.sum are up-to-date"
    fi

    echo "Building ${module_path}..."
    if go build ./... 2>/dev/null; then
        echo "${module_path} builds successfully"
    else
        temp_dir="/tmp/go-build-check-${module_key}-$$"
        mkdir -p "$temp_dir"

        if go build -o "$temp_dir/" ./... 2>/dev/null; then
            echo "${module_path} builds successfully"
            rm -rf "$temp_dir"
        else
            has_build_issues=true
            build_issue_modules+=("${module_path}")
            echo "::error::${module_path} failed to build. Please check dependencies and imports."
            rm -rf "$temp_dir" 2>/dev/null || true
        fi
    fi

    echo "::endgroup::"
done

cd "${EXAMPLES_REPO_ROOT}"

if [ "${#internal_import_modules[@]}" -gt 0 ] || \
   [ "${#mod_issue_modules[@]}" -gt 0 ] || \
   [ "${#build_issue_modules[@]}" -gt 0 ]; then
    echo "::group::Examples check summary"
    if [ "${#internal_import_modules[@]}" -gt 0 ]; then
        echo "Directories with internal package imports:"
        printf '%s\n' "${internal_import_modules[@]}" | sed 's/^/- /'
    fi
    if [ "${#mod_issue_modules[@]}" -gt 0 ]; then
        echo "Directories with go.mod/go.sum issues:"
        printf '%s\n' "${mod_issue_modules[@]}" | sed 's/^/- /'
    fi
    if [ "${#build_issue_modules[@]}" -gt 0 ]; then
        echo "Directories with build failures:"
        printf '%s\n' "${build_issue_modules[@]}" | sed 's/^/- /'
    fi
    echo "::endgroup::"
fi

if [ "$has_internal_imports" = true ]; then
    echo "::error::Some examples modules contain internal package imports. Examples must not use internal packages."
    echo "::endgroup::"
    exit 1
elif [ "$has_mod_issues" = true ] && [ "$has_build_issues" = true ]; then
    echo "::error::Some examples modules have go.mod/go.sum issues and some have build failures"
    echo "::endgroup::"
    exit 1
elif [ "$has_mod_issues" = true ]; then
    echo "::error::Some examples modules have go.mod/go.sum files that are not up-to-date"
    echo "::endgroup::"
    exit 1
elif [ "$has_build_issues" = true ]; then
    echo "::error::Some examples modules failed to build"
    echo "::endgroup::"
    exit 1
fi

echo "All examples modules are up-to-date and build successfully"
echo "::endgroup::"
