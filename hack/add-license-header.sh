#!/usr/bin/env bash

# Copyright 2026 The Kubermatic Kubernetes Platform contributors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Adds the license header from hack/boilerplate/boilerplate.<ext>.txt to all
# given files and to all files within the given directories that don't have
# it yet. The template is chosen by file extension, or by file name for files
# without an extension (e.g. Dockerfile). Files without a matching template are
# skipped. YEAR within the template is replaced with the current year.
#
# Usage: hack/boilerplate/add-license-header.sh FILE_OR_DIR...

set -euo pipefail

BOILERPLATE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)/boilerplate"
YEAR="${YEAR:-$(date +%Y)}"

usage() {
  echo "Usage: $0 FILE_OR_DIR..." >&2
  exit 1
}

# Prints the template path for the given file, or nothing if there is none.
template_for() {
  local name ext
  name="$(basename "$1")"
  ext="${name##*.}"
  if [[ "${name}" == "${ext}" ]]; then
    ext="${name}" # no extension, e.g. Dockerfile
  fi
  if [[ -f "${BOILERPLATE_DIR}/boilerplate.${ext}.txt" ]]; then
    echo "${BOILERPLATE_DIR}/boilerplate.${ext}.txt"
  fi
}

# Returns 0 if the file already starts with the template (with any year),
# ignoring a leading shebang line.
has_header() {
  local file="$1" template="$2"
  diff -q "${template}" \
    <(sed '1{/^#!/d}' "${file}" | sed '/./,$!d' | head -n "$(wc -l <"${template}")" | sed -E '/Copyright/s/[0-9]{4}/YEAR/') \
    >/dev/null
}

add_header() {
  local file="$1" template tmp
  template="$(template_for "${file}")"
  if [[ -z "${template}" ]]; then
    return
  fi
  if has_header "${file}" "${template}"; then
    return
  fi

  tmp="$(mktemp)"
  {
    if head -n 1 "${file}" | grep -q '^#!'; then
      head -n 1 "${file}"
      echo
      sed "s/YEAR/${YEAR}/" "${template}"
      echo
      tail -n +2 "${file}" | sed '/./,$!d'
    else
      sed "s/YEAR/${YEAR}/" "${template}"
      echo
      cat "${file}"
    fi
  } >"${tmp}"
  # Preserve the file mode by writing into the existing file.
  cat "${tmp}" >"${file}"
  rm -f "${tmp}"
  echo "added license header: ${file}"
}

main() {
  if [[ $# -eq 0 ]]; then
    usage
  fi

  local target file
  for target in "$@"; do
    if [[ -d "${target}" ]]; then
      while IFS= read -r -d '' file; do
        add_header "${file}"
      done < <(find "${target}" \
        \( -name .git -o -name vendor -o -name _build \) -prune -o \
        -type f ! -name 'zz_generated.*' ! -name 'zz_generated_*' -print0)
    elif [[ -f "${target}" ]]; then
      add_header "${target}"
    else
      echo "no such file or directory: ${target}" >&2
      exit 1
    fi
  done
}

main "$@"
