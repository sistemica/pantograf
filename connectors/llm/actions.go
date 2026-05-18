package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// ── list-models ───────────────────────────────────────────────────────────

type listModelsAction struct{}

func (listModelsAction) Name() string         { return "list-models" }
func (listModelsAction) DisplayName() string  { return "List models" }
func (listModelsAction) Description() string  { return "Available models on this endpoint." }
func (listModelsAction) Schema() connector.Schema { return connector.Schema{} }

func (listModelsAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/models", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── chat-completion ───────────────────────────────────────────────────────

type chatCompletionAction struct{}

func (chatCompletionAction) Name() string         { return "chat-completion" }
func (chatCompletionAction) DisplayName() string  { return "Chat completion" }
func (chatCompletionAction) Description() string  { return "OpenAI-compatible chat. Supports thinking-channel models (qwen, etc.) and tool calls (function calling)." }
func (chatCompletionAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "model", Kind: connector.FieldString, Required: true,
			Description: "Model id. e.g. qwen36-27b, nemotron-omni-30b, gpt-4o."},
		{Name: "prompt", Kind: connector.FieldLongText,
			Description: "Shortcut: single user message. Use `messages` for richer histories."},
		{Name: "system", Kind: connector.FieldLongText,
			Description: "Optional system prompt prepended to prompt shortcut."},
		{Name: "messages", Kind: connector.FieldLongText,
			Description: "Full OpenAI messages array as JSON, e.g. '[{\"role\":\"user\",\"content\":\"hi\"}]'. Overrides prompt+system."},
		{Name: "temperature", Kind: connector.FieldString,
			Description: "0.0–2.0. Empty = backend default."},
		{Name: "tools", Kind: connector.FieldLongText,
			Description: "OpenAI tools array as JSON. e.g. '[{\"type\":\"function\",\"function\":{\"name\":\"...\",\"description\":\"...\",\"parameters\":{...}}}]'."},
		{Name: "tool_choice", Kind: connector.FieldString,
			Description: "auto | none | required | {type:function,function:{name:...}} as JSON."},
		{Name: "enable_thinking", Kind: connector.FieldBool, Default: false,
			Description: "Pass `enable_thinking: true` in `chat_template_kwargs` (Qwen/vLLM extension). Other backends ignore."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return the full backend response instead of {text, reasoning, tool_calls, ...}."},
	}}
}

func (a chatCompletionAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	model := params.String("model")
	if model == "" {
		return nil, errors.New("model is required")
	}

	// Build messages — either explicit JSON or prompt+system shortcut.
	var messages []map[string]any
	if raw := params.String("messages"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &messages); err != nil {
			return nil, fmt.Errorf("messages JSON: %w", err)
		}
	} else {
		prompt := params.String("prompt")
		if prompt == "" {
			return nil, errors.New("either `prompt` or `messages` is required")
		}
		if sys := params.String("system"); sys != "" {
			messages = append(messages, map[string]any{"role": "system", "content": sys})
		}
		messages = append(messages, map[string]any{"role": "user", "content": prompt})
	}

	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if t := params.String("temperature"); t != "" {
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, fmt.Errorf("temperature: %w", err)
		}
		body["temperature"] = f
	}
	if t := params.String("tools"); t != "" {
		var tools []any
		if err := json.Unmarshal([]byte(t), &tools); err != nil {
			return nil, fmt.Errorf("tools JSON: %w", err)
		}
		body["tools"] = tools
	}
	if tc := params.String("tool_choice"); tc != "" {
		// tool_choice can be a string ("auto"/"none"/"required") or an object.
		// Try parsing as JSON object first; fall back to literal string.
		var asObj any
		if err := json.Unmarshal([]byte(tc), &asObj); err == nil {
			body["tool_choice"] = asObj
		} else {
			body["tool_choice"] = tc
		}
	}
	if params.Bool("enable_thinking") {
		// vLLM / Qwen3.5+ convention.
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": true}
	}

	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/chat/completions", body, &resp); err != nil {
		return nil, err
	}
	if params.Bool("raw") {
		return resp, nil
	}
	return simplifyResponse(resp), nil
}

