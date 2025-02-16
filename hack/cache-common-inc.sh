#!/usr/bin/env bash

# print the error message and exit with status 1
function error_exit() {
	echo "Error: $*" >&2
	exit 1
}

# e.g.
# ```console
# $ download_template_if_needed templates/default.yaml
# templates/default.yaml
# $ download_template_if_needed https://raw.githubusercontent.com/lima-vm/lima/v0.15.1/examples/ubuntu-lts.yaml
# /tmp/tmp.1J9Q6Q/template.yaml
# ```
function download_template_if_needed() {
	local template="$1"
	tmp_yaml="$(mktemp -d)/template.yaml"
	limactl tmpl copy --embed-all "${template}" "${tmp_yaml}" || return
	echo "${tmp_yaml}"
}

# e.g.
# ```console
# $ print_image_locations_for_arch_from_template templates/default.yaml
# https://cloud-images.ubuntu.com/releases/24.04/release-20240809/ubuntu-24.04-server-cloudimg-arm64.img sha256:2e0c90562af1970ffff220a5073a7830f4acc2aad55b31593003e8c363381e7a
# https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-arm64.img null
# ```
function print_image_locations_for_arch_from_template() {
	local template arch
	template=$(download_template_if_needed "$1") || return
	local -r template=${template}
	arch=$(detect_arch "${template}" "${2:-}") || return
	local -r arch=${arch}

	# extract digest, location and size by parsing template using arch
	local -r yq_filter="[.images | map(select(.arch == \"${arch}\")) | .[].location] | .[]"
	yq eval "${yq_filter}" "${template}"
}

# e.g.
# ```console
# $ detect_arch templates/default.yaml
# x86_64
# $ detect_arch templates/default.yaml arm64
# aarch64
# ```
function detect_arch() {
	local template arch
	template=$(download_template_if_needed "$1") || return
	local -r template=${template}

	arch="${2:-$(yq '.arch // ""' "${template}")}"
	arch="${arch:-$(uname -m)}"
	# normalize arch. amd64 -> x86_64, arm64 -> aarch64
	case "${arch}" in
	amd64 | x86_64) arch=x86_64 ;;
	aarch64 | arm64) arch=aarch64 ;;
	*) ;;
	esac
	echo "${arch}"
}

# e.g.
# ```console
# $ print_image_locations_for_arch_from_template templates/default.yaml|print_valid_image_index
# 0
# ```
function print_valid_image_index() {
	local index=0
	while read -r location; do
		[[ ${location} != "null" ]] || continue
		http_code_and_size=$(check_location_with_cache "${location}")
		read -r http_code _size <<<"${http_code_and_size}"
		if [[ ${http_code} -eq 200 ]]; then
			echo "${index}"
			return
		fi
		index=$((index + 1))
	done
	echo "Failed to get the valid image location" >&2
	return 1
}

# e.g.
# ```console
# $ size_from_location "https://cloud-images.ubuntu.com/releases/24.04/release-20240725/ubuntu-24.04-server-cloudimg-amd64.img"
# 585498624
# ```
function size_from_location() {
	(
		set -o pipefail
		local location=$1
		check_location "${location}" | cut -d' ' -f2
	)
}

# Check the remote location and print the http code and size.
# If GITHUB_ACTIONS is true, the result is not cached.
# e.g.
# ```console
# $ check_location "https://cloud-images.ubuntu.com/releases/24.04/release-20240725/ubuntu-24.04-server-cloudimg-amd64.img"
# 200 585498624
# ```
function check_location() {
	# shellcheck disable=SC2154
	if [[ ${GITHUB_ACTIONS:-false} == true ]]; then
		check_location_without_cache "$1"
	else
		check_location_with_cache "$1"
	fi
}

# Check the remote location and print the http code and size.
# The result is cached in .check_location-response-cache.yaml
# e.g.
# ```console
# $ check_location_with_cache "https://cloud-images.ubuntu.com/releases/24.04/release-20240725/ubuntu-24.04-server-cloudimg-amd64.img"
# 200 585498624
# ```
function check_location_with_cache() {
	local -r location="$1" cache_file=".check_location-response-cache.yaml"
	# check ${cache_file} for the cache
	if [[ -f ${cache_file} ]]; then
		cached=$(yq -e eval ".[\"${location}\"]" "${cache_file}" 2>/dev/null) && echo "${cached}" && return
	else
		touch "${cache_file}"
	fi
	http_code_and_size=$(check_location_without_cache "${location}") || return
	yq eval ".[\"${location}\"] = \"${http_code_and_size}\"" -i "${cache_file}" || return
	echo "${http_code_and_size}"
}

