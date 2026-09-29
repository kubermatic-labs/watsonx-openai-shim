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

## Configuration

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

## Translation

The watsonx.ai chat API is close to the OpenAI format, so most fields are passed on unchanged.

Requests:

- `model` becomes `model_id`, and the configured project ID is added.
- `developer` messages become `system` messages, since watsonx.ai has no developer role.
- Messages keep only `role`, `content`, `name`, `tool_calls` and `tool_call_id`.
  `null` content is dropped.
  Content parts of non-user messages are joined into a string,
  since watsonx.ai accepts content parts only in user messages.
- `tool_choice` becomes `tool_choice_option` for `auto`, `none` and `required`,
  and stays `tool_choice` for a named function.
- `max_completion_tokens` and `max_tokens` become `max_tokens`,
  since older watsonx.ai releases do not know `max_completion_tokens`.
  Without either, `--default-max-tokens` applies.
- `tools`, `temperature`, `top_p`, `frequency_penalty`, `presence_penalty`, `stop`, `n`, `seed`,
  `response_format`, `logprobs`, `top_logprobs` and `logit_bias` are passed on.
  Other fields are ignored.

Responses:

- `model_id` becomes `model`.
- The `time_limit` finish reason becomes `length`.
  The `cancelled` and `error` finish reasons are reported as errors.
- `reasoning_content` is passed on.
  It is no OpenAI field, but many OpenAI-compatible clients understand it.
- watsonx.ai streams tool calls without an `index`.
  The shim adds it: a tool call with an ID starts a new call,
  and fragments without an ID continue the current one.
- In streams, usage is sent as a final chunk only if the client set `stream_options.include_usage`.

Models:

- The models are fetched from `/ml/v1/foundation_model_specs?version=2024-05-31` on every request,
  following all result pages.
- Withdrawn models are left out, using the watsonx.ai filter `!lifecycle_withdrawn`.
  Deprecated and constricted models are listed, since they still serve inference requests.
  The filter does not apply to the custom foundation models.
- With `--auth-mode=cpd`, the custom foundation models deployed on the CPD instance are added,
  fetched from `/ml/v4/custom_foundation_models?version=2024-05-01`.
  They are described in the same format and mapped the same way.
- `id` is taken from `model_id`, and `owned_by` is set to `<provider> / <source>`.
- `created` is the current time, since watsonx.ai reports no creation time.
- The non-OpenAI fields `description`, `max_completion_tokens` and `token_limits` are added.
  `description` is the `short_description` followed by the supported `task_ids`.
  `max_completion_tokens` and `token_limits` are taken from `model_limits`.

Errors:

- Client errors reported by watsonx, e.g. an unknown model, keep their status code.
- Authentication errors and all other upstream failures are reported as 502.
- An error during a stream is sent as an error event, followed by `data: [DONE]`.
- A client disconnect cancels the watsonx.ai request.

## Limitations

- `/v1/models` lists all foundation models, including models without chat support.
- If the custom foundation models cannot be fetched on CPD, `/v1/models` fails with 502.
- Streamed tool calls are assembled based on the format [IBM's Node SDK](https://github.com/IBM/watsonx-ai-node-sdk) expects.
  However, the `ibm/ibm-defence-4-0-small` model returns tool calls as regular content chunks, formatted as Python-dict and prefixed with `<|tool_call|>` which the shim doesn't convert into a tool call but passes on to the client as is, e.g.: `<|tool_call|>{'name': 'bash', 'arguments': {'command': 'fortune | cowsay | lolcat'}}`.
  Also, in case of the `ibm/granite-8b-code-instruct` model, the response even says that `--tool-call-parser` must be specified on the server for that to work.
- Failed requests are not retried, except once for a rejected token.

## Development

To build the container image, run:
```sh
make container
```

To run the tests, run:
```sh
make test
```

To run the linter, run:
```sh
make lint
```
