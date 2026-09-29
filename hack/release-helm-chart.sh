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

usage() {
    echo "Usage: $0 CHART_DIR REGISTRY VERSION" >&2
    echo "    CHART_DIR - the directory containing the Helm chart" >&2
    echo "    REGISTRY  - the OCI registry to push the chart to; the chart name is appended" >&2
    echo "    VERSION   - the version of the chart to release" >&2
    echo "  Supported env vars:" >&2
    echo "    IMAGE_TAG - the image tag to set in the chart" >&2
    echo "    SNAPSHOT  - if set to true, the chart will not be uploaded" >&2
    exit 1
}

[ $# -eq 3 ] || usage

CHART_DIR="$1"
REGISTRY="$2"
VERSION="$3"
DEST_DIR="_build/chart"
CHART_NAME="$(yq e '.name' "$CHART_DIR/Chart.yaml")"
CHART_FILE="${DEST_DIR}/${CHART_NAME}-$VERSION.tgz"

: "${IMAGE_TAG:=}"
: "${SNAPSHOT:=false}"

echo "Starting Helm chart release (SNAPSHOT=$SNAPSHOT)"

mkdir -p "$DEST_DIR"

# Use a temp dir within the repo dir since snap-installed yq cannot see the host's /tmp
TMP_DIR="$(mktemp -d -p "$PWD/_build")"
TMP_CHART="$TMP_DIR/$CHART_NAME"
trap 'rm -rf "$TMP_DIR"' EXIT
cp -r "$CHART_DIR" "$TMP_CHART"

if [ "$IMAGE_TAG" ]; then
    echo "Setting container image tag to $IMAGE_TAG"
    IMAGE_TAG="$IMAGE_TAG" \
    yq -i e '.image.tag = strenv(IMAGE_TAG)' "$TMP_CHART/values.yaml"
fi

helm lint "$TMP_CHART" $([ ! -f "$TMP_CHART/values.lint.yaml" ] || echo "--values=$TMP_CHART/values.lint.yaml")

echo "Packaging Helm chart version $VERSION"
helm package "$TMP_CHART" --destination "$DEST_DIR" --version="$VERSION" --app-version="$VERSION"

if [ "$SNAPSHOT" = "true" ]; then
    echo "Skipping Helm chart upload since SNAPSHOT=true"
else
    echo "Releasing chart version $VERSION"
    helm push "$CHART_FILE" "oci://$REGISTRY"
fi