// simplifyResponse extracts the common fields most consumers want from the
// OpenAI shape: assistant text, reasoning (when present), tool_calls,
// finish_reason, usage, model.
func simplifyResponse(resp map[string]any) map[string]any {
	out := map[string]any{
		"model": resp["model"],
		"usage": resp["usage"],
	}
	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		return out
	}
	first, _ := choices[0].(map[string]any)
	if first == nil {
		return out
	}
	out["finish_reason"] = first["finish_reason"]
	msg, _ := first["message"].(map[string]any)
	if msg == nil {
		return out
	}
	if c, ok := msg["content"].(string); ok && c != "" {
		out["text"] = c
	}
	// Reasoning channel — different backends use different keys.
	for _, k := range []string{"reasoning_content", "reasoning", "thinking"} {
		if v, ok := msg[k].(string); ok && v != "" {
			out["reasoning"] = v
			break
		}
	}
	if tc, ok := msg["tool_calls"]; ok && tc != nil {
		out["tool_calls"] = tc
	}
	return out
}

// ── embed ─────────────────────────────────────────────────────────────────

type embedAction struct{}

func (embedAction) Name() string         { return "embed" }
func (embedAction) DisplayName() string  { return "Embeddings" }
func (embedAction) Description() string  { return "Convert text to embedding vectors via the OpenAI /embeddings shape." }
func (embedAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "model", Kind: connector.FieldString, Required: true,
			Description: "Embedding model id. e.g. qwen3-embed, nomic-embed, text-embedding-3-small."},
		{Name: "input", Kind: connector.FieldLongText, Required: true,
			Description: "Text to embed. For batch, pass JSON array as a string."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return full backend response instead of {model, dim, embeddings[]}."},
	}}
}

func (a embedAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	model := params.String("model")
	if model == "" {
		return nil, errors.New("model is required")
	}
	rawInput := params.String("input")
	if rawInput == "" {
		return nil, errors.New("input is required")
	}

	// Accept either a JSON array or a bare string.
	var input any = rawInput
	var arr []string
	if err := json.Unmarshal([]byte(rawInput), &arr); err == nil {
		input = arr
	}

	body := map[string]any{"model": model, "input": input}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/embeddings", body, &resp); err != nil {
		return nil, err
	}
	if params.Bool("raw") {
		return resp, nil
	}

	out := map[string]any{
		"model": resp["model"],
		"usage": resp["usage"],
	}
	data, _ := resp["data"].([]any)
	embeddings := make([]any, 0, len(data))
	var dim int
	for _, d := range data {
		row, _ := d.(map[string]any)
		if row == nil {
			continue
		}
		emb, _ := row["embedding"].([]any)
		if dim == 0 {
			dim = len(emb)
		}
		embeddings = append(embeddings, emb)
	}
	out["dim"] = dim
	out["embeddings"] = embeddings
	out["count"] = len(embeddings)
	return out, nil
}

// ── chat-with-image ───────────────────────────────────────────────────────
//
// Convenience wrapper over chat-completion for the common vision case:
// "here is a local image, answer this prompt about it." Reads the file,
// base64-encodes it into a data: URL, builds the OpenAI vision messages
// shape, and POSTs to /chat/completions.
//
// Local files only — the `image` field is IsPath:true so the path whitelist
// applies. For remote URLs use chat-completion with a custom `messages`
// array instead (the escape hatch already supports any content shape).

type chatWithImageAction struct{}

func (chatWithImageAction) Name() string         { return "chat-with-image" }
func (chatWithImageAction) DisplayName() string  { return "Chat with image" }
func (chatWithImageAction) Description() string  { return "Multimodal chat. Reads a local image file and asks the model about it via OpenAI vision shape." }
func (chatWithImageAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "model", Kind: connector.FieldString, Required: true,
			Description: "Vision-capable model id. e.g. gpt-4o, qwen2.5-vl-7b, llava-1.6."},
		{Name: "prompt", Kind: connector.FieldLongText, Required: true,
			Description: "The question or instruction about the image."},
		{Name: "image", Kind: connector.FieldString, Required: true, IsPath: true,
			Description: "Local path to image (jpg/png/webp/gif). MIME sniffed from contents. Subject to PGF_ALLOWED_PATHS."},
		{Name: "system", Kind: connector.FieldLongText,
			Description: "Optional system prompt."},
		{Name: "detail", Kind: connector.FieldEnum, Default: "auto",
			Options: []connector.EnumOption{
				{Value: "low", Label: "Low (fast, ~85 tokens)"},
				{Value: "high", Label: "High (full resolution)"},
				{Value: "auto", Label: "Auto (backend decides)"},
			},
			Description: "Vision detail hint. Many local backends ignore."},
		{Name: "temperature", Kind: connector.FieldString,
			Description: "0.0–2.0. Empty = backend default."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return full backend response instead of simplified {text, ...}."},
	}}
}

