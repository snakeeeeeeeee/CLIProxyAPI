package claudetrace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var unsafeFileNameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func SaveTrace(dir string, trace Trace) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create trace directory: %w", err)
	}
	if trace.SchemaVersion == 0 {
		trace.SchemaVersion = SchemaVersion
	}
	if trace.CapturedAt.IsZero() {
		trace.CapturedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal trace: %w", err)
	}
	name := traceFileName(trace)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write trace: %w", err)
	}
	return path, nil
}

func LoadTraces(dir string) ([]Trace, error) {
	var traces []Trace
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read trace directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read trace %s: %w", path, err)
		}
		var trace Trace
		if err := json.Unmarshal(data, &trace); err != nil {
			return nil, fmt.Errorf("parse trace %s: %w", path, err)
		}
		traces = append(traces, trace)
	}
	return traces, nil
}

func traceFileName(trace Trace) string {
	ts := trace.CapturedAt.UTC().Format("20060102T150405.000000000Z")
	method := strings.ToUpper(strings.TrimSpace(trace.Method))
	if method == "" {
		method = "REQ"
	}
	path := strings.Trim(trace.Path, "/")
	if path == "" {
		path = "root"
	}
	path = strings.ReplaceAll(path, "/", "_")
	path = unsafeFileNameChars.ReplaceAllString(path, "_")
	hashSeed := trace.Method + "|" + trace.Path + "|" + trace.Query + "|" + trace.BodyShape.Model + "|" + fmt.Sprint(trace.CapturedAt.UnixNano())
	return fmt.Sprintf("%s_%s_%s_%s.json", ts, method, path, shortHash([]byte(hashSeed))[:8])
}
