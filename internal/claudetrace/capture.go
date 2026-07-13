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
	"authorization":            true,
	"x-api-key":                true,
	"api-key":                  true,
	"proxy-authorization":      true,
	"cookie":                   true,
	"set-cookie":               true,
	"x-claude-code-session-id": true,
}

func CaptureRequest(req *http.Request, opts CaptureOptions) Trace {
	trace := Trace{
		SchemaVersion: SchemaVersion,
		Source:        strings.TrimSpace(opts.Source),
		RequestMode:   strings.TrimSpace(opts.RequestMode),
		RequestKind:   strings.TrimSpace(opts.RequestKind),
		CapturedAt:    time.Now().UTC(),
		Headers:       RedactHeaders(nil),
		Stream:        opts.Stream,
		StatusCode:    opts.StatusCode,
		ResponseError: strings.TrimSpace(opts.ResponseError),
		TLSProfile:    strings.TrimSpace(opts.TLSProfile),
		TLSFingerprint: TLSFingerprint{
			JA3:  strings.TrimSpace(opts.TLSJA3),
			JA4:  strings.TrimSpace(opts.TLSJA4),
			ALPN: strings.TrimSpace(opts.TLSALPN),
		},
		RawHeaderOrder: normalizeHeaderOrder(opts.RawHeaderOrder),
	}
	if trace.Source == "" {
		trace.Source = SourceReal
	}
	if req != nil {
		trace.Method = req.Method
		trace.HTTPProtocol = req.Proto
		if req.URL != nil {
			trace.Path = req.URL.Path
			trace.Query = req.URL.RawQuery
			trace.URL = redactURL(req.URL)
		}
		trace.Headers = RedactHeaders(req.Header)
		trace.Accept = req.Header.Get("Accept")
		trace.AcceptEncoding = req.Header.Get("Accept-Encoding")
		trace.Stainless = stainlessTuple(req.Header)
		if !trace.Stream {
			trace.Stream = requestLooksStreaming(req.Header, opts.RequestBody)
		}
	}
	trace.RequestID = requestID(opts.ResponseHeaders)
	trace.Body = RedactBody(opts.RequestBody, opts.RedactUserContent)
	trace.BodyShape = BuildBodyShape(opts.RequestBody)
	trace.Session = buildSessionInvariant(req, opts.RequestBody)
	if trace.RequestKind == "" {
		trace.RequestKind = InferRequestKind(trace.Path, opts.RequestBody)
	}
	if trace.RequestMode == "" {
		trace.RequestMode = InferRequestMode(trace.Headers, trace.BodyShape)
	}
	return trace
}

func buildSessionInvariant(req *http.Request, body []byte) SessionInvariant {
	headerSession := ""
	if req != nil {
		headerSession = strings.TrimSpace(req.Header.Get("X-Claude-Code-Session-Id"))
	}
	metadataSession := metadataSessionID(gjson.GetBytes(body, "metadata.user_id").String())
	return SessionInvariant{
		HeaderPresent:   headerSession != "",
		MetadataPresent: metadataSession != "",
		Match:           headerSession != "" && metadataSession != "" && strings.EqualFold(headerSession, metadataSession),
	}
}

func metadataSessionID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if gjson.Valid(value) {
		return strings.TrimSpace(gjson.Get(value, "session_id").String())
	}
	const marker = "_session_"
	if index := strings.LastIndex(strings.ToLower(value), marker); index >= 0 {
		return strings.TrimSpace(value[index+len(marker):])
	}
	return ""
}

func normalizeHeaderOrder(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

// InferRequestKind separates interactive requests from helper and follow-up traffic.
func InferRequestKind(path string, body []byte) string {
	if strings.HasSuffix(strings.TrimSpace(path), "/messages/count_tokens") {
		return RequestKindCountTokens
	}
	if !strings.HasSuffix(strings.TrimSpace(path), "/messages") || !gjson.ValidBytes(body) {
		return RequestKindOther
	}
	messages := gjson.GetBytes(body, "messages")
	toolFollowup := false
	messages.ForEach(func(_, message gjson.Result) bool {
		content := message.Get("content")
		content.ForEach(func(_, part gjson.Result) bool {
			kind := strings.ToLower(strings.TrimSpace(part.Get("type").String()))
			if kind == "tool_use" || kind == "tool_result" {
				toolFollowup = true
				return false
			}
			return true
		})
		return !toolFollowup
	})
	if toolFollowup {
		return RequestKindToolFollowup
	}
	if gjson.GetBytes(body, "output_config.format").Exists() && len(gjson.GetBytes(body, "tools").Array()) == 0 {
		return RequestKindStructuredHelper
	}
	return RequestKindInteractive
}

func InferRequestMode(headers map[string]string, shape BodyShape) string {
	if looksLikeClaudeCodeHeader(headers) && looksLikeClaudeCodeBody(shape) {
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
	if shape.BillingBlockKind == "" || shape.MetadataUserIDKind == "" {
		return false
	}
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
		BillingEntrypoint:    billingEntrypoint(root.Get("system")),
		BillingHasCCH:        billingHasCCH(root.Get("system")),
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

func stainlessTuple(headers http.Header) StainlessTuple {
	return StainlessTuple{
		Lang:           headers.Get("X-Stainless-Lang"),
		PackageVersion: headers.Get("X-Stainless-Package-Version"),
		OS:             headers.Get("X-Stainless-OS"),
		Arch:           headers.Get("X-Stainless-Arch"),
		Runtime:        headers.Get("X-Stainless-Runtime"),
		RuntimeVersion: headers.Get("X-Stainless-Runtime-Version"),
		RetryCount:     headers.Get("X-Stainless-Retry-Count"),
		Timeout:        headers.Get("X-Stainless-Timeout"),
	}
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
			if strings.EqualFold(childPath, "metadata.user_id") {
				out[key] = map[string]any{
					"redacted": true,
					"kind":     metadataUserIDKind(fmt.Sprint(child)),
				}
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

func billingEntrypoint(system gjson.Result) string {
	first := system.Get("0.text").String()
	if first == "" && system.Type == gjson.String {
		first = system.String()
	}
	const marker = "cc_entrypoint="
	index := strings.Index(first, marker)
	if index < 0 {
		return ""
	}
	value := first[index+len(marker):]
	if end := strings.IndexByte(value, ';'); end >= 0 {
		value = value[:end]
	}
	return strings.TrimSpace(value)
}

func billingHasCCH(system gjson.Result) bool {
	first := system.Get("0.text").String()
	if first == "" && system.Type == gjson.String {
		first = system.String()
	}
	return strings.Contains(first, "cch=")
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
