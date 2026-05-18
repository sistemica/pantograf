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
| `chat-completion` | Text chat. Thinking channel + tool calls pass through. |
| `chat-with-image` | **Vision.** Local image → base64 data URL → OpenAI vision messages → `/chat/completions`. Steerable by prompt (extract text, classify, return JSON, anything). |
| `chat-with-audio` | **Audio-via-chat.** Local audio → base64 → OpenAI `input_audio` messages → `/chat/completions`. Requires a backend that *actually* serves audio inputs in chat (see §Multimodal — what works where). |
| `embed` | `/embeddings`. Accepts a string or a JSON array as `input`. |
| `transcribe` | **Speech-to-text.** Multipart upload to `/audio/transcriptions`. Fast, raw text. No prompt steering — use this for "voice note → transcript → downstream LLM" pipelines. |

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

## Multimodal — what works where

The connector exposes three multimodal entry points; which one to reach for depends on **what the agent needs** and **what the backend actually serves**. The label "multimodal" on a model isn't a guarantee — the *serving layer* (vLLM, llama.cpp, mlx) decides which modalities are wired up.

### Decision table — by intent

| Goal | Action | Why |
|---|---|---|
| Raw transcript, fast, cheap | `transcribe` | Dedicated STT, no prompt budget for audio decoding, one round trip. |
| "Voice memo → transcript → another LLM step" | `transcribe`, then `chat-completion` | Each step does one thing well; transcripts are short, so the second call is cheap. |
| "Summarize this voicemail in 3 bullets" | `chat-with-audio` | End-to-end in one shot — model reasons about the audio directly. |
| "Is the speaker frustrated? What action items?" | `chat-with-audio` | STT throws away tone; multimodal models can use it. |
| OCR / read text from screenshot | `chat-with-image` -p prompt="Quote all visible text." | Prompt steers exactly what to extract. |
| Structured extraction from invoice / receipt | `chat-with-image` -p prompt="Return JSON {date,total,currency,vendor}." | Vision + structured-output prompt is one call. |
| Anything that needs `tool_calls` | `chat-completion` (text only) | Most vision/audio models don't reliably emit tool calls — separate steps. |

### Backend compatibility (Strix Halo + Mac Studio proxy as of 2026-05)

| Backend / serving stack | Vision via chat | Audio via chat | STT via `/audio/transcriptions` |
|---|---|---|---|
| llama.cpp (Strix Halo: nemotron-omni, qwen35-9b-vision, qwen36-27b) | ✅ via mmproj | ❌ "audio input is not supported" — llama.cpp has no audio decoder yet | n/a |
| vllm-mlx (Mac Studio: qwen36, qwen35-ablit) | depends on model | depends on model | n/a |
| MLX Whisper (Mac Studio, bridged) | n/a | n/a | ✅ via the `/v1/audio/transcriptions` route in the proxy |
| OpenAI direct | ✅ gpt-4o | ✅ gpt-4o-audio-preview | ✅ whisper-1 |
| vLLM-Whisper / faster-whisper-server | n/a | n/a | ✅ |

**Practical rule for this stack**: vision goes through `chat-with-image` against `qwen35-9b` or `nemotron-omni-30b`. STT goes through `transcribe` (bridged to MLX Whisper). `chat-with-audio` is plumbed and works against compatible backends but is **not currently functional against the local proxy** — llama.cpp doesn't have audio support compiled in yet. The day a backend with audio-in-chat lands, the action works without any code change.

### Format / file constraints

- `chat-with-image`: MIME sniffed from contents (jpg, png, gif, webp). Path subject to `PGF_ALLOWED_PATHS`.
- `chat-with-audio`: format derived from extension (wav, mp3, m4a, ogg, opus, flac, webm). Override with `-p format=...`. Path subject to `PGF_ALLOWED_PATHS`.
- `transcribe`: through the local proxy, the MLX upstream **filters by filename extension** (`.m4a, .webm, .ogg, .wav, .flac, .mp3`). `.opus` (codec) inside a file named `*.opus` is rejected even though `.opus`-in-`.ogg` is accepted — rename to `.ogg` and the same bytes go through.

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

# Vision: describe a screenshot
pgf run llm/proxy chat-with-image \
  -p model=qwen35-9b \
  -p prompt="What text is visible in this image?" \
  -p image=/tmp/screenshot.png

# Vision: structured extraction
pgf run llm/proxy chat-with-image \
  -p model=qwen35-9b \
  -p prompt='Return JSON only: {brand, model, capacity_gb}.' \
  -p image=/tmp/product.jpg

# Audio: transcribe via Whisper bridge (raw text, fast)
pgf run llm/proxy transcribe \
  -p model=whisper-1 -p audio=/tmp/note.ogg -p language=de

# Audio via chat (requires audio-capable backend — see §Multimodal)
pgf run llm/proxy chat-with-audio \
  -p model=gpt-4o-audio-preview \
  -p audio=/tmp/voicemail.mp3 \
  -p prompt="Summarize in 3 bullets. Note any callbacks requested."
```

## Known gaps

- No streaming (`stream: true`) — chunked SSE responses aren't in the
  shape `pgf run` outputs. A future `pgf stream` could expose this.
- No `pgf agent` command yet — see top-level README for the deferred
  agent-loop runner that would consume `chat-completion` + the action
  registry to execute tool calls against other connectors.
