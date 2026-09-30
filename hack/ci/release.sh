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

set -euo pipefail

cd "$(dirname "$0")"/../..

[ -f /etc/github/oauth ] || { echo "/etc/github/oauth not found; requires preset-kubermatic-bot-token" >&2; exit 1; }

TOOL_LOCATIONS="GORELEASER=$(which goreleaser) GOLANGCI_LINT=$(which golangci-lint) BOILERPLATE=$(which boilerplate)"

go version
boilerplate --version
golangci-lint --version
goreleaser --version

echo

make verify-file-headers lint test $TOOL_LOCATIONS

echo

git remote add origin git@github.com:kubermatic-labs/watsonx-openai-shim.git
export GITHUB_TOKEN=$(cat /etc/github/oauth | tr -d '\n')

echo
echo "Starting release process..."
echo

./hack/ci/with-dockerd.sh ./hack/ci/with-quay-login.sh make clean release $TOOL_LOCATIONS