func (a chatWithImageAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	model := params.String("model")
	prompt := params.String("prompt")
	image := params.String("image")
	if model == "" || prompt == "" || image == "" {
		return nil, errors.New("model, prompt, and image are required")
	}
	dataURL, err := imageToDataURL(image)
	if err != nil {
		return nil, err
	}

	var messages []map[string]any
	if sys := params.String("system"); sys != "" {
		messages = append(messages, map[string]any{"role": "system", "content": sys})
	}
	messages = append(messages, map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": "image_url", "image_url": map[string]any{
				"url":    dataURL,
				"detail": params.String("detail"),
			}},
		},
	})

	body := map[string]any{"model": model, "messages": messages}
	if t := params.String("temperature"); t != "" {
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, fmt.Errorf("temperature: %w", err)
		}
		body["temperature"] = f
	}

	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/chat/completions", body, &resp); err != nil {
		return nil, err
	}
	if params.Bool("raw") {
		return resp, nil
	}
	return simplifyResponse(resp), nil
}

// imageToDataURL reads a local image file and returns a data: URL ready
// for OpenAI's image_url content part. MIME is detected from the file's
// magic bytes via http.DetectContentType (covers jpg/png/gif/webp).
func imageToDataURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	mime := stdhttp.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		// Fall back to extension — DetectContentType returns
		// "application/octet-stream" for some WebP variants.
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png":
			mime = "image/png"
		case ".jpg", ".jpeg":
			mime = "image/jpeg"
		case ".gif":
			mime = "image/gif"
		case ".webp":
			mime = "image/webp"
		default:
			return "", fmt.Errorf("image: unsupported type (sniffed %q)", mime)
		}
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// ── chat-with-audio ───────────────────────────────────────────────────────
//
// Multimodal chat with audio input. Reads a local audio file, base64-encodes
// it, builds the OpenAI `input_audio` content shape, posts to /chat/completions.
// The backend model must accept audio in its multimodal channel
// (nemotron-omni-30b, gpt-4o-audio, qwen2-audio, ...).
//
// This is different from `transcribe`: nothing here is fixed to STT — the
// prompt steers what the model does with the audio. "Transcribe verbatim"
// gets you raw text; "summarize this voicemail in 3 bullets" gets you a
// summary; "is the speaker frustrated?" gets you sentiment. Use `transcribe`
// when you need fast, raw text from a dedicated Whisper backend.

type chatWithAudioAction struct{}

func (chatWithAudioAction) Name() string         { return "chat-with-audio" }
func (chatWithAudioAction) DisplayName() string  { return "Chat with audio" }
func (chatWithAudioAction) Description() string  { return "Multimodal chat. Reads a local audio file and asks the model about it via OpenAI input_audio shape. Backend must accept audio in chat-completions (nemotron-omni, gpt-4o-audio, qwen2-audio, ...)." }
func (chatWithAudioAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "model", Kind: connector.FieldString, Required: true,
			Description: "Audio-capable multimodal model id. e.g. nemotron-omni-30b, gpt-4o-audio-preview, qwen2-audio-7b."},
		{Name: "prompt", Kind: connector.FieldLongText, Required: true,
			Description: "Instruction about the audio. Anything: transcribe verbatim, summarize, classify, extract action items."},
		{Name: "audio", Kind: connector.FieldString, Required: true, IsPath: true,
			Description: "Local path to audio file. Format auto-detected from extension. Subject to PGF_ALLOWED_PATHS."},
		{Name: "format", Kind: connector.FieldString,
			Description: "Override format hint. Empty = auto from file extension. Common values: wav, mp3, m4a, ogg, flac, webm."},
		{Name: "system", Kind: connector.FieldLongText,
			Description: "Optional system prompt."},
		{Name: "temperature", Kind: connector.FieldString,
			Description: "0.0–2.0. Empty = backend default."},
		{Name: "enable_thinking", Kind: connector.FieldBool, Default: false,
			Description: "Pass `enable_thinking: true` in `chat_template_kwargs` (Qwen/vLLM extension)."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return full backend response instead of simplified {text, ...}."},
	}}
}

