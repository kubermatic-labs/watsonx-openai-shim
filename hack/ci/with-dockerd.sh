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

dockerd --host=unix:///var/run/docker.sock &
DOCKERD_PID=$!
trap 'kill "$DOCKERD_PID"' EXIT

for _ in {1..30}; do
  if docker info >/dev/null 2>&1; then
    exec "$@"
  fi
  sleep 1
done

echo "dockerd did not become ready within 30 seconds" >&2
exit 1
