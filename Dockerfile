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

FROM docker.io/golang:1.26.4-alpine3.24 AS builder

ARG GIT_VERSION=v0.0.0-dev
ARG GIT_HEAD=<unknown>
ARG GOPROXY=
ARG GOCACHE_MINIO_ADDRESS=

COPY go.mod go.sum /build/
WORKDIR /build
RUN go mod download
COPY cmd/watsonx-openai-shim cmd/watsonx-openai-shim
COPY internal internal
ENV CGO_ENABLED=0
RUN go build -o watsonx-openai-shim \
	-ldflags '-s -w -extldflags "-static" '" \
		-X github.com/kubermatic-labs/watsonx-openai-shim/internal/version.gitVersion=$GIT_VERSION \
		-X github.com/kubermatic-labs/watsonx-openai-shim/internal/version.gitHead=$GIT_HEAD" \
	./cmd/watsonx-openai-shim


FROM gcr.io/distroless/static:nonroot
LABEL maintainer="support@kubermatic.com"
COPY --from=builder /build/watsonx-openai-shim /usr/local/bin/watsonx-openai-shim
USER nobody
ENTRYPOINT [ "watsonx-openai-shim" ]
