{{/*
Copyright 2026 The Kubermatic Kubernetes Platform contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/}}

{{/* The base name for all resources created by this chart. */}}
{{- define "watsonx-openai-shim.name" -}}
{{- .Release.Name -}}
{{- end -}}

{{/* Common labels applied to all resources. */}}
{{- define "watsonx-openai-shim.labels" -}}
app.kubernetes.io/name: {{ include "watsonx-openai-shim.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/*
Image pull secret references. `imagePullSecrets` is used when it is
non-empty, otherwise `global.imagePullSecrets` - the chart's own list wins
outright rather than being merged. Entries of either list may be given as plain
strings or as `{name: ...}` maps; duplicates are removed.
*/}}
{{- define "watsonx-openai-shim.imagePullSecrets" -}}
{{- $configured := .Values.imagePullSecrets | default list -}}
{{- if not $configured -}}
{{- $configured = (.Values.global).imagePullSecrets | default list -}}
{{- end -}}
{{- $names := list -}}
{{- range $configured -}}
{{- if kindIs "map" . -}}
{{- $names = append $names .name -}}
{{- else -}}
{{- $names = append $names . -}}
{{- end -}}
{{- end -}}
{{- range uniq (compact $names) }}
- name: {{ . }}
{{- end }}
{{- end -}}

{{/*
The `sidecars` list, rendered as native sidecars (Kubernetes >= 1.29): an
initContainer with restartPolicy: Always starts before the regular containers,
keeps running alongside them, and is terminated once the last one exits. That
gives the gateway the ordering it needs: its tunnel is up before it serves.

Each entry is run through `tpl`, so a secretKeyRef (or any other field) may name
a Secret with a template expression and line up with an entry of `secrets`
written the same way.

A restartPolicy other than Always is rejected: it would turn the sidecar into a
plain init container, which blocks the gateway until it exits.

Emits nothing when no sidecars are configured, so callers guard the include with
`with` rather than indenting a blank line.
*/}}
{{- define "watsonx-openai-shim.sidecarContainers" -}}
{{- with .Values.deployment.sidecars }}
initContainers:
  {{- range $i, $sidecar := . }}
  {{- if and (hasKey $sidecar "restartPolicy") (ne (toString $sidecar.restartPolicy) "Always") }}
  {{- $name := $sidecar.name | default (printf "#%d" $i) }}
  {{- fail (printf "deployment.sidecars[%d] (%s) has restartPolicy %q, but a sidecar's restartPolicy must be Always" $i $name (toString $sidecar.restartPolicy)) }}
  {{- end }}
  - restartPolicy: Always
    {{- tpl (omit $sidecar "restartPolicy" | toYaml) $ | nindent 4 }}
  {{- end }}
{{- end }}
{{- end -}}

{{/* Labels used to select the gateway Deployment's pods. */}}
{{- define "watsonx-openai-shim.deployment.selectorLabels" -}}
app.kubernetes.io/name: {{ include "watsonx-openai-shim.name" . }}
app.kubernetes.io/component: gateway
{{- end -}}

{{/*
Fully qualified gateway image reference. The registry is taken from
`image.registry` when set, otherwise `global.imageRegistry`,
otherwise the chart's own default - so a global setting applies unless this
chart was told a specific registry.

That default lives here rather than in values.yaml on purpose: a non-empty
default there is indistinguishable from a user-supplied value, and would shadow
`global.imageRegistry` in every install.
*/}}
{{- define "watsonx-openai-shim.defaultRegistry" -}}
quay.io/kubermatic-labs
{{- end -}}

{{- define "watsonx-openai-shim.image" -}}
{{- $image := .Values.image -}}
{{- $registry := $image.registry | default (.Values.global).imageRegistry | default (include "watsonx-openai-shim.defaultRegistry" .) -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $image.repository ($image.tag | toString) -}}
{{- else -}}
{{- printf "%s:%s" $image.repository ($image.tag | toString) -}}
{{- end -}}
{{- end -}}

{{/*
Name of the Secret holding the CPD username and API key the gateway
authenticates with: `watsonx.credentials.secretName` when an existing Secret was
named, otherwise the name of the Secret the chart creates from the inline
username and apiKey. The configured name is taken literally.
*/}}
{{- define "watsonx-openai-shim.credentialsSecretName" -}}
{{- with .Values.watsonx.credentials.secretName -}}
{{- . -}}
{{- else -}}
{{- printf "%s-watsonx-credentials" (include "watsonx-openai-shim.name" .) -}}
{{- end -}}
{{- end -}}

{{/*
True when the credential is given inline, which is what makes the chart create
the Secret itself rather than referring to an existing one.
*/}}
{{- define "watsonx-openai-shim.credentialsInline" -}}
{{- if and .Values.watsonx.credentials.apiKey (or .Values.watsonx.credentials.username (eq .Values.watsonx.authMode "iam")) -}}
true
{{- end -}}
{{- end -}}

{{/*
Validates the values the gateway Deployment cannot run without.
*/}}
{{- define "watsonx-openai-shim.deployment.validate" -}}
{{- if not (has (lower (toString .Values.logLevel)) (list "debug" "info" "warn" "error")) -}}
{{- fail "logLevel must be one of debug, info, warn or error" -}}
{{- end -}}
{{- if not (has (toString .Values.watsonx.authMode) (list "cpd" "iam")) -}}
{{- fail "watsonx.authMode must be one of cpd or iam" -}}
{{- end -}}
{{- if not .Values.watsonx.url -}}
{{- fail "watsonx.url is required (the CPD base URL, e.g. https://watsonx.example.data.center)" -}}
{{- end -}}
{{- if not .Values.watsonx.projectID -}}
{{- fail "watsonx.projectID is required (the watsonx.ai project UUID sent with every request)" -}}
{{- end -}}
{{- $creds := .Values.watsonx.credentials -}}
{{- $inline := or $creds.username $creds.apiKey -}}
{{- if and $creds.secretName $inline -}}
{{- fail "watsonx.credentials: set either secretName or username plus apiKey, not both - with both it is ambiguous which Secret the gateway would read" -}}
{{- end -}}
{{- if not (or $creds.secretName (include "watsonx-openai-shim.credentialsInline" .)) -}}
{{- fail "watsonx.credentials: set secretName to use an existing Secret, or set apiKey (and username in case authMode=cpd) to have the chart create one" -}}
{{- end -}}
{{- end -}}
