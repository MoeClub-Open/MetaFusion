package assistant

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

func validProtocol(protocol string) bool {
	switch protocol {
	case "openai_compatible", "openai_responses", "anthropic_messages", "gemini_generate_content":
		return true
	}
	return false
}

func completionEndpoint(base, protocol, model string) (string, error) {
	base = strings.TrimRight(base, "/")
	var suffix string
	switch protocol {
	case "openai_compatible":
		suffix = "/chat/completions"
	case "openai_responses":
		suffix = "/responses"
	case "anthropic_messages":
		suffix = "/messages"
	case "gemini_generate_content":
		suffix = "/models/" + url.PathEscape(strings.TrimPrefix(model, "models/")) + ":generateContent"
	default:
		return "", fmt.Errorf("assistant_invalid_protocol")
	}
	if strings.HasSuffix(base, suffix) {
		return base, nil
	}
	return base + suffix, nil
}

func providerOutput(message Message, binding string) ([]json.RawMessage, bool) {
	if message.ProviderContext == nil || message.ProviderContext.Binding != binding {
		return nil, false
	}
	var output []json.RawMessage
	if json.Unmarshal(message.ProviderContext.Output, &output) != nil || len(output) == 0 {
		return nil, false
	}
	return output, true
}

func protocolPayload(protocol, binding string, r Reservation, in CompletionRequest) ([]byte, error) {
	if protocol == "openai_compatible" {
		return completionPayload(r, in)
	}
	var payload map[string]any
	switch protocol {
	case "openai_responses":
		items := []any{}
		for _, message := range in.Messages {
			if message.Role == "assistant" {
				if output, ok := providerOutput(message, binding); ok {
					for _, item := range output {
						items = append(items, item)
					}
					continue
				}
			}
			if message.Role == "tool" {
				items = append(items, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
				continue
			}
			if message.Content != "" {
				items = append(items, map[string]any{"role": message.Role, "content": message.Content})
			}
			for _, call := range message.ToolCalls {
				items = append(items, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
			}
		}
		payload = map[string]any{"model": r.Model.UpstreamModel, "input": items, "max_output_tokens": r.OutputTokens, "store": false, "stream": false, "include": []string{"reasoning.encrypted_content"}}
		if len(in.Tools) > 0 {
			tools := []any{}
			for _, tool := range in.Tools {
				tools = append(tools, map[string]any{"type": "function", "name": tool.Function.Name, "description": tool.Function.Description, "parameters": tool.Function.Parameters, "strict": false})
			}
			payload["tools"] = tools
		}
	case "anthropic_messages":
		messages, system := []map[string]any{}, []string{}
		for _, message := range in.Messages {
			if message.Role == "system" {
				system = append(system, message.Content)
				continue
			}
			role := message.Role
			parts := []any{}
			if role == "tool" {
				role = "user"
				parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": message.Content})
			} else if output, ok := providerOutput(message, binding); role == "assistant" && ok {
				for _, item := range output {
					parts = append(parts, item)
				}
			} else {
				if message.Content != "" {
					parts = append(parts, map[string]any{"type": "text", "text": message.Content})
				}
				for _, call := range message.ToolCalls {
					parts = append(parts, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": json.RawMessage(call.Function.Arguments)})
				}
			}
			messages = appendProtocolParts(messages, role, "content", parts)
		}
		payload = map[string]any{"model": r.Model.UpstreamModel, "system": strings.Join(system, "\n\n"), "messages": messages, "max_tokens": r.OutputTokens, "stream": false}
		if len(in.Tools) > 0 {
			tools := []any{}
			for _, tool := range in.Tools {
				tools = append(tools, map[string]any{"name": tool.Function.Name, "description": tool.Function.Description, "input_schema": tool.Function.Parameters})
			}
			payload["tools"] = tools
		}
	case "gemini_generate_content":
		contents, system := []map[string]any{}, []any{}
		names, providerIDs := map[string]string{}, map[string]bool{}
		for _, message := range in.Messages {
			if message.Role == "system" {
				system = append(system, map[string]any{"text": message.Content})
				continue
			}
			for _, call := range message.ToolCalls {
				names[call.ID] = call.Function.Name
			}
			role, parts := "user", []any{}
			if message.Role == "tool" {
				name := names[message.ToolCallID]
				if name == "" {
					return nil, fmt.Errorf("assistant_invalid_tool_result")
				}
				var result map[string]any
				if json.Unmarshal([]byte(message.Content), &result) != nil || result == nil {
					result = map[string]any{"output": message.Content}
				}
				response := map[string]any{"name": name, "response": result}
				if providerIDs[message.ToolCallID] {
					response["id"] = message.ToolCallID
				}
				parts = append(parts, map[string]any{"functionResponse": response})
			} else if message.Role == "assistant" {
				role = "model"
				if output, ok := providerOutput(message, binding); ok {
					for _, item := range output {
						parts = append(parts, item)
						var part struct {
							FunctionCall struct {
								ID string `json:"id"`
							} `json:"functionCall"`
						}
						if json.Unmarshal(item, &part) == nil && part.FunctionCall.ID != "" {
							providerIDs[part.FunctionCall.ID] = true
						}
					}
				} else {
					if message.Content != "" {
						parts = append(parts, map[string]any{"text": message.Content})
					}
					for _, call := range message.ToolCalls {
						parts = append(parts, map[string]any{"functionCall": map[string]any{"name": call.Function.Name, "args": json.RawMessage(call.Function.Arguments)}})
					}
				}
			} else if message.Content != "" {
				parts = append(parts, map[string]any{"text": message.Content})
			}
			contents = appendProtocolParts(contents, role, "parts", parts)
		}
		payload = map[string]any{"contents": contents, "systemInstruction": map[string]any{"parts": system}, "generationConfig": map[string]any{"maxOutputTokens": r.OutputTokens, "candidateCount": 1}}
		if len(in.Tools) > 0 {
			declarations := []any{}
			for _, tool := range in.Tools {
				declarations = append(declarations, map[string]any{"name": tool.Function.Name, "description": tool.Function.Description, "parametersJsonSchema": tool.Function.Parameters})
			}
			payload["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
		}
	default:
		return nil, fmt.Errorf("assistant_invalid_protocol")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(body) > 2*1024*1024 {
		return nil, fmt.Errorf("assistant_request_too_large")
	}
	return body, nil
}

func appendProtocolParts(messages []map[string]any, role, field string, parts []any) []map[string]any {
	if len(parts) == 0 {
		return messages
	}
	if len(messages) > 0 && messages[len(messages)-1]["role"] == role {
		last := messages[len(messages)-1]
		last[field] = append(last[field].([]any), parts...)
		return messages
	}
	return append(messages, map[string]any{"role": role, field: parts})
}

func protocolToolCall(id, name, arguments string) ToolCall {
	call := ToolCall{ID: id, Type: "function"}
	call.Function.Name, call.Function.Arguments = name, arguments
	return call
}

func tokenSum(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, fmt.Errorf("assistant_provider_invalid_usage")
		}
		total += value
	}
	return total, nil
}

func decodeProtocolCompletion(reader io.Reader, protocol, binding string) (CompletionResponse, error) {
	if protocol == "openai_compatible" {
		return decodeCompletion(reader)
	}
	var out CompletionResponse
	out.Message.Role = "assistant"
	var output json.RawMessage
	switch protocol {
	case "openai_responses":
		var raw struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Output json.RawMessage `json:"output"`
			Error  json.RawMessage `json:"error"`
			Usage  *struct {
				Input   int64 `json:"input_tokens"`
				Output  int64 `json:"output_tokens"`
				Details struct {
					Cached int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		}
		if json.NewDecoder(reader).Decode(&raw) != nil {
			return out, fmt.Errorf("assistant_provider_invalid_response")
		}
		if raw.Usage == nil {
			return out, fmt.Errorf("assistant_provider_usage_missing")
		}
		out.ID, out.FinishReason = raw.ID, raw.Status
		out.Usage = TokenUsage{InputTokens: raw.Usage.Input, OutputTokens: raw.Usage.Output, CachedInputTokens: raw.Usage.Details.Cached}
		if (len(raw.Error) > 0 && string(raw.Error) != "null") || (raw.Status != "completed" && raw.Status != "incomplete") {
			return out, fmt.Errorf("assistant_provider_incomplete_response")
		}
		var items []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if json.Unmarshal(raw.Output, &items) != nil {
			return out, fmt.Errorf("assistant_provider_invalid_response")
		}
		for _, item := range items {
			switch item.Type {
			case "message":
				if item.Role != "assistant" {
					return out, fmt.Errorf("assistant_provider_invalid_role")
				}
				for _, content := range item.Content {
					if content.Type == "output_text" {
						out.Message.Content += content.Text
					} else if content.Type == "refusal" {
						out.Message.Content += content.Refusal
					}
				}
			case "function_call":
				out.Message.ToolCalls = append(out.Message.ToolCalls, protocolToolCall(item.CallID, item.Name, item.Arguments))
			}
		}
		// Truncated function arguments must never reach a catalog mutation.
		if raw.Status == "incomplete" && len(out.Message.ToolCalls) > 0 {
			return out, fmt.Errorf("assistant_provider_incomplete_tool_call")
		}
		output = raw.Output
	case "anthropic_messages":
		var raw struct {
			ID         string          `json:"id"`
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			StopReason string          `json:"stop_reason"`
			Usage      *struct {
				Input   int64 `json:"input_tokens"`
				Output  int64 `json:"output_tokens"`
				Read    int64 `json:"cache_read_input_tokens"`
				Created int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		}
		if json.NewDecoder(reader).Decode(&raw) != nil {
			return out, fmt.Errorf("assistant_provider_invalid_response")
		}
		if raw.Usage == nil {
			return out, fmt.Errorf("assistant_provider_usage_missing")
		}
		out.ID, out.FinishReason, out.Message.Role = raw.ID, raw.StopReason, raw.Role
		input, err := tokenSum(raw.Usage.Input, raw.Usage.Read, raw.Usage.Created)
		if err != nil {
			return out, err
		}
		out.Usage = TokenUsage{InputTokens: input, OutputTokens: raw.Usage.Output, CachedInputTokens: raw.Usage.Read}
		var parts []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Text  string          `json:"text"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(raw.Content, &parts) != nil {
			return out, fmt.Errorf("assistant_provider_invalid_response")
		}
		for _, part := range parts {
			if part.Type == "text" {
				out.Message.Content += part.Text
			}
			if part.Type == "tool_use" {
				out.Message.ToolCalls = append(out.Message.ToolCalls, protocolToolCall(part.ID, part.Name, string(part.Input)))
			}
		}
		if raw.StopReason == "max_tokens" && len(out.Message.ToolCalls) > 0 {
			return out, fmt.Errorf("assistant_provider_incomplete_tool_call")
		}
		output = raw.Content
	case "gemini_generate_content":
		var raw struct {
			ID         string `json:"responseId"`
			Candidates []struct {
				Content struct {
					Role  string          `json:"role"`
					Parts json.RawMessage `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Usage *struct {
				Input    int64 `json:"promptTokenCount"`
				Output   int64 `json:"candidatesTokenCount"`
				Thoughts int64 `json:"thoughtsTokenCount"`
				Cached   int64 `json:"cachedContentTokenCount"`
			} `json:"usageMetadata"`
		}
		if json.NewDecoder(reader).Decode(&raw) != nil {
			return out, fmt.Errorf("assistant_provider_invalid_response")
		}
		if raw.Usage == nil {
			return out, fmt.Errorf("assistant_provider_usage_missing")
		}
		outputTokens, err := tokenSum(raw.Usage.Output, raw.Usage.Thoughts)
		if err != nil {
			return out, err
		}
		out.Usage = TokenUsage{InputTokens: raw.Usage.Input, OutputTokens: outputTokens, CachedInputTokens: raw.Usage.Cached}
		if len(raw.Candidates) != 1 {
			return out, fmt.Errorf("assistant_provider_invalid_choices")
		}
		candidate := raw.Candidates[0]
		if candidate.Content.Role != "model" {
			return out, fmt.Errorf("assistant_provider_invalid_role")
		}
		out.ID, out.FinishReason = raw.ID, candidate.FinishReason
		var parts []struct {
			Text    string `json:"text"`
			Thought bool   `json:"thought"`
			Call    *struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		}
		if json.Unmarshal(candidate.Content.Parts, &parts) != nil {
			return out, fmt.Errorf("assistant_provider_invalid_response")
		}
		for _, part := range parts {
			if !part.Thought {
				out.Message.Content += part.Text
			}
			if part.Call != nil {
				id := part.Call.ID
				if id == "" {
					id = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				}
				args := string(part.Call.Args)
				if args == "" {
					args = "{}"
				}
				out.Message.ToolCalls = append(out.Message.ToolCalls, protocolToolCall(id, part.Call.Name, args))
			}
		}
		if candidate.FinishReason != "STOP" && len(out.Message.ToolCalls) > 0 {
			return out, fmt.Errorf("assistant_provider_incomplete_tool_call")
		}
		output = candidate.Content.Parts
	default:
		return out, fmt.Errorf("assistant_invalid_protocol")
	}
	out.Message.ProviderContext = &ProviderContext{Binding: binding, Output: output}
	return validateCompletion(out)
}
