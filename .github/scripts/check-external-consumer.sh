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

module_path_from_go_mod() {
	local mod_file="$1"
	local directive module_path _
	while read -r directive module_path _; do
		if [[ "${directive}" == "module" ]]; then
			if [[ -z "${module_path}" ]]; then
				echo "empty module path in ${mod_file}" >&2
				return 1
			fi
			printf '%s\n' "${module_path}"
			return 0
		fi
	done <"${mod_file}"
	echo "unable to read module path from ${mod_file}" >&2
	return 1
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
	local -a replace_args=()
	for mod_file in "${repository_modules[@]}"; do
		mod_dir="$(cd "$(dirname "${mod_file}")" && pwd)"
		module_path="$(module_path_from_go_mod "${mod_file}")"
		replace_args+=("-replace" "${module_path}=${mod_dir}")
	done
	go mod edit "${replace_args[@]}"
}

check_module_as_external_consumer() {
	local mod_file="$1"
	local mod_dir module_path readable package_file consumer_dir status
	mod_dir="$(cd "$(dirname "${mod_file}")" && pwd)"
	readable="$(module_readable_name "${mod_file}")"
	module_path="$(module_path_from_go_mod "${mod_file}")"

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

# is_pseudo_version reports whether ver looks like a Go pseudo-version
# (vX.Y.Z[-pre].0.yyyymmddhhmmss-<commit>).
is_pseudo_version() {
	local ver="$1"
	[[ "${ver}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+.*-0?\.?[0-9]{14}-[a-f0-9]{12,40}$ ]]
}

# is_repository_module_path reports whether dep_path belongs to this
# repository (the root module or one of its nested modules).
is_repository_module_path() {
	local dep_path="$1" root_path
	root_path="$(module_path_from_go_mod "${repo_root}/go.mod")"
	[[ "${dep_path}" == "${root_path}" || "${dep_path}" == "${root_path}/"* ]]
}

# module_requires prints "<path> <version>" for every require entry of
# the module, ignoring comments and replace directives.
module_requires() {
	local mod_file="$1"
	awk '
		$1 == "require" && NF >= 3 && $2 != "(" { print $2, $3 }
		$1 == "require" && $2 == "(" { in_require = 1; next }
		in_require && $1 == ")" { in_require = 0; next }
		in_require && $1 ~ /^\/\// { next }
		in_require && NF >= 2 { print $1, $2 }
	' "${mod_file}"
}

# version_resolves_remotely reports whether dep_path@dep_ver can be
# fetched from the module proxy, i.e. the version has been published
# (its commit is reachable from a branch or tag of the upstream
# repository).
version_resolves_remotely() {
	local dep_path="$1" dep_ver="$2" resolver_dir
	resolver_dir="${tmp_root}/resolver"
	mkdir -p "${resolver_dir}"
	if [[ ! -f "${resolver_dir}/go.mod" ]]; then
		(cd "${resolver_dir}" && GOWORK=off go mod init example.com/consumer-version-probe >/dev/null 2>&1)
	fi
	(cd "${resolver_dir}" && GOWORK=off go mod download "${dep_path}@${dep_ver}" >/dev/null 2>&1)
}

# commit_exists_locally reports whether commit is present in this
# checkout, e.g. a commit from the current change under test.
commit_exists_locally() {
	local commit="$1"
	git -C "${repo_root}" cat-file -e "${commit}^{commit}" 2>/dev/null
}

# extract_commit_source materializes the tree of commit into dest using
# git archive, so a consumer can compile against the exact source the
# pseudo-version names.
extract_commit_source() {
	local commit="$1" dest="$2"
	mkdir -p "${dest}"
	git -C "${repo_root}" archive --format=tar "${commit}" | tar -xf - -C "${dest}"
}

# check_module_with_upstream_dependencies compiles the module under test
# against the EXACT versions of sibling repository modules declared in
# its go.mod, instead of repository-wide local replacements. This catches
# requirement versions that predate the APIs the module compiles against:
# once published, a downstream build selects the declared version and the
# module would not compile.
#
# Only "bootstrap" dependencies trigger the check: pseudo-versions naming
# commits that are present in this checkout but not yet published (not
# resolvable from the module proxy — typically commits from this very
# change). Published pins (tags and remotely resolvable pseudo-versions)
# are validated the same way once the module is published; enforcing that
# repo-wide is left to a separate cleanup, since several existing
# submodules currently carry stale pins that predate APIs they use.
#
# A bootstrap pin cannot be fetched remotely, so its source is
# materialized from the local git history at the exact commit the version
# names — a faithful stand-in for the future proxy download once the
# commit lands on a default branch of the upstream repository.
check_module_with_upstream_dependencies() {
	local mod_file="$1"
	local mod_dir module_path readable package_file consumer_dir status
	local req_path req_ver commit dep_src
	local -a replace_args=()
	local has_bootstrap=false has_broken=false
	mod_dir="$(cd "$(dirname "${mod_file}")" && pwd)"
	readable="$(module_readable_name "${mod_file}")"
	module_path="$(module_path_from_go_mod "${mod_file}")"

	while read -r req_path req_ver; do
		[[ -z "${req_path}" || -z "${req_ver}" ]] && continue
		[[ "${req_path}" == "${module_path}" ]] && continue
		is_repository_module_path "${req_path}" || continue
		if ! is_pseudo_version "${req_ver}"; then
			continue # published tag; covered by the regular consumer check
		fi
		if version_resolves_remotely "${req_path}" "${req_ver}"; then
			continue # published pseudo-version
		fi
		commit="${req_ver##*-}"
		if ! commit_exists_locally "${commit}"; then
			echo "::error::${readable} requires ${req_path}@${req_ver}, which is neither resolvable from the module proxy nor present in this checkout. Re-pin it to a version containing the required APIs."
			has_broken=true
			continue
		fi
		dep_src="${tmp_root}/dep-src.${commit}"
		if [[ ! -d "${dep_src}" ]]; then
			if ! extract_commit_source "${commit}" "${dep_src}"; then
				echo "::error::Failed to extract ${req_path}@${req_ver} source from commit ${commit}."
				has_broken=true
				continue
			fi
		fi
		replace_args+=("-replace" "${req_path}=${dep_src}")
		has_bootstrap=true
	done < <(module_requires "${mod_file}")

	if [[ "${has_broken}" == "true" ]]; then
		return 1
	fi
	if [[ "${has_bootstrap}" != "true" ]]; then
		# No unpublished sibling requirements; the regular consumer
		# check already covers this module.
		return 0
	fi

	echo "::group::External consumer (upstream dependencies): ${readable}"
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

	consumer_dir="$(mktemp -d "${tmp_root}/consumer-upstream.XXXXXX")"
	status=0
	(
		cd "${consumer_dir}"
		go mod init example.com/trpc-agent-go-upstream-consumer
		# Replace the module under test (it has no published version
		# yet) and each bootstrap sibling with the exact source its
		# declared pseudo-version names. Every other dependency —
		# including published sibling pins — resolves remotely, so no
		# repository-wide replacements mask version problems here.
		go mod edit -replace "${module_path}=${mod_dir}" "${replace_args[@]}"
		write_consumer_test "${package_file}" "${consumer_dir}/consumer_test.go"
		GOWORK=off go mod tidy
		GOWORK=off go test ./...
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
		# Additionally compile against the exact declared versions of
		# sibling repository modules when the module pins unpublished
		# (bootstrap) commits; the function is a no-op otherwise.
		if ! check_module_with_upstream_dependencies "${mod_file}"; then
			failed_modules+=("$(module_readable_name "${mod_file}") (upstream dependencies)")
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
