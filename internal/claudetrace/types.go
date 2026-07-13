// Package claudetrace records and compares redacted Claude Code request traces.
package claudetrace

import (
	"net/http"
	"time"
)

const (
	SchemaVersion = 3

	SourceReal = "real"
	SourceOurs = "ours"

	RequestModeRealClaudeCodePassthrough = "real-claude-code-passthrough"
	RequestModeAPIMimic                  = "api-mimic"

	RequestKindInteractive      = "interactive"
	RequestKindStructuredHelper = "structured-helper"
	RequestKindCountTokens      = "count_tokens"
	RequestKindToolFollowup     = "tool-followup"
	RequestKindOther            = "other"

	SeverityFatal          = "fatal"
	SeverityWarn           = "warn"
	SeverityInfo           = "info"
	SeverityIgnoredDynamic = "ignored-dynamic"
)

type Config struct {
	Enabled           bool
	DumpDir           string
	RedactUserContent bool
	Source            string
}

type CaptureOptions struct {
	Source            string
	RedactUserContent bool
	RequestBody       []byte
	StatusCode        int
	ResponseHeaders   http.Header
	ResponseError     string
	Stream            bool
	RequestMode       string
	RequestKind       string
	TLSProfile        string
	TLSJA3            string
	TLSJA4            string
	TLSALPN           string
	RawHeaderOrder    []string
}

type Trace struct {
	SchemaVersion  int               `json:"schema_version"`
	Source         string            `json:"source"`
	RequestMode    string            `json:"request_mode,omitempty"`
	RequestKind    string            `json:"request_kind,omitempty"`
	CapturedAt     time.Time         `json:"captured_at"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	HTTPProtocol   string            `json:"http_protocol,omitempty"`
	Accept         string            `json:"accept,omitempty"`
	AcceptEncoding string            `json:"accept_encoding,omitempty"`
	TLSProfile     string            `json:"tls_profile,omitempty"`
	TLSFingerprint TLSFingerprint    `json:"tls_fingerprint"`
	RawHeaderOrder []string          `json:"raw_header_order,omitempty"`
	Stainless      StainlessTuple    `json:"stainless"`
	Session        SessionInvariant  `json:"session"`
	Query          string            `json:"query,omitempty"`
	URL            string            `json:"url,omitempty"`
	Stream         bool              `json:"stream"`
	StatusCode     int               `json:"status_code,omitempty"`
	RequestID      string            `json:"request_id,omitempty"`
	Headers        map[string]string `json:"headers"`
	Body           any               `json:"body,omitempty"`
	BodyShape      BodyShape         `json:"body_shape"`
	ResponseError  string            `json:"response_error,omitempty"`
}

type TLSFingerprint struct {
	JA3  string `json:"ja3,omitempty"`
	JA4  string `json:"ja4,omitempty"`
	ALPN string `json:"alpn,omitempty"`
}

// SessionInvariant records only structural consistency. Session values are
// intentionally excluded from trace files.
type SessionInvariant struct {
	HeaderPresent   bool `json:"header_present"`
	MetadataPresent bool `json:"metadata_present"`
	Match           bool `json:"match"`
}

type StainlessTuple struct {
	Lang           string `json:"lang,omitempty"`
	PackageVersion string `json:"package_version,omitempty"`
	OS             string `json:"os,omitempty"`
	Arch           string `json:"arch,omitempty"`
	Runtime        string `json:"runtime,omitempty"`
	RuntimeVersion string `json:"runtime_version,omitempty"`
	RetryCount     string `json:"retry_count,omitempty"`
	Timeout        string `json:"timeout,omitempty"`
}

type BodyShape struct {
	Model                string     `json:"model,omitempty"`
	HasMetadata          bool       `json:"has_metadata"`
	MetadataUserIDKind   string     `json:"metadata_user_id_kind,omitempty"`
	SystemBlockCount     int        `json:"system_block_count"`
	SystemTextHashes     []TextHash `json:"system_text_hashes,omitempty"`
	BillingBlockKind     string     `json:"billing_block_kind,omitempty"`
	BillingEntrypoint    string     `json:"billing_entrypoint,omitempty"`
	BillingHasCCH        bool       `json:"billing_has_cch"`
	MessageCount         int        `json:"message_count"`
	UserTextHashes       []TextHash `json:"user_text_hashes,omitempty"`
	ToolCount            int        `json:"tool_count"`
	ToolSchemaHashes     []ToolHash `json:"tool_schema_hashes,omitempty"`
	CacheControlPaths    []string   `json:"cache_control_paths,omitempty"`
	HasThinking          bool       `json:"has_thinking"`
	ThinkingType         string     `json:"thinking_type,omitempty"`
	HasContextManagement bool       `json:"has_context_management"`
	TopLevelKeys         []string   `json:"top_level_keys,omitempty"`
}

type TextHash struct {
	Path   string `json:"path"`
	Hash   string `json:"hash"`
	Length int    `json:"length"`
}

type ToolHash struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	SchemaHash string `json:"schema_hash,omitempty"`
	RawHash    string `json:"raw_hash"`
}

type DiffFinding struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Real     string `json:"real,omitempty"`
	Ours     string `json:"ours,omitempty"`
	Message  string `json:"message"`
}

type DiffSummary struct {
	RealTraceCount int            `json:"real_trace_count"`
	OursTraceCount int            `json:"ours_trace_count"`
	Counts         map[string]int `json:"counts"`
}
