# Translating watsonx.ai to OpenAI API

The watsonx.ai chat API is close to the OpenAI format, so most fields are passed on unchanged.

## Requests

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

## Responses

- `model_id` becomes `model`.
- The `time_limit` finish reason becomes `length`.
  The `cancelled` and `error` finish reasons are reported as errors.
- `reasoning_content` is passed on.
  It is no OpenAI field, but many OpenAI-compatible clients understand it.
- watsonx.ai streams tool calls without an `index`.
  The shim adds it: a tool call with an ID starts a new call,
  and fragments without an ID continue the current one.
- In streams, usage is sent as a final chunk only if the client set `stream_options.include_usage`.

## Models

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

## Errors

- Client errors reported by watsonx, e.g. an unknown model, keep their status code.
- Authentication errors and all other upstream failures are reported as 502.
- An error during a stream is sent as an error event, followed by `data: [DONE]`.
- A client disconnect cancels the watsonx.ai request.
