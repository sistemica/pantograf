package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/sistemica/pantograf/connector"
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
