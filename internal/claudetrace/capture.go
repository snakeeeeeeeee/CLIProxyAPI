package claudetrace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"api-key":             true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
}

func CaptureRequest(req *http.Request, opts CaptureOptions) Trace {
	trace := Trace{
		SchemaVersion: SchemaVersion,
		Source:        strings.TrimSpace(opts.Source),
		RequestMode:   strings.TrimSpace(opts.RequestMode),
		CapturedAt:    time.Now().UTC(),
		Headers:       RedactHeaders(nil),
		Stream:        opts.Stream,
		StatusCode:    opts.StatusCode,
		ResponseError: strings.TrimSpace(opts.ResponseError),
	}
	if trace.Source == "" {
		trace.Source = SourceReal
	}
	if req != nil {
		trace.Method = req.Method
		if req.URL != nil {
			trace.Path = req.URL.Path
			trace.Query = req.URL.RawQuery
			trace.URL = redactURL(req.URL)
		}
		trace.Headers = RedactHeaders(req.Header)
		if !trace.Stream {
			trace.Stream = requestLooksStreaming(req.Header, opts.RequestBody)
		}
	}
	trace.RequestID = requestID(opts.ResponseHeaders)
	trace.Body = RedactBody(opts.RequestBody, opts.RedactUserContent)
	trace.BodyShape = BuildBodyShape(opts.RequestBody)
	if trace.RequestMode == "" {
		trace.RequestMode = InferRequestMode(trace.Headers, trace.BodyShape)
	}
	return trace
}

func InferRequestMode(headers map[string]string, shape BodyShape) string {
	if looksLikeClaudeCodeHeader(headers) || looksLikeClaudeCodeBody(shape) {
		return RequestModeRealClaudeCodePassthrough
	}
	return RequestModeAPIMimic
}

func looksLikeClaudeCodeHeader(headers map[string]string) bool {
	ua, okUA := headerValue(headers, "User-Agent")
	if okUA && strings.HasPrefix(strings.TrimSpace(ua), "claude-cli/") {
		return true
	}
	xApp, okXApp := headerValue(headers, "X-App")
	return okXApp && strings.EqualFold(strings.TrimSpace(xApp), "cli")
}

func looksLikeClaudeCodeBody(shape BodyShape) bool {
	if shape.BillingBlockKind != "" && shape.ToolCount >= 10 {
		return true
	}
	return shape.BillingBlockKind != "" && shape.HasContextManagement && (shape.HasThinking || shape.ToolCount > 0)
}

func RedactHeaders(headers http.Header) map[string]string {
	out := make(map[string]string)
	for key, values := range headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "" {
			continue
		}
		if sensitiveHeaders[lower] {
			out[http.CanonicalHeaderKey(key)] = "<redacted>"
			continue
		}
		out[http.CanonicalHeaderKey(key)] = strings.Join(values, ",")
	}
	return out
}

func RedactBody(raw []byte, redactUserContent bool) any {
	if len(raw) == 0 || !gjson.ValidBytes(raw) {
		if len(raw) == 0 {
			return nil
		}
		return map[string]any{"raw_hash": shortHash(raw), "raw_len": len(raw)}
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return map[string]any{"raw_hash": shortHash(raw), "raw_len": len(raw)}
	}
	if redactUserContent {
		body = redactJSONValue(body, "")
	}
	return body
}

func BuildBodyShape(raw []byte) BodyShape {
	root := gjson.ParseBytes(raw)
	shape := BodyShape{
		Model:                root.Get("model").String(),
		HasMetadata:          root.Get("metadata").Exists(),
		MetadataUserIDKind:   metadataUserIDKind(root.Get("metadata.user_id").String()),
		SystemBlockCount:     countSystemBlocks(root.Get("system")),
		SystemTextHashes:     systemTextHashes(root.Get("system")),
		BillingBlockKind:     billingBlockKind(root.Get("system")),
		MessageCount:         len(root.Get("messages").Array()),
		UserTextHashes:       userTextHashes(root.Get("messages")),
		ToolCount:            len(root.Get("tools").Array()),
		ToolSchemaHashes:     toolSchemaHashes(root.Get("tools")),
		CacheControlPaths:    cacheControlPaths(root),
		HasThinking:          root.Get("thinking").Exists(),
		ThinkingType:         root.Get("thinking.type").String(),
		HasContextManagement: root.Get("context_management").Exists(),
		TopLevelKeys:         topLevelKeys(root),
	}
	return shape
}

func redactJSONValue(value any, path string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			lower := strings.ToLower(key)
			if lower == "authorization" || lower == "x-api-key" || lower == "api_key" || lower == "access_token" || lower == "refresh_token" {
				out[key] = "<redacted>"
				continue
			}
			if shouldRedactText(childPath, key, child) {
				out[key] = summarizeText(fmt.Sprint(child))
				continue
			}
			out[key] = redactJSONValue(child, childPath)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactJSONValue(child, fmt.Sprintf("%s.%d", path, i))
		}
		return out
	default:
		return value
	}
}

