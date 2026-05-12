# connectors/llm

OpenAI-compatible LLM connector. Talks to anything that speaks
`/v1/chat/completions`: your local proxy, OpenAI, Together, Groq, vLLM,
Ollama, etc. Bearer-token auth, configurable base URL via preset.

One pantograf instance = one backend. Multiple coexist:

```
llm/proxy    → http://192.168.1.125:4000/v1   (Strix+Mac aggregate proxy)
llm/openai   → https://api.openai.com/v1
```

## Actions

| Name | Description |
|---|---|
| `list-models` | Available models on the endpoint. Used as Validate probe. |
| `chat-completion` | Full chat surface. Thinking channel + tool calls pass through. |
| `embed` | `/embeddings`. Accepts a string or a JSON array as `input`. |

## Credential

| Field | Required | Notes |
|---|---|---|
| `api_key` | yes | `FieldSecret`. Sent as `Authorization: Bearer <key>`. For the local proxy use `sk-1234`. |
| `api_base` | yes | `/v1`-rooted base URL. Trailing slash trimmed. |

### Presets

| Name | api_base |
|---|---|
| Local LLM proxy | `http://192.168.1.125:4000/v1` |
| OpenAI | `https://api.openai.com/v1` |
| Custom | manual |

`Validate` calls `GET /models` and reports the number of available models
plus the first one's id.

## Reasoning / thinking channel

Many backends now expose a separate reasoning track:

- **Qwen3 / vLLM** — `enable_thinking: true` in `chat_template_kwargs`,
  response includes `reasoning_content` on the assistant message.
- **DeepSeek / Reflection** — `reasoning_content` directly.
- **OpenAI o1/o3** — handled internally; not exposed to the API client.

The connector handles all three by passing through. Set
`-p enable_thinking=true` for Qwen-family models. The simplified
response shape surfaces it as a `reasoning` field alongside `text`:

```json
{
  "text": "Berlin is the capital of Germany.",
  "reasoning": "The user asked for the capital. Germany's capital is Berlin. …",
  "finish_reason": "stop",
  "model": "qwen36-27b",
  "usage": {...}
}
```

## Tool calls (function calling)

OpenAI-style. Pass `tools` as a JSON array; the response surfaces
`tool_calls` when the model wants to invoke one:

```bash
pgf run llm/proxy chat-completion \
  -p model=qwen36-27b \
  -p prompt="What's the weather in Berlin?" \
  -p tools='[{
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Current weather for a city",
      "parameters": {
        "type": "object",
        "properties": {"city": {"type": "string"}},
        "required": ["city"]
      }
    }
  }]'
```

Returns:

```json
{
  "tool_calls": [{
    "id": "...",
    "type": "function",
    "function": {"name": "get_weather", "arguments": "{\"city\":\"Berlin\"}"}
  }],
  "finish_reason": "tool_calls"
}
```

The agent loop is up to the caller — execute each call, append
`role: "tool"` messages, send another chat-completion. See the
top-level README for a 30-line bash example.

## Why no `max_tokens` parameter

Intentionally not in the schema. Reasoning models hide tokens in the
thinking channel; a hard cap that doesn't know about reasoning produces
`finish_reason: "length"` with **empty visible content** (the cap was
spent on reasoning before the model could write its answer). Picking a
"safe" default is impossible — let the model decide when it's done.

If you genuinely need to cap output for a non-reasoning model, build the
request body explicitly and use the `messages` parameter to pass a full
custom shape, or open a follow-up to add the field back with a clearer
description.

## Timeouts

The connector's underlying HTTP client uses a **10-minute** per-request
timeout — long enough for the longest reasoning runs we've seen. Ctx
cancellation still works for immediate shutdown. Don't reduce this for
fast endpoints; it doesn't cost anything when the response comes back
quickly.

## Usage

```bash
# Setup
pgf connect llm proxy   # wizard, pick "Local LLM proxy" preset, paste sk-1234

# Simple call
pgf run llm/proxy chat-completion \
  -p model=qwen36-27b -p prompt="What is the capital of Germany?"

# With thinking
pgf run llm/proxy chat-completion \
  -p model=qwen36-27b -p prompt="Plan a route from A to B." \
  -p enable_thinking=true

# System prompt + multi-turn via messages
pgf run llm/proxy chat-completion \
  -p model=qwen36-27b \
  -p messages='[
    {"role":"system","content":"You are terse."},
    {"role":"user","content":"hi"},
    {"role":"assistant","content":"hi."},
    {"role":"user","content":"what model?"}
  ]'

# Embedding
pgf run llm/proxy embed -p model=qwen3-embed -p input="hello world"
pgf run llm/proxy embed -p model=qwen3-embed -p input='["hello","world","foo"]'

# Raw backend response (skip simplification)
pgf run llm/proxy chat-completion -p model=qwen36-27b -p prompt=hi -p raw=true
```

## Known gaps

- No streaming (`stream: true`) — chunked SSE responses aren't in the
  shape `pgf run` outputs. A future `pgf stream` could expose this.
- No `pgf agent` command yet — see top-level README for the deferred
  agent-loop runner that would consume `chat-completion` + the action
  registry to execute tool calls against other connectors.
