#!/usr/bin/env bash
set -euo pipefail

# This script verifies that packages from published modules can be imported by
# an external consumer against the current repository snapshot.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
cd "${repo_root}"

tmp_root="$(mktemp -d)"
trap 'chmod -R u+w "${tmp_root}" >/dev/null 2>&1 || true; rm -rf "${tmp_root}"' EXIT

# The consumer check intentionally uses the non-CGO build path so CI does not
# depend on system SQLite libraries.
export CGO_ENABLED=0

declare -a requested_modules=()
declare -a repository_modules=()
while [[ $# -gt 0 ]]; do
	case "$1" in
		--module)
			if [[ $# -lt 2 ]]; then
				echo "missing value for --module" >&2
				exit 2
			fi
			requested_modules+=("$2")
			shift 2
			;;
		*)
			echo "unknown argument: $1" >&2
			exit 2
			;;
	esac
done

is_do_not_use_module() {
	local mod_file="$1"
	head -n 5 "${mod_file}" | grep -q "DO NOT USE!"
}

normalize_module_file() {
	local module="$1"
	local normalized="${module#./}"
	normalized="./${normalized}"
	if [[ ! -f "${normalized}" ]]; then
		echo "module file not found: ${module}" >&2
		return 2
	fi
	printf '%s\n' "${normalized}"
}

discover_modules() {
	local -n modules_ref="$1"
	local mod_file
	modules_ref=()
	if (( ${#requested_modules[@]} > 0 )); then
		for mod_file in "${requested_modules[@]}"; do
			modules_ref+=("$(normalize_module_file "${mod_file}")")
		done
		return 0
	fi
	while IFS= read -r -d '' mod_file; do
		if is_do_not_use_module "${mod_file}"; then
			continue
		fi
		modules_ref+=("${mod_file}")
	done < <(find . -name "go.mod" \
		-not -path "./.resource/*" \
		-not -path "./docs/*" \
		-not -path "./examples/*" \
		-not -path "./test/*" \
		-print0 | sort -z)
}

discover_repository_modules() {
	local -n modules_ref="$1"
	local mod_file mod_dir
	modules_ref=()
	while IFS= read -r -d '' mod_file; do
		if is_do_not_use_module "${mod_file}"; then
			continue
		fi
		mod_dir="$(cd "$(dirname "${mod_file}")" && pwd)"
		modules_ref+=("${mod_dir}/go.mod")
	done < <(find . -name "go.mod" \
		-not -path "./.resource/*" \
		-not -path "./docs/*" \
		-not -path "./examples/*" \
		-not -path "./test/*" \
		-print0 | sort -z)
}

module_readable_name() {
	local mod_file="$1"
	local mod_dir rel_dir
	mod_dir="$(dirname "${mod_file}")"
	rel_dir="${mod_dir#./}"
	if [[ -z "${rel_dir}" || "${rel_dir}" == "." ]]; then
		printf 'root\n'
		return 0
	fi
	printf '%s\n' "${rel_dir}"
}

is_external_importable_path() {
	local import_path="$1"
	[[ "${import_path}" != */internal ]] && [[ "${import_path}" != */internal/* ]]
}

list_importable_packages() {
	local mod_dir="$1"
	local output_file="$2"
	local package_list import_path package_name go_files cgo_files
	package_list="$(mktemp "${tmp_root}/packages.XXXXXX")"
	if ! (cd "${mod_dir}" && go list -f '{{.ImportPath}}{{"\t"}}{{.Name}}{{"\t"}}{{len .GoFiles}}{{"\t"}}{{len .CgoFiles}}' ./...) >"${package_list}"; then
		return 1
	fi
	: >"${output_file}"
	while IFS=$'\t' read -r import_path package_name go_files cgo_files; do
		if [[ -z "${import_path}" || -z "${package_name}" ]]; then
			continue
		fi
		if (( go_files == 0 )); then
			continue
		fi
		if [[ "${package_name}" == "main" ]]; then
			continue
		fi
		if ! is_external_importable_path "${import_path}"; then
			continue
		fi
		printf '%s\n' "${import_path}" >>"${output_file}"
	done <"${package_list}"
}

write_consumer_test() {
	local package_file="$1"
	local output_file="$2"
	{
		printf 'package consumer\n\n'
		printf 'import (\n'
		while IFS= read -r import_path; do
			printf '\t_ "%s"\n' "${import_path}"
		done <"${package_file}"
		printf ')\n'
	} >"${output_file}"
}

add_repository_replaces() {
	local mod_file mod_dir module_path
	for mod_file in "${repository_modules[@]}"; do
		mod_dir="$(cd "$(dirname "${mod_file}")" && pwd)"
		module_path="$(cd "${mod_dir}" && go list -m -f '{{.Path}}')"
		go mod edit -replace "${module_path}=${mod_dir}"
	done
}

check_module_as_external_consumer() {
	local mod_file="$1"
	local mod_dir module_path readable package_file consumer_dir status
	mod_dir="$(cd "$(dirname "${mod_file}")" && pwd)"
	readable="$(module_readable_name "${mod_file}")"
	module_path="$(cd "${mod_dir}" && go list -m -f '{{.Path}}')"

	echo "::group::External consumer: ${readable}"
	echo "module path: ${module_path}"
	echo "module dir: ${mod_dir}"

	package_file="$(mktemp "${tmp_root}/importable-packages.XXXXXX")"
	if ! list_importable_packages "${mod_dir}" "${package_file}"; then
		echo "::error::Failed to list packages for ${readable}."
		echo "::endgroup::"
		return 1
	fi
	if [[ ! -s "${package_file}" ]]; then
		echo "No external importable packages found, skipping ${readable}."
		echo "::endgroup::"
		return 0
	fi

	echo "Importable packages:"
	sed 's/^/  - /' "${package_file}"

	consumer_dir="$(mktemp -d "${tmp_root}/consumer.XXXXXX")"
	status=0
	(
		cd "${consumer_dir}"
		go mod init example.com/trpc-agent-go-external-consumer
		add_repository_replaces
		write_consumer_test "${package_file}" "${consumer_dir}/consumer_test.go"
		go mod tidy
		go test ./...
	) || status=$?

	echo "::endgroup::"
	return "${status}"
}

main() {
	local -a modules=()
	discover_modules modules
	discover_repository_modules repository_modules
	if (( ${#modules[@]} == 0 )); then
		echo "no go.mod files found." >&2
		return 2
	fi
	if (( ${#repository_modules[@]} == 0 )); then
		echo "no repository go.mod files found." >&2
		return 2
	fi
	local -a failed_modules=()
	local mod_file
	for mod_file in "${modules[@]}"; do
		if is_do_not_use_module "${mod_file}"; then
			echo "Skipping $(module_readable_name "${mod_file}"): marked as DO NOT USE."
			continue
		fi
		if ! check_module_as_external_consumer "${mod_file}"; then
			failed_modules+=("$(module_readable_name "${mod_file}")")
		fi
	done
	if (( ${#failed_modules[@]} > 0 )); then
		echo "::group::External consumer check summary"
		echo "External consumer check failed for modules:"
		local module_name
		for module_name in "${failed_modules[@]}"; do
			echo "  - ${module_name}"
		done
		echo "::endgroup::"
		return 1
	fi
}

main "$@"