# e.g.
# ```console
# $ check_location "https://cloud-images.ubuntu.com/releases/24.04/release-20240725/ubuntu-24.04-server-cloudimg-amd64.img"
# 200 585498624
# ```
function check_location_without_cache() {
	local -r location="$1"
	curl -sIL -w "%{http_code} %header{Content-Length}" "${location}" -o /dev/null
}

# e.g.
# ```console
# $ print_image_kernel_initrd_locations_with_digest_for_arch_from_template_at_index templates/default.yaml 0
# https://cloud-images.ubuntu.com/releases/24.04/release-20240809/ubuntu-24.04-server-cloudimg-arm64.img
# sha256:2e0c90562af1970ffff220a5073a7830f4acc2aad55b31593003e8c363381e7a
# null
# null
# null
# null
# ```
function print_image_kernel_initrd_locations_with_digest_for_arch_from_template_at_index() {
	local template index="${2:-}" arch
	template=$(download_template_if_needed "$1") || return
	local -r template=${template}
	arch=$(detect_arch "${template}" "${3:-}") || return
	local -r arch=${arch}

	local -r yq_filter="[(.images[] | select(.arch == \"${arch}\"))].[${index}]|[
		.location,
		.digest,
		.kernel.location,
		.kernel.digest,
		.initrd.location,
		.initrd.digest
	]"
	yq -o=t eval "${yq_filter}" "${template}"
}

# e.g.
# ```console
# $ print_containerd_config_for_arch_from_template templates/default.yaml
# true
# https://github.com/containerd/nerdctl/releases/download/v1.7.6/nerdctl-full-1.7.6-linux-arm64.tar.gz
# sha256:77c747f09853ee3d229d77e8de0dd3c85622537d82be57433dc1fca4493bab95
# ```
function print_containerd_config_for_arch_from_template() {
	local template arch
	template=$(download_template_if_needed "$1") || return
	local -r template=${template}
	arch=$(detect_arch "${template}" "${2:-}") || return
	local -r arch=${arch}

	local -r yq_filter="
		[.containerd|[.system or .user],
		.containerd.archives | map(select(.arch == \"${arch}\")) | [.[0].location, .[0].digest]]|flatten
	"
	validated_template="$(
		limactl validate "${template}" --fill 2>/dev/null || echo "{.containerd: {system: false, user: false, archives: []}}"
	)"
	yq -o=t eval "${yq_filter}" <<<"${validated_template}"
}

# e.g.
# ```console
# $ location_to_sha256 "https://cloud-images.ubuntu.com/releases/24.04/release-20240809/ubuntu-24.04-server-cloudimg-arm64.img"
# ae988d797c6de06b9c8a81a2b814904151135ccfd4616c22948057f6280477e8
# ```
function location_to_sha256() {
	(
		set -o pipefail
		local -r location="$1"
		if command -v sha256sum >/dev/null; then
			sha256="$(echo -n "${location}" | sha256sum | cut -d' ' -f1)"
		elif command -v shasum >/dev/null; then
			sha256="$(echo -n "${location}" | shasum -a 256 | cut -d' ' -f1)"
		else
			error_exit "sha256sum or shasum not found"
		fi
		echo "${sha256}"
	)
}

# e.g.
# ```console
# $ cache_download_dir
# .download # on GitHub Actions
# /home/user/.cache/lima/download # on Linux
# /Users/user/Library/Caches/lima/download # on macOS
# /home/user/.cache/lima/download # on others
# ```
function cache_download_dir() {
	if [[ ${GITHUB_ACTIONS:-false} == true ]]; then
		echo ".download"
	else
		case "$(uname -s)" in
		Linux) echo "${XDG_CACHE_HOME:-${HOME}/.cache}/lima/download" ;;
		Darwin) echo "${HOME}/Library/Caches/lima/download" ;;
		*) echo "${HOME}/.cache/lima/download" ;;
		esac
	fi
}

# e.g.
# ```console
# $ location_to_cache_path "https://cloud-images.ubuntu.com/releases/24.04/release-20240809/ubuntu-24.04-server-cloudimg-arm64.img"
# .download/by-url-sha256/ae988d797c6de06b9c8a81a2b814904151135ccfd4616c22948057f6280477e8
# ```
function location_to_cache_path() {
	local location=$1
	[[ ${location} != "null" ]] || return
	sha256=$(location_to_sha256 "${location}") && download_dir=$(cache_download_dir) && echo "${download_dir}/by-url-sha256/${sha256}"
}