func (a chatWithAudioAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	model := params.String("model")
	prompt := params.String("prompt")
	audio := params.String("audio")
	if model == "" || prompt == "" || audio == "" {
		return nil, errors.New("model, prompt, and audio are required")
	}
	data, err := os.ReadFile(audio)
	if err != nil {
		return nil, fmt.Errorf("read audio: %w", err)
	}
	format := params.String("format")
	if format == "" {
		format = audioFormatFromPath(audio)
	}
	if format == "" {
		return nil, fmt.Errorf("audio: cannot infer format from extension %q; pass -p format=...", filepath.Ext(audio))
	}

	var messages []map[string]any
	if sys := params.String("system"); sys != "" {
		messages = append(messages, map[string]any{"role": "system", "content": sys})
	}
	messages = append(messages, map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": "input_audio", "input_audio": map[string]any{
				"data":   base64.StdEncoding.EncodeToString(data),
				"format": format,
			}},
		},
	})

	body := map[string]any{"model": model, "messages": messages}
	if t := params.String("temperature"); t != "" {
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, fmt.Errorf("temperature: %w", err)
		}
		body["temperature"] = f
	}
	if params.Bool("enable_thinking") {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": true}
	}

	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/chat/completions", body, &resp); err != nil {
		return nil, err
	}
	if params.Bool("raw") {
		return resp, nil
	}
	return simplifyResponse(resp), nil
}

// audioFormatFromPath maps a filename extension to the format hint the
// backend will see. Returns "" when the extension is unrecognised — at
// which point the caller can pass -p format=... explicitly. The mapping
// is conservative: container/codec specifics that backends disagree on
// (opus inside ogg, m4a vs mp4) are left as the user wrote them.
func audioFormatFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		return "wav"
	case ".mp3":
		return "mp3"
	case ".m4a":
		return "m4a"
	case ".mp4":
		return "mp4"
	case ".ogg":
		return "ogg"
	case ".opus":
		return "opus"
	case ".flac":
		return "flac"
	case ".webm":
		return "webm"
	}
	return ""
}

// ── transcribe ────────────────────────────────────────────────────────────
//
// Calls the OpenAI Whisper-compatible endpoint at /audio/transcriptions.
// Works against any backend implementing that shape (OpenAI, LiteLLM
// router with audio routing, vLLM-with-Whisper). For standalone Whisper
// servers without an LLM layer, use the `whisper` connector instead.

type transcribeAction struct{}

func (transcribeAction) Name() string         { return "transcribe" }
func (transcribeAction) DisplayName() string  { return "Transcribe audio" }
func (transcribeAction) Description() string  { return "Speech-to-text via the OpenAI /audio/transcriptions shape. Multipart upload of an audio file." }
func (transcribeAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "model", Kind: connector.FieldString, Required: true, Default: "whisper-1",
			Description: "Whisper-family model id. e.g. whisper-1 (OpenAI), Systran/faster-whisper-large-v3 (local)."},
		{Name: "audio", Kind: connector.FieldString, Required: true, IsPath: true,
			Description: "Local path to audio file (mp3/wav/m4a/ogg/webm/flac). Subject to PGF_ALLOWED_PATHS."},
		{Name: "language", Kind: connector.FieldString,
			Description: "ISO-639-1 hint (en, de, fr, ...). Empty = auto-detect."},
		{Name: "prompt", Kind: connector.FieldLongText,
			Description: "Optional context to prime the model (proper nouns, jargon)."},
		{Name: "temperature", Kind: connector.FieldString,
			Description: "0.0–1.0. Empty = backend default."},
		{Name: "verbose", Kind: connector.FieldBool, Default: false,
			Description: "When true, request verbose_json (segments + word timestamps when supported)."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return the full backend response instead of {text, ...}."},
	}}
}

func (a transcribeAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	model := params.String("model")
	audio := params.String("audio")
	if model == "" || audio == "" {
		return nil, errors.New("model and audio are required")
	}

	format := "json"
	if params.Bool("verbose") {
		format = "verbose_json"
	}
	fields := map[string]string{
		"model":           model,
		"response_format": format,
	}
	if l := params.String("language"); l != "" {
		fields["language"] = l
	}
	if p := params.String("prompt"); p != "" {
		fields["prompt"] = p
	}
	if t := params.String("temperature"); t != "" {
		fields["temperature"] = t
	}

	files := []httptr.FileField{{FieldName: "file", Path: audio}}

	var resp map[string]any
	if err := s.http.PostMultipart(ctx, "/audio/transcriptions", fields, files, &resp); err != nil {
		return nil, err
	}
	if params.Bool("raw") {
		return resp, nil
	}
	out := map[string]any{}
	if t, ok := resp["text"].(string); ok {
		out["text"] = t
	}
	for _, k := range []string{"language", "duration", "segments", "words"} {
		if v, ok := resp[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}