func shouldRedactText(path string, key string, value any) bool {
	if key != "text" && key != "content" {
		return false
	}
	if _, ok := value.(string); !ok {
		return false
	}
	lowerPath := strings.ToLower(path)
	if strings.HasPrefix(lowerPath, "system") {
		return false
	}
	return strings.Contains(lowerPath, "messages")
}

func summarizeText(text string) map[string]any {
	return map[string]any{
		"redacted": true,
		"hash":     shortHash([]byte(text)),
		"length":   len(text),
	}
}

func systemTextHashes(system gjson.Result) []TextHash {
	var out []TextHash
	if system.IsArray() {
		system.ForEach(func(index, block gjson.Result) bool {
			text := block.Get("text").String()
			if text != "" {
				out = append(out, TextHash{Path: fmt.Sprintf("system.%d.text", index.Int()), Hash: shortHash([]byte(text)), Length: len(text)})
			}
			return true
		})
		return out
	}
	if system.Type == gjson.String {
		text := system.String()
		out = append(out, TextHash{Path: "system", Hash: shortHash([]byte(text)), Length: len(text)})
	}
	return out
}

func userTextHashes(messages gjson.Result) []TextHash {
	var out []TextHash
	if !messages.IsArray() {
		return out
	}
	messages.ForEach(func(msgIndex, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		content := msg.Get("content")
		if content.Type == gjson.String {
			text := content.String()
			out = append(out, TextHash{Path: fmt.Sprintf("messages.%d.content", msgIndex.Int()), Hash: shortHash([]byte(text)), Length: len(text)})
			return true
		}
		if content.IsArray() {
			content.ForEach(func(partIndex, part gjson.Result) bool {
				if part.Get("type").String() == "text" {
					text := part.Get("text").String()
					out = append(out, TextHash{Path: fmt.Sprintf("messages.%d.content.%d.text", msgIndex.Int(), partIndex.Int()), Hash: shortHash([]byte(text)), Length: len(text)})
				}
				return true
			})
		}
		return true
	})
	return out
}

func toolSchemaHashes(tools gjson.Result) []ToolHash {
	var out []ToolHash
	if !tools.IsArray() {
		return out
	}
	tools.ForEach(func(_, tool gjson.Result) bool {
		out = append(out, ToolHash{
			Name:       tool.Get("name").String(),
			Type:       tool.Get("type").String(),
			SchemaHash: shortHash([]byte(canonicalJSON(tool.Get("input_schema").Raw))),
			RawHash:    shortHash([]byte(canonicalJSON(tool.Raw))),
		})
		return true
	})
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func cacheControlPaths(root gjson.Result) []string {
	var out []string
	walkJSON(root.Value(), "", func(path string, value any) {
		if strings.HasSuffix(path, ".cache_control") || path == "cache_control" {
			out = append(out, path)
		}
	})
	sort.Strings(out)
	return out
}

func topLevelKeys(root gjson.Result) []string {
	value, ok := root.Value().(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func walkJSON(value any, path string, fn func(path string, value any)) {
	fn(path, value)
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			walkJSON(child, childPath, fn)
		}
	case []any:
		for i, child := range typed {
			childPath := fmt.Sprintf("%s.%d", path, i)
			if path == "" {
				childPath = fmt.Sprintf("%d", i)
			}
			walkJSON(child, childPath, fn)
		}
	}
}

func countSystemBlocks(system gjson.Result) int {
	if system.IsArray() {
		return len(system.Array())
	}
	if system.Exists() {
		return 1
	}
	return 0
}

func billingBlockKind(system gjson.Result) string {
	first := system.Get("0.text").String()
	if first == "" && system.Type == gjson.String {
		first = system.String()
	}
	if strings.HasPrefix(first, "x-anthropic-billing-header:") {
		if strings.Contains(first, "cch=00000") {
			return "billing_zero_cch"
		}
		if strings.Contains(first, "cch=") {
			return "billing_cch"
		}
		return "billing_no_cch"
	}
	return "none"
}

func metadataUserIDKind(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "{") && strings.Contains(value, "device_id") && strings.Contains(value, "session_id") {
		return "json_device_account_session"
	}
	if strings.HasPrefix(value, "user_") && strings.Contains(value, "_account_") && strings.Contains(value, "_session_") {
		return "legacy_user_account_session"
	}
	return "opaque"
}

func requestLooksStreaming(headers http.Header, body []byte) bool {
	if strings.Contains(strings.ToLower(headers.Get("Accept")), "text/event-stream") {
		return true
	}
	return gjson.GetBytes(body, "stream").Bool()
}

func requestID(headers http.Header) string {
	for _, key := range []string{"request-id", "x-request-id", "anthropic-request-id"} {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func shortHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func canonicalJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	if clone.User != nil {
		clone.User = url.User("redacted")
	}
	return clone.String()
}