# e.g.
# ```console
# $ cache_key_from_prefix_location_and_digest image "https://cloud-images.ubuntu.com/releases/24.04/release-20240809/ubuntu-24.04-server-cloudimg-arm64.img" "sha256:2e0c90562af1970ffff220a5073a7830f4acc2aad55b31593003e8c363381e7a"
# image:ubuntu-24.04-server-cloudimg-arm64.img-sha256:2e0c90562af1970ffff220a5073a7830f4acc2aad55b31593003e8c363381e7a
# $ cache_key_from_prefix_location_and_digest image "https://cloud-images.ubuntu.com/releases/24.04/release-20240809/ubuntu-24.04-server-cloudimg-arm64.img" "null"
# image:ubuntu-24.04-server-cloudimg-arm64.img-url-sha256:ae988d797c6de06b9c8a81a2b814904151135ccfd4616c22948057f6280477e8
# ```
function cache_key_from_prefix_location_and_digest() {
	local prefix=$1 location=$2 digest=$3 location_basename
	[[ ${location} != "null" ]] || return
	location_basename=$(basename "${location}")
	if [[ ${digest} != "null" ]]; then
		echo "${prefix}:${location_basename}-${digest}"
	else
		# use sha256 of location as key if digest is not available
		echo "${prefix}:${location_basename}-url-sha256:$(location_to_sha256 "${location}")"
	fi
}

# e.g.
# ```console
# $ print_path_and_key_for_cache image "https://cloud-images.ubuntu.com/releases/24.04/release-20240809/ubuntu-24.04-server-cloudimg-arm64.img" "sha256:2e0c90562af1970ffff220a5073a7830f4acc2aad55b31593003e8c363381e7a"
# image-path=.download/by-url-sha256/ae988d797c6de06b9c8a81a2b814904151135ccfd4616c22948057f6280477e8
# image-key=image:ubuntu-24.04-server-cloudimg-arm64.img-sha256:2e0c90562af1970ffff220a5073a7830f4acc2aad55b31593003e8c363381e7a
# ```
function print_path_and_key_for_cache() {
	local -r prefix=$1 location=$2 digest=$3
	cache_path=$(location_to_cache_path "${location}" || true)
	cache_key=$(cache_key_from_prefix_location_and_digest "${prefix}" "${location}" "${digest}" || true)
	echo "${prefix}-path=${cache_path}"
	echo "${prefix}-key=${cache_key}"
}

# e.g.
# ```console
# $ print_cache_informations_from_template templates/default.yaml
# image-path=.download/by-url-sha256/ae988d797c6de06b9c8a81a2b814904151135ccfd4616c22948057f6280477e8
# image-key=image:ubuntu-24.04-server-cloudimg-arm64.img-sha256:2e0c90562af1970ffff220a5073a7830f4acc2aad55b31593003e8c363381e7a
# kernel-path=
# kernel-key=
# initrd-path=
# initrd-key=
# containerd-path=.download/by-url-sha256/21cc8dfa548ea8a678135bd6984c9feb9f8a01901d10b11bb491f6f4e7537158
# containerd-key=containerd:nerdctl-full-1.7.6-linux-arm64.tar.gz-sha256:77c747f09853ee3d229d77e8de0dd3c85622537d82be57433dc1fca4493bab95
# $ print_cache_informations_from_template templates/experimental/riscv64.yaml
# image-path=.download/by-url-sha256/760b6ec69c801177bdaea06d7ee25bcd6ab72a331b9d3bf38376578164eb8f01
# image-key=image:ubuntu-24.04-server-cloudimg-riscv64.img-sha256:361d72c5ed9781b097ab2dfb1a489c64e51936be648bbc5badee762ebdc50c31
# kernel-path=.download/by-url-sha256/4568026693dc0f31a551b6741839979c607ee6bb0bf7209c89f3348321c52c61
# kernel-key=kernel:qemu-riscv64_smode_uboot.elf-sha256:d4b3a10c3ef04219641802a586dca905e768805f5a5164fb68520887df54f33c
# initrd-path=
# initrd-key=
# ```
function print_cache_informations_from_template() {
	(
		set -o pipefail
		local template index image_kernel_initrd_info location digest containerd_info
		template=$(download_template_if_needed "$1") || return
		local -r template="${template}"
		index=$(print_image_locations_for_arch_from_template "${template}" "${@:2}" | print_valid_image_index) || return
		local -r index="${index}"
		image_kernel_initrd_info=$(print_image_kernel_initrd_locations_with_digest_for_arch_from_template_at_index "${template}" "${index}" "${@:2}") || return
		# shellcheck disable=SC2034
		read -r image_location image_digest kernel_location kernel_digest initrd_location initrd_digest <<<"${image_kernel_initrd_info}"
		for prefix in image kernel initrd; do
			location=${prefix}_location
			digest=${prefix}_digest
			print_path_and_key_for_cache "${prefix}" "${!location}" "${!digest}"
		done
		if command -v limactl >/dev/null; then
			containerd_info=$(print_containerd_config_for_arch_from_template "${template}" "${@:2}") || return
			read -r containerd_enabled containerd_location containerd_digest <<<"${containerd_info}"
			if [[ ${containerd_enabled} == "true" ]]; then
				print_path_and_key_for_cache "containerd" "${containerd_location}" "${containerd_digest}"
			fi
		fi
	)
}

