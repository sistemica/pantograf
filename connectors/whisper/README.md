# connectors/whisper

Dedicated speech-to-text connector. Targets standalone OpenAI-compatible
Whisper servers — faster-whisper-server, vLLM-Whisper, whisper.cpp's
HTTP front-end. Same `/audio/transcriptions` shape as OpenAI; this
connector exists because the deployment is typically separate from the
LLM proxy (often unauthenticated, on a different host).

## When to use this vs `llm transcribe` vs `llm chat-with-audio`

All three give you audio → text. Pick by *what reaches the model* and
*what gets back out*:

| You want… | Use | Why |
|---|---|---|
| Raw transcript, lowest latency, dedicated STT box | `whisper/<instance>` | One credential per Whisper server, presets included, optional `vad_filter`. |
| Raw transcript via your unified LLM proxy | `llm/<instance> transcribe` | Reuses the same key + base URL you already manage. Requires the proxy to expose `/v1/audio/transcriptions` (e.g. nginx route bridged to a Whisper backend). |
| Audio → reasoning in one shot ("summarize", "extract action items", "classify tone") | `llm/<instance> chat-with-audio` | Multimodal model handles audio + prompt directly. Requires a backend that *actually serves* audio in chat — see llm README §Multimodal. |

Functionally `whisper transcribe` and `llm transcribe` send the **same
HTTP request** to the same `/audio/transcriptions` shape. The split is
about *how you organise credentials*: a Whisper server with its own URL
and key gets its own instance; a Whisper bridged behind your LLM proxy
piggybacks on the existing `llm/<instance>`.

## Actions

| Name | Description |
|---|---|
| `list-models` | `GET /models`. Used as Validate probe. |
| `transcribe` | `POST /audio/transcriptions` (source language preserved). |
| `translate` | `POST /audio/translations` (auto-translates to English). |

## Credential

| Field | Required | Notes |
|---|---|---|
| `api_base` | yes | /v1-rooted URL. Trailing slash trimmed. |
| `api_key` | no | `FieldSecret`. Sent as `Authorization: Bearer <key>` when present. Most local servers don't need it. |
| `default_model` | no | Pre-fills `model` on transcribe/translate when omitted. |

### Presets

| Name | api_base |
|---|---|
| faster-whisper-server (local) | `http://localhost:8000/v1` |
| OpenAI Whisper | `https://api.openai.com/v1` |
| Custom | manual |

For the Sistemica lab specifically: the unified proxy at
`http://192.168.1.125:4000/v1` has `/audio/transcriptions` routed to MLX
Whisper on Mac Studio. You can point this connector at the proxy URL
(api_key `sk-1234`) and it works identically — verified live.

## Usage

```bash
pgf connect whisper local        # wizard; pick faster-whisper preset; leave api_key blank
pgf run whisper/local transcribe \
  -p model=Systran/faster-whisper-large-v3 \
  -p audio=/tmp/note.m4a \
  -p language=de

# Verbose JSON with segment timestamps
pgf run whisper/local transcribe -p audio=/tmp/call.wav -p verbose=true

# Translate German audio to English text
pgf run whisper/local translate -p audio=/tmp/de-clip.mp3
```

Output (default, non-verbose):

```json
{ "text": "..." }
```

Verbose adds `language`, `duration`, `segments[]` (and `words[]` for
backends that emit word-level timestamps).

## Path whitelist

`audio` is `IsPath: true` — when `PGF_ALLOWED_PATHS` is set, the audio
file must resolve inside one of the allowed roots. Same gate as the
email connector's `attachments`. See pantograf's `SECURITY.md`.

## Backend quirks

Different Whisper implementations enforce slightly different rules at
their `/audio/transcriptions` boundary. Confirmed:

- **MLX Whisper** (the bridged backend in the Sistemica proxy): rejects
  files whose **filename extension** is `.opus` with `{"detail":"Unsupported file type. Allowed: .m4a, .webm, .ogg, .wav, .flac, .mp3"}` — even when the codec inside is fine. Workaround: rename `*.opus` to `*.ogg`; the same bytes go through.
- **MLX Whisper**: ignores the OpenAI `prompt`, `temperature`, and
  `response_format` form fields. The connector still sends them; they're just dropped by the upstream.
- **faster-whisper-server**: supports the `vad_filter` extension. The
  connector exposes it as `-p vad_filter=true`. Other backends ignore.
- **OpenAI** vs **everyone else**: the official API supports `response_format=srt|vtt|verbose_json`, returning non-JSON bodies for the subtitle formats. The connector currently requests `json` or `verbose_json` only — pass `-p raw=true` for the full verbose_json shape.

## Known gaps

- No streaming endpoint — Whisper API has no SSE for partial
  transcripts. (Some servers expose WebSocket streaming on
  non-OpenAI-compatible paths; not handled here.)
- No diarization / speaker labels. Out of scope for the OpenAI shape.
- Single file per call. Batch via shell loop.
- No subtitle output (`srt`/`vtt`) — the JSON-only fast path keeps the
  connector simple. Add when an agent workflow actually needs it.
