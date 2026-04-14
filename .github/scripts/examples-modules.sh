#!/usr/bin/env bash

examples_helper_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLES_REPO_ROOT="$(cd "${examples_helper_dir}/../.." && pwd)"
EXAMPLES_DIR="${EXAMPLES_REPO_ROOT}/examples"

load_examples_shard_config() {
	local shard_index shard_total
	shard_index="${EXAMPLES_SHARD_INDEX:-0}"
	shard_total="${EXAMPLES_SHARD_TOTAL:-1}"
	if [[ ! "${shard_index}" =~ ^[0-9]+$ ]]; then
		echo "invalid EXAMPLES_SHARD_INDEX: ${shard_index}" >&2
		return 2
	fi
	if [[ ! "${shard_total}" =~ ^[1-9][0-9]*$ ]]; then
		echo "invalid EXAMPLES_SHARD_TOTAL: ${shard_total}" >&2
		return 2
	fi
	if (( shard_index >= shard_total )); then
		echo "EXAMPLES_SHARD_INDEX must be smaller than EXAMPLES_SHARD_TOTAL" >&2
		return 2
	fi
	EXAMPLES_LIB_SHARD_INDEX="${shard_index}"
	EXAMPLES_LIB_SHARD_TOTAL="${shard_total}"
}

examples_shard_label() {
	printf '%d/%d\n' \
		"$(( EXAMPLES_LIB_SHARD_INDEX + 1 ))" \
		"${EXAMPLES_LIB_SHARD_TOTAL}"
}

examples_module_rel_path() {
	local mod_file module_dir rel_path
	mod_file="$1"
	module_dir="$(dirname "${mod_file}")"
	if [[ "${module_dir}" == "${EXAMPLES_DIR}" ]]; then
		printf '\n'
		return 0
	fi
	rel_path="${module_dir#"${EXAMPLES_DIR}/"}"
	if [[ "${rel_path}" == "${module_dir}" ]]; then
		echo "module is outside examples directory: ${mod_file}" >&2
		return 2
	fi
	printf '%s\n' "${rel_path}"
}

examples_module_name() {
	local rel_path
	rel_path="$(examples_module_rel_path "$1")"
	if [[ -n "${rel_path}" ]]; then
		printf 'examples/%s\n' "${rel_path}"
		return 0
	fi
	printf 'examples/root\n'
}

examples_module_path() {
	local rel_path
	rel_path="$(examples_module_rel_path "$1")"
	if [[ -n "${rel_path}" ]]; then
		printf 'examples/%s\n' "${rel_path}"
		return 0
	fi
	printf 'examples\n'
}

examples_module_group_name() {
	local rel_path
	rel_path="$(examples_module_rel_path "$1")"
	if [[ -n "${rel_path}" ]]; then
		printf '%s\n' "${rel_path}"
		return 0
	fi
	printf 'examples\n'
}

examples_module_weight() {
	local mod_file module_dir go_sum weight
	mod_file="$1"
	module_dir="$(dirname "${mod_file}")"
	go_sum="${module_dir}/go.sum"
	if [[ ! -f "${go_sum}" ]]; then
		printf '1\n'
		return 0
	fi
	weight="$(wc -l < "${go_sum}")"
	weight="${weight//[[:space:]]/}"
	if [[ -z "${weight}" ]] || (( weight < 1 )); then
		weight=1
	fi
	printf '%s\n' "${weight}"
}

discover_examples_modules() {
	local -n modules_ref="$1"
	local mod_file
	modules_ref=()
	while IFS= read -r -d '' mod_file; do
		if head -n 5 "${mod_file}" | grep -q "DO NOT USE!"; then
			continue
		fi
		modules_ref+=("${mod_file}")
	done < <(find "${EXAMPLES_DIR}" -name "go.mod" -type f -print0 | sort -z)
	if (( ${#modules_ref[@]} == 0 )); then
		echo "no go.mod files found under examples directory." >&2
		return 2
	fi
}

select_examples_modules_for_current_shard() {
	local -n selected_ref="$1"
	local -a all_modules=()
	local -a shard_loads=()
	local -A module_shards=()
	local mod_file target_shard weight idx

	load_examples_shard_config
	discover_examples_modules all_modules
	if (( EXAMPLES_LIB_SHARD_TOTAL == 1 )); then
		selected_ref=("${all_modules[@]}")
		return 0
	fi

	for (( idx = 0; idx < EXAMPLES_LIB_SHARD_TOTAL; idx++ )); do
		shard_loads[idx]=0
	done

	while IFS=$'\t' read -r weight mod_file; do
		target_shard=0
		for (( idx = 1; idx < EXAMPLES_LIB_SHARD_TOTAL; idx++ )); do
			if (( shard_loads[idx] < shard_loads[target_shard] )); then
				target_shard="${idx}"
			fi
		done
		module_shards["${mod_file}"]="${target_shard}"
		shard_loads[target_shard]=$(( shard_loads[target_shard] + 10#${weight} ))
	done < <(
		for mod_file in "${all_modules[@]}"; do
			weight="$(examples_module_weight "${mod_file}")"
			printf '%s\t%s\n' "${weight}" "${mod_file}"
		done | sort -r -n -k1,1
	)

	selected_ref=()
	for mod_file in "${all_modules[@]}"; do
		if [[ "${module_shards["${mod_file}"]}" == \
			"${EXAMPLES_LIB_SHARD_INDEX}" ]]; then
			selected_ref+=("${mod_file}")
		fi
	done
	if (( ${#selected_ref[@]} == 0 )); then
		echo "no examples modules selected for shard $(examples_shard_label)." >&2
		return 2
	fi
}
