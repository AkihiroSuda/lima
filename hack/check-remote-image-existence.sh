#!/bin/bash
set -eu -o pipefail

# Get the directory of the script
script_dir=$(dirname "$0")

result=0
for template in $(realpath "${script_dir}"/../examples/*.yaml "${script_dir}"/./test-templates/*.yaml | sort -u); do
	for location in $(yq eval '.images[].location' "${template}"); do
		if [[ ${location} == http* ]]; then
			response=$(curl -L -s -I -o /dev/null -w "%{http_code}" "${location}")
			if [[ ${response} != "200" ]]; then
				line=$(grep -n "${location}" "${template}" | cut -d ':' -f 1)
				echo "::error file=${template},line=${line}::response: ${response} for ${location}"
				result=1
			else
				echo "response: ${response} for ${location}"
			fi
		fi
	done
done

exit "${result}"
