// Command claude_trace_diff compares redacted Claude Code trace directories.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudetrace"
)

func main() {
	var realDir string
	var oursDir string
	var outPath string
	var failOnFatal bool

	flag.StringVar(&realDir, "real", "traces/real", "Directory containing real Claude Code traces")
	flag.StringVar(&oursDir, "ours", "traces/ours", "Directory containing account-pool outbound traces")
	flag.StringVar(&outPath, "out", "traces/report.md", "Markdown report output path")
	flag.BoolVar(&failOnFatal, "fail-on-fatal", false, "Exit with code 1 when fatal findings exist")
	flag.Parse()

	realTraces, err := claudetrace.LoadTraces(realDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load real traces: %v\n", err)
		os.Exit(1)
	}
	oursTraces, err := claudetrace.LoadTraces(oursDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to load ours traces: %v\n", err)
		os.Exit(1)
	}
	findings := claudetrace.CompareTraceSets(realTraces, oursTraces)
	report := claudetrace.BuildMarkdownReport(realTraces, oursTraces, findings)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create report directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, []byte(report), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to write report: %v\n", err)
		os.Exit(1)
	}
	summary := claudetrace.SummarizeDiff(realTraces, oursTraces, findings)
	fmt.Printf("Report written: %s\n", outPath)
	fmt.Printf("real=%d ours=%d fatal=%d warn=%d info=%d ignored_dynamic=%d\n",
		summary.RealTraceCount,
		summary.OursTraceCount,
		summary.Counts[claudetrace.SeverityFatal],
		summary.Counts[claudetrace.SeverityWarn],
		summary.Counts[claudetrace.SeverityInfo],
		summary.Counts[claudetrace.SeverityIgnoredDynamic],
	)
	if failOnFatal && summary.Counts[claudetrace.SeverityFatal] > 0 {
		os.Exit(1)
	}
}
