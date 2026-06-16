package claudetrace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CompareTraceSets compares real Claude Code traces against account-pool traces.
// It pairs traces by method, path, model, and stream flag, then compares each
// pair in capture order. User message text hashes are intentionally ignored.
func CompareTraceSets(realTraces, oursTraces []Trace) []DiffFinding {
	realGroups := groupTraces(realTraces)
	oursGroups := groupTraces(oursTraces)
	keys := make([]string, 0, len(realGroups)+len(oursGroups))
	seen := make(map[string]bool, len(realGroups)+len(oursGroups))
	for key := range realGroups {
		keys = append(keys, key)
		seen[key] = true
	}
	for key := range oursGroups {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	var findings []DiffFinding
	for _, key := range keys {
		realList := realGroups[key]
		oursList := oursGroups[key]
		sortTraces(realList)
		sortTraces(oursList)
		maxLen := len(realList)
		if len(oursList) > maxLen {
			maxLen = len(oursList)
		}
		for i := 0; i < maxLen; i++ {
			prefix := fmt.Sprintf("trace[%s]#%d", key, i+1)
			if i >= len(realList) {
				findings = append(findings, finding(SeverityWarn, prefix, "", traceLabel(oursList[i]), "ours trace has no matching real trace"))
				continue
			}
			if i >= len(oursList) {
				findings = append(findings, finding(SeverityFatal, prefix, traceLabel(realList[i]), "", "real trace has no matching ours trace"))
				continue
			}
			findings = append(findings, CompareTracePair(realList[i], oursList[i], prefix)...)
		}
	}
	return findings
}

// CompareTracePair compares one real/ours pair.
func CompareTracePair(realTrace, oursTrace Trace, prefix string) []DiffFinding {
	if strings.TrimSpace(prefix) == "" {
		prefix = "trace"
	}
	var out []DiffFinding
	if realTrace.Method != oursTrace.Method {
		out = append(out, finding(SeverityFatal, prefix+".method", realTrace.Method, oursTrace.Method, "HTTP method differs"))
	}
	if realTrace.Path != oursTrace.Path {
		out = append(out, finding(SeverityFatal, prefix+".path", realTrace.Path, oursTrace.Path, "request path differs"))
	}
	if realTrace.Stream != oursTrace.Stream {
		out = append(out, finding(SeverityFatal, prefix+".stream", fmt.Sprint(realTrace.Stream), fmt.Sprint(oursTrace.Stream), "stream flag differs"))
	}
	if realTrace.BodyShape.Model != oursTrace.BodyShape.Model {
		out = append(out, finding(SeverityFatal, prefix+".body.model", realTrace.BodyShape.Model, oursTrace.BodyShape.Model, "model differs"))
	}

	mode := normalizeRequestMode(oursTrace.RequestMode)
	if mode == "" {
		mode = normalizeRequestMode(realTrace.RequestMode)
	}
	if mode == "" {
		mode = InferRequestMode(oursTrace.Headers, oursTrace.BodyShape)
	}
	if realMode := normalizeRequestMode(realTrace.RequestMode); realMode != "" && realMode != mode {
		out = append(out, finding(SeverityInfo, prefix+".request_mode", realMode, mode, "request modes differ"))
	}

	out = append(out, compareHeader(prefix, realTrace, oursTrace, "User-Agent", SeverityWarn, SeverityWarn, false)...)
	out = append(out, compareHeader(prefix, realTrace, oursTrace, "X-App", SeverityFatal, SeverityFatal, false)...)
	out = append(out, compareHeader(prefix, realTrace, oursTrace, "Anthropic-Version", SeverityFatal, SeverityFatal, false)...)
	out = append(out, compareHeader(prefix, realTrace, oursTrace, "Anthropic-Beta", SeverityWarn, SeverityWarn, false)...)
	out = append(out, compareHeader(prefix, realTrace, oursTrace, "X-Claude-Code-Session-Id", SeverityWarn, SeverityIgnoredDynamic, true)...)
	out = append(out, compareHeader(prefix, realTrace, oursTrace, "X-Client-Request-Id", SeverityWarn, SeverityIgnoredDynamic, true)...)

	out = append(out, compareBodyShape(prefix, realTrace.BodyShape, oursTrace.BodyShape, mode)...)
	if realTrace.RequestID != "" && oursTrace.RequestID != "" && realTrace.RequestID != oursTrace.RequestID {
		out = append(out, finding(SeverityIgnoredDynamic, prefix+".response.request_id", realTrace.RequestID, oursTrace.RequestID, "upstream request ids are expected to differ"))
	}
	return out
}

func compareHeader(prefix string, realTrace, oursTrace Trace, key, missingSeverity, mismatchSeverity string, dynamic bool) []DiffFinding {
	realValue, realOK := headerValue(realTrace.Headers, key)
	oursValue, oursOK := headerValue(oursTrace.Headers, key)
	field := prefix + ".headers." + strings.ToLower(key)
	if realOK && !oursOK {
		return []DiffFinding{finding(missingSeverity, field, realValue, "", fmt.Sprintf("missing %s header", key))}
	}
	if !realOK && oursOK {
		return []DiffFinding{finding(SeverityInfo, field, "", oursValue, fmt.Sprintf("ours has extra %s header", key))}
	}
	if !realOK && !oursOK {
		return nil
	}
	if realValue == oursValue {
		return nil
	}
	if dynamic {
		return []DiffFinding{finding(mismatchSeverity, field, realValue, oursValue, fmt.Sprintf("%s differs and is dynamic", key))}
	}
	return []DiffFinding{finding(mismatchSeverity, field, realValue, oursValue, fmt.Sprintf("%s header differs", key))}
}

func compareBodyShape(prefix string, realShape, oursShape BodyShape, mode string) []DiffFinding {
	var out []DiffFinding
	apiMimic := normalizeRequestMode(mode) == RequestModeAPIMimic
	systemSeverity := SeverityFatal
	toolSeverity := SeverityFatal
	thinkingSeverity := SeverityWarn
	if apiMimic {
		systemSeverity = SeverityInfo
		toolSeverity = SeverityInfo
		thinkingSeverity = SeverityInfo
	}
	out = append(out, compareString(prefix+".body.metadata_user_id_kind", realShape.MetadataUserIDKind, oursShape.MetadataUserIDKind, SeverityWarn, "metadata.user_id format differs")...)
	out = append(out, compareString(prefix+".body.billing_block_kind", realShape.BillingBlockKind, oursShape.BillingBlockKind, SeverityFatal, "billing block format differs")...)
	out = append(out, compareInt(prefix+".body.system_block_count", realShape.SystemBlockCount, oursShape.SystemBlockCount, systemSeverity, "system block count differs")...)
	out = append(out, compareJSON(prefix+".body.system_text_hashes", realShape.SystemTextHashes, oursShape.SystemTextHashes, systemSeverity, "system block text hashes/order differ")...)
	out = append(out, compareInt(prefix+".body.tool_count", realShape.ToolCount, oursShape.ToolCount, toolSeverity, "tool count differs")...)
	out = append(out, compareJSON(prefix+".body.tool_schema_hashes", realShape.ToolSchemaHashes, oursShape.ToolSchemaHashes, toolSeverity, "tool schemas differ")...)
	out = append(out, compareJSON(prefix+".body.cache_control_paths", realShape.CacheControlPaths, oursShape.CacheControlPaths, thinkingSeverity, "cache_control placement differs")...)
	out = append(out, compareBool(prefix+".body.has_thinking", realShape.HasThinking, oursShape.HasThinking, thinkingSeverity, "thinking presence differs")...)
	out = append(out, compareString(prefix+".body.thinking_type", realShape.ThinkingType, oursShape.ThinkingType, thinkingSeverity, "thinking type differs")...)
	out = append(out, compareBool(prefix+".body.has_context_management", realShape.HasContextManagement, oursShape.HasContextManagement, thinkingSeverity, "context_management presence differs")...)
	out = append(out, compareJSON(prefix+".body.top_level_keys", realShape.TopLevelKeys, oursShape.TopLevelKeys, thinkingSeverity, "top-level fields differ")...)
	return out
}

func normalizeRequestMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case RequestModeRealClaudeCodePassthrough:
		return RequestModeRealClaudeCodePassthrough
	case RequestModeAPIMimic:
		return RequestModeAPIMimic
	default:
		return ""
	}
}

func groupTraces(traces []Trace) map[string][]Trace {
	groups := make(map[string][]Trace)
	for _, trace := range traces {
		key := traceGroupKey(trace)
		groups[key] = append(groups[key], trace)
	}
	return groups
}

func traceGroupKey(trace Trace) string {
	stream := "nonstream"
	if trace.Stream {
		stream = "stream"
	}
	model := strings.TrimSpace(trace.BodyShape.Model)
	if model == "" {
		model = "unknown-model"
	}
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(trace.Method)),
		strings.TrimSpace(trace.Path),
		model,
		stream,
	}, "|")
}

func sortTraces(traces []Trace) {
	sort.SliceStable(traces, func(i, j int) bool {
		if traces[i].CapturedAt.Equal(traces[j].CapturedAt) {
			return traceLabel(traces[i]) < traceLabel(traces[j])
		}
		return traces[i].CapturedAt.Before(traces[j].CapturedAt)
	})
}

func traceLabel(trace Trace) string {
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(trace.Method)),
		strings.TrimSpace(trace.Path),
		strings.TrimSpace(trace.BodyShape.Model),
		fmt.Sprintf("stream=%v", trace.Stream),
		strings.TrimSpace(trace.RequestMode),
		trace.CapturedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
	}, " ")
}

func headerValue(headers map[string]string, key string) (string, bool) {
	if len(headers) == 0 {
		return "", false
	}
	keyLower := strings.ToLower(key)
	for candidate, value := range headers {
		if strings.ToLower(candidate) == keyLower {
			return strings.TrimSpace(value), strings.TrimSpace(value) != ""
		}
	}
	return "", false
}

func compareString(field, realValue, oursValue, severity, message string) []DiffFinding {
	if realValue == oursValue {
		return nil
	}
	return []DiffFinding{finding(severity, field, realValue, oursValue, message)}
}

func compareBool(field string, realValue, oursValue bool, severity, message string) []DiffFinding {
	if realValue == oursValue {
		return nil
	}
	return []DiffFinding{finding(severity, field, fmt.Sprint(realValue), fmt.Sprint(oursValue), message)}
}

func compareInt(field string, realValue, oursValue int, severity, message string) []DiffFinding {
	if realValue == oursValue {
		return nil
	}
	return []DiffFinding{finding(severity, field, fmt.Sprint(realValue), fmt.Sprint(oursValue), message)}
}

func compareJSON(field string, realValue, oursValue any, severity, message string) []DiffFinding {
	realRaw, _ := json.Marshal(realValue)
	oursRaw, _ := json.Marshal(oursValue)
	if string(realRaw) == string(oursRaw) {
		return nil
	}
	return []DiffFinding{finding(severity, field, string(realRaw), string(oursRaw), message)}
}

func finding(severity, field, realValue, oursValue, message string) DiffFinding {
	return DiffFinding{
		Severity: severity,
		Field:    field,
		Real:     realValue,
		Ours:     oursValue,
		Message:  message,
	}
}

func SummarizeDiff(realTraces, oursTraces []Trace, findings []DiffFinding) DiffSummary {
	counts := map[string]int{
		SeverityFatal:          0,
		SeverityWarn:           0,
		SeverityInfo:           0,
		SeverityIgnoredDynamic: 0,
	}
	for _, item := range findings {
		severity := strings.TrimSpace(item.Severity)
		if severity == "" {
			severity = SeverityInfo
		}
		counts[severity]++
	}
	return DiffSummary{
		RealTraceCount: len(realTraces),
		OursTraceCount: len(oursTraces),
		Counts:         counts,
	}
}