# Compatible with hashFile() in GitHub Actions
# e.g.
# ```console
# $ hash_file templates/default.yaml
# ceec5ba3dc8872c083b2eb7f44e3e3f295d5dcdeccf0961ee153be6586525e5e
# ```
function hash_file() {
	(
		set -o pipefail
		local hash=""
		for file in "$@"; do
			hash="${hash}$(sha256sum "${file}" | cut -d' ' -f1)" || return
		done
		echo "${hash}" | xxd -r -p | sha256sum | cut -d' ' -f1
	)
}

# Download the file to the cache directory and print the path.
# e.g.
# ```console
# $ download_to_cache "https://cloud-images.ubuntu.com/releases/24.04/release-20240821/ubuntu-24.04-server-cloudimg-arm64.img"
# .download/by-url-sha256/346ee1ff9e381b78ba08e2a29445960b5cd31c51f896fc346b82e26e345a5b9a/data # on GitHub Actions
# /home/user/.cache/lima/download/by-url-sha256/346ee1ff9e381b78ba08e2a29445960b5cd31c51f896fc346b82e26e345a5b9a/data # on Linux
# /Users/user/Library/Caches/lima/download/by-url-sha256/346ee1ff9e381b78ba08e2a29445960b5cd31c51f896fc346b82e26e345a5b9a/data # on macOS
# /home/user/.cache/lima/download/by-url-sha256/346ee1ff9e381b78ba08e2a29445960b5cd31c51f896fc346b82e26e345a5b9a/data # on others
function download_to_cache() {
	local cache_path use_redirected_location=${2:-YES}
	cache_path=$(location_to_cache_path "$1")
	# before checking remote location, check if the data file is already downloaded and the time file is updated within 10 minutes
	if [[ -f ${cache_path}/data && -n "$(find "${cache_path}/time" -mmin -10 || true)" ]]; then
		echo "${cache_path}/data"
		return
	fi

	# check the remote location
	local curl_info_json write_out
	write_out='{
		"json":%{json},
		"header":%{header_json}
	}'
	curl_info_json=$(curl -sSLI -w "${write_out}" "$1" -o /dev/null)

	local code time type url
	code=$(jq -r '.json.http_code' <<<"${curl_info_json}")
	time=$(jq -r '(.header["last-modified"]|first) // (.header["date"]|first) // empty' <<<"${curl_info_json}")
	type=$(jq -r '.json.content_type' <<<"${curl_info_json}")
	if [[ ${use_redirected_location} == "YES" ]]; then
		url=$(jq -r '.json.url_effective' <<<"${curl_info_json}")
	else
		url=$1
	fi
	[[ ${code} == 200 ]] || error_exit "Failed to download $1"

	cache_path=$(location_to_cache_path "${url}")
	[[ -d ${cache_path} ]] || mkdir -p "${cache_path}"

	local needs_download=0
	[[ -f ${cache_path}/data ]] || needs_download=1
	[[ -f ${cache_path}/time && "$(<"${cache_path}/time")" == "${time}" ]] || needs_download=1
	[[ -f ${cache_path}/type && "$(<"${cache_path}/type")" == "${type}" ]] || needs_download=1
	if [[ ${needs_download} -eq 1 ]]; then
		curl_info_json=$(
			echo "downloading ${url}" >&2
			curl -SL -w "${write_out}" --no-clobber -o "${cache_path}/data" "${url}"
		)
		local filename
		code=$(jq -r '.json.http_code' <<<"${curl_info_json}")
		time=$(jq -r '(.header["last-modified"]|first) // (.header["date"]|first) // empty' <<<"${curl_info_json}")
		type=$(jq -r '.json.content_type' <<<"${curl_info_json}")
		url=$(jq -r '.json.url_effective' <<<"${curl_info_json}")
		filename=$(jq -r '.json.filename_effective' <<<"${curl_info_json}")
		[[ ${code} == 200 ]] || error_exit "Failed to download ${url}"
		[[ "${cache_path}/data" == "${filename}" ]] || mv "${filename}" "${cache_path}/data"
		# sha256.digest seems existing if expected digest is available. so, not creating it here.
		# sha256sum "${cache_path}/data" | awk '{print "sha256:"$1}' >"${cache_path}/sha256.digest"
		echo -n "${time}" >"${cache_path}/time"
	else
		touch "${cache_path}/time"
	fi
	[[ -f ${cache_path}/type ]] || echo -n "${type}" >"${cache_path}/type"
	[[ -f ${cache_path}/url ]] || echo -n "${url}" >"${cache_path}/url"
	echo "${cache_path}/data"
}

# Download the file to the cache directory without redirect and print the path.
function download_to_cache_without_redirect() {
	download_to_cache "$1" "NO"
}
