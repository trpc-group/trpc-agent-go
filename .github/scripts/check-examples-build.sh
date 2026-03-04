#!/usr/bin/env bash
set -euo pipefail

export CGO_ENABLED=0

# This script checks that all Go example modules can be built with CGO disabled.
# It runs `go build ./...` in every module under the `examples` directory.

has_flag_prefix() {
	local prefix="$1"
	shift
	local arg
	for arg in "$@"; do
		if [[ "$arg" == "$prefix" || "$arg" == "$prefix="* ]]; then
			return 0
		fi
	done
	return 1
}

main() {
	# Resolve repository paths.
	local script_dir repo_root examples_dir
	script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	repo_root="$(cd "${script_dir}/../.." && pwd)"
	examples_dir="$repo_root/examples"
	# Validate the examples directory exists.
	if [[ ! -d "$examples_dir" ]]; then
		echo "examples directory not found: $examples_dir" >&2
		return 2
	fi
	# Build the `go build` arguments.
	local -a build_args=()
	if ! has_flag_prefix "-mod" "$@"; then
		build_args+=("-mod=readonly")
	fi
	build_args+=("$@")
	# Discover all example Go modules.
	local -a mod_files=()
	while IFS= read -r modfile; do
		mod_files+=("$modfile")
	done < <(find "$examples_dir" -name go.mod -type f | sort)
	if (( ${#mod_files[@]} == 0 )); then
		echo "no go.mod files found under examples directory." >&2
		return 2
	fi
	# Build each module and record failures.
	local -a failed_modules=()
	local modfile module_dir rel_dir build_out_dir
	build_out_dir="$(mktemp -d -t trpc-agent-go-examples-build-XXXXXX)"
	trap "rm -rf \"$build_out_dir\"" EXIT
	for modfile in "${mod_files[@]}"; do
		# Skip placeholder modules.
		if head -n 5 "${modfile}" | grep -q "DO NOT USE!"; then
			continue
		fi
		module_dir="$(dirname "$modfile")"
		rel_dir="${module_dir#"$repo_root"/}"
		echo "==> Build: $rel_dir"
		local build_log build_log2
		# Try a standard build first.
		if build_log=$(cd "$module_dir" && go build "${build_args[@]}" ./... 2>&1); then
			if [[ -n "$build_log" ]]; then
				printf '%s\n' "$build_log"
			fi
			echo "OK : $rel_dir"
		else
			# Retry by writing binaries into a temporary directory.
			if build_log2=$(cd "$module_dir" && go build -o "$build_out_dir/" "${build_args[@]}" ./... 2>&1); then
				if [[ -n "$build_log2" ]]; then
					printf '%s\n' "$build_log2"
				fi
				echo "OK : $rel_dir"
			else
				printf '%s\n' "$build_log" >&2
				printf '%s\n' "$build_log2" >&2
				echo "Build failed: $rel_dir" >&2
				failed_modules+=("$rel_dir")
			fi
		fi
		echo
	done
	# Report failures if any.
	if (( ${#failed_modules[@]} > 0 )); then
		echo "Build failed for modules (${#failed_modules[@]}):" >&2
		local m
		for m in "${failed_modules[@]}"; do
			echo "  - $m" >&2
		done
		return 1
	fi
	echo "All examples modules built successfully."
}

main "$@"
