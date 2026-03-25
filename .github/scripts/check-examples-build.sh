#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/examples-modules.sh"
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
	# Validate the examples directory exists.
	if [[ ! -d "${EXAMPLES_DIR}" ]]; then
		echo "examples directory not found: ${EXAMPLES_DIR}" >&2
		return 2
	fi
	# Build the `go build` arguments.
	local -a build_args=()
	if ! has_flag_prefix "-mod" "$@"; then
		build_args+=("-mod=readonly")
	fi
	build_args+=("$@")
	local -a mod_files=()
	select_examples_modules_for_current_shard mod_files
	echo "Shard: $(examples_shard_label)"
	echo "Selected modules: ${#mod_files[@]}"
	local modfile
	for modfile in "${mod_files[@]}"; do
		echo "  - $(examples_module_path "${modfile}")"
	done
	if (( ${#mod_files[@]} == 0 )); then
		echo "no examples modules selected for this shard." >&2
		return 2
	fi
	# Build each module and record failures.
	local -a failed_modules=()
	local module_dir module_path build_out_dir
	build_out_dir="$(mktemp -d -t trpc-agent-go-examples-build-XXXXXX)"
	trap "rm -rf \"$build_out_dir\"" EXIT
	for modfile in "${mod_files[@]}"; do
		module_dir="$(dirname "$modfile")"
		module_path="$(examples_module_path "${modfile}")"
		echo "==> Build: $module_path"
		local build_log build_log2
		# Try a standard build first.
		if build_log=$(cd "$module_dir" && go build "${build_args[@]}" ./... 2>&1); then
			if [[ -n "$build_log" ]]; then
				printf '%s\n' "$build_log"
			fi
			echo "OK : $module_path"
		else
			# Retry by writing binaries into a temporary directory.
			if build_log2=$(cd "$module_dir" && go build -o "$build_out_dir/" "${build_args[@]}" ./... 2>&1); then
				if [[ -n "$build_log2" ]]; then
					printf '%s\n' "$build_log2"
				fi
				echo "OK : $module_path"
			else
				printf '%s\n' "$build_log" >&2
				printf '%s\n' "$build_log2" >&2
				echo "Build failed: $module_path" >&2
				failed_modules+=("$module_path")
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
