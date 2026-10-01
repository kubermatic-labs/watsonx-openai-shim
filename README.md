# watsonx-openai-shim

watsonx-openai-shim serves the watsonx.ai chat API as the OpenAI chat completion API.
It supports streamed responses and tool calls.
It calls the watsonx.ai endpoints `/ml/v1/text/chat`, `/ml/v1/text/chat_stream`
and `/ml/v1/foundation_model_specs` directly.
On IBM Cloud Pak for Data (CPD), it also calls `/ml/v4/custom_foundation_models`.

## Endpoints

- `POST /v1/chat/completions`: chat completions, streamed and non-streamed.
- `GET /v1/models`: the models watsonx.ai offers.
- `GET /v1/models/{id}`: a single model watsonx.ai offers.
- `GET /healthz`: liveness check.

## Installation

### Helm

To install/upgrade watsonx-openai-shim via Helm, you must prepare the configuration within a `values.yaml` like this first (placeholders must be replaced with real values):
```yaml
watsonx:
  url: <WATSONX_URL>
  projectID: <WATSONX_PROJECTID>
  authMode: cpd # cpd for on-prem; iam for IBM Cloud
  credentials:
    apiKey: <CPD_APIKEY>
    username: <CPD_USERNAME>
```
(For more options, see the [default values.yaml](./charts/watsonx-openai-shim/values.yaml).)

Now you can install the Helm chart as follows (with `$VERSION` substituted with an actual release version):
```sh
helm upgrade --install watsonx-openai-shim oci://quay.io/kubermatic-labs/helm-charts/watsonx-openai-shim \
  --version=$VERSION \
  --values=values.yaml \
  --timeout=5m \
  --wait
```

### Container image

The container image is published under `quay.io/kubermatic-labs/watsonx-openai-shim`.

## CLI Configuration

| Flag | Description |
| --- | --- |
| `--watsonx-url` | watsonx.ai base URL, e.g. `https://eu-de.ml.cloud.ibm.com` or the CPD URL (required). |
| `--project-id` | watsonx.ai project ID sent with every request (required). |
| `--api-key` | The API key (required). |
| `--username` | The CPD username (required with `--auth-mode=cpd`). |
| `--auth-mode` | `iam` (IBM Cloud) or `cpd` (CPD). Defaults to `cpd`. |
| `--iam-host` | IBM Cloud IAM host, used with `--auth-mode=iam`. |
| `--ca-file` | PEM file with additional CA certificates to trust for watsonx.ai. |
| `--insecure-skip-tls-verify` | Skip TLS certificate verification for watsonx.ai. |
| `--default-max-tokens` | Token limit for requests that set none. Defaults to 1024. |
| `--upstream-connect-timeout` | Time to wait for watsonx.ai connection establishment. Defaults to 5s. |
| `--upstream-response-timeout` | Time to wait for watsonx.ai response headers. Defaults to 50s. |
| `--listen-addr` | HTTP listen address. Defaults to `:8080`. |
| `--log-level` | `debug`, `info`, `warn` or `error`. Defaults to `info`. |

Every flag can also be set as an environment variable with the `WXS_` prefix,
e.g. `--project-id` as `WXS_PROJECT_ID`.
Pass secrets such as `WXS_API_KEY` this way, so they do not show up in the process list.
The shim refuses to start when it finds an unknown `WXS_` environment variable.

## Logging

The shim assigns a random ID to every request and returns it in the `X-Request-Id` response header.
All log lines related to a request carry that ID as `requestID`.
At the `debug` level, the shim also logs every response and stream chunk from watsonx.ai
as received, including the model output.

## Authentication

The shim exchanges its credentials for a bearer token and shares it between requests.
It obtains the first token with the first request, so it starts even while watsonx.ai is unreachable.
It replaces a token 5 minutes before it expires, or at half its lifetime for short-lived tokens.
The expiry is taken from the IAM response or from the `exp` claim of the CPD token.
When watsonx.ai rejects a token with 401, the shim obtains a new one and retries the request once.

## Development

To build the container image, run:
```sh
make snapshot
```

To run the tests, run:
```sh
make test
```

To run the linter, run:
```sh
make lint
```

To list all supported build targets, run:
```sh
make help
```

## Translating the watsonx to the OpenAI API

The rules to translate from the watsonx.ai to the OpenAI API are documented [here](WATSONX2OPENAI.md).

## Limitations

- `/v1/models` lists all foundation models, including models without chat support.
- If the custom foundation models cannot be fetched on CPD, `/v1/models` fails with 502.
- Streamed tool calls are assembled based on the format [IBM's Node SDK](https://github.com/IBM/watsonx-ai-node-sdk) expects.
  However, the `ibm/ibm-defence-4-0-small` model returns tool calls as regular content chunks, formatted as Python-dict and prefixed with `<|tool_call|>` which the shim doesn't convert into a tool call but passes on to the client as is, e.g.: `<|tool_call|>{'name': 'bash', 'arguments': {'command': 'fortune | cowsay | lolcat'}}`.
  Also, in case of the `ibm/granite-8b-code-instruct` model, the response even says that `--tool-call-parser` must be specified on the server for that to work.
- Failed requests are not retried, except once for a rejected token.