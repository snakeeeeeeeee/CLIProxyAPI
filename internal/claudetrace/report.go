package claudetrace

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func BuildMarkdownReport(realTraces, oursTraces []Trace, findings []DiffFinding) string {
	summary := SummarizeDiff(realTraces, oursTraces, findings)
	var b strings.Builder
	b.WriteString("# Claude Code Trace Diff Report\n\n")
	b.WriteString(fmt.Sprintf("- Generated At: `%s`\n", time.Now().UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Real Traces: `%d`\n", summary.RealTraceCount))
	b.WriteString(fmt.Sprintf("- Ours Traces: `%d`\n", summary.OursTraceCount))
	b.WriteString(fmt.Sprintf("- Request Modes: `%s`\n", strings.Join(requestModeSummary(realTraces, oursTraces), ", ")))
	b.WriteString(fmt.Sprintf("- Fatal: `%d`\n", summary.Counts[SeverityFatal]))
	b.WriteString(fmt.Sprintf("- Warn: `%d`\n", summary.Counts[SeverityWarn]))
	b.WriteString(fmt.Sprintf("- Info: `%d`\n", summary.Counts[SeverityInfo]))
	b.WriteString(fmt.Sprintf("- Ignored Dynamic: `%d`\n\n", summary.Counts[SeverityIgnoredDynamic]))

	if len(findings) == 0 {
		b.WriteString("No trace differences found.\n")
		return b.String()
	}

	for _, severity := range []string{SeverityFatal, SeverityWarn, SeverityInfo, SeverityIgnoredDynamic} {
		items := filterFindings(findings, severity)
		if len(items) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("## %s\n\n", strings.ToUpper(severity)))
		b.WriteString("| Field | Real | Ours | Message |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
		for _, item := range items {
			b.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s |\n",
				escapeMarkdownTable(item.Field),
				codeOrDash(item.Real),
				codeOrDash(item.Ours),
				escapeMarkdownTable(item.Message),
			))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func requestModeSummary(realTraces, oursTraces []Trace) []string {
	counts := map[string]int{}
	for _, trace := range append(append([]Trace(nil), realTraces...), oursTraces...) {
		mode := strings.TrimSpace(trace.RequestMode)
		if mode == "" {
			mode = "unknown"
		}
		counts[mode]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return out
}

func filterFindings(findings []DiffFinding, severity string) []DiffFinding {
	var out []DiffFinding
	for _, item := range findings {
		if item.Severity == severity {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Field < out[j].Field
	})
	return out
}

func codeOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	if len(value) > 180 {
		value = value[:180] + "..."
	}
	return "`" + escapeMarkdownTable(value) + "`"
}

func escapeMarkdownTable(value string) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "`", "\\`")
	return value
}
