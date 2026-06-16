package helps

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// userIDPattern matches Claude Code format: user_[64-hex]_account_[uuid]_session_[uuid]
var userIDPattern = regexp.MustCompile(`^user_([a-fA-F0-9]{64})_account_([0-9a-fA-F-]*)_session_([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)

var opaqueUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,255}$`)

const NewClaudeCodeMetadataUserIDVersion = "2.1.78"

type ClaudeCodeMetadataUserID struct {
	DeviceID    string `json:"device_id"`
	AccountUUID string `json:"account_uuid"`
	SessionID   string `json:"session_id"`
}

// generateFakeUserID generates a fake user ID in Claude Code format.
// Format: user_[64-hex-chars]_account_[UUID-v4]_session_[UUID-v4]
func generateFakeUserID() string {
	hexBytes := make([]byte, 32)
	_, _ = rand.Read(hexBytes)
	hexPart := hex.EncodeToString(hexBytes)
	accountUUID := uuid.New().String()
	sessionUUID := uuid.New().String()
	return "user_" + hexPart + "_account_" + accountUUID + "_session_" + sessionUUID
}

func ParseClaudeCodeMetadataUserID(userID string) (ClaudeCodeMetadataUserID, bool) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ClaudeCodeMetadataUserID{}, false
	}
	if strings.HasPrefix(userID, "{") {
		var parsed ClaudeCodeMetadataUserID
		if err := json.Unmarshal([]byte(userID), &parsed); err != nil {
			return ClaudeCodeMetadataUserID{}, false
		}
		parsed.DeviceID = strings.TrimSpace(parsed.DeviceID)
		parsed.AccountUUID = strings.TrimSpace(parsed.AccountUUID)
		parsed.SessionID = strings.TrimSpace(parsed.SessionID)
		if parsed.DeviceID == "" || parsed.SessionID == "" {
			return ClaudeCodeMetadataUserID{}, false
		}
		return parsed, true
	}
	matches := userIDPattern.FindStringSubmatch(userID)
	if len(matches) != 4 {
		return ClaudeCodeMetadataUserID{}, false
	}
	return ClaudeCodeMetadataUserID{
		DeviceID:    strings.ToLower(matches[1]),
		AccountUUID: strings.ToLower(matches[2]),
		SessionID:   strings.ToLower(matches[3]),
	}, true
}

func FormatClaudeCodeMetadataUserID(deviceID, accountUUID, sessionID, version string) string {
	deviceID = strings.TrimSpace(strings.ToLower(deviceID))
	accountUUID = strings.TrimSpace(strings.ToLower(accountUUID))
	sessionID = strings.TrimSpace(strings.ToLower(sessionID))
	if IsNewClaudeCodeMetadataUserIDVersion(version) {
		raw, _ := json.Marshal(ClaudeCodeMetadataUserID{
			DeviceID:    deviceID,
			AccountUUID: accountUUID,
			SessionID:   sessionID,
		})
		return string(raw)
	}
	return "user_" + deviceID + "_account_" + accountUUID + "_session_" + sessionID
}

func IsNewClaudeCodeMetadataUserIDVersion(version string) bool {
	return CompareSemver(version, NewClaudeCodeMetadataUserIDVersion) >= 0
}

func CompareSemver(a, b string) int {
	ap := parseSemver(a)
	bp := parseSemver(b)
	for i := 0; i < 3; i++ {
		if ap[i] < bp[i] {
			return -1
		}
		if ap[i] > bp[i] {
			return 1
		}
	}
	return 0
}

func parseSemver(version string) [3]int {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if idx := strings.IndexAny(version, " +-"); idx >= 0 {
		version = version[:idx]
	}
	parts := strings.Split(version, ".")
	out := [3]int{}
	for i := 0; i < len(parts) && i < 3; i++ {
		value, err := strconv.Atoi(parts[i])
		if err == nil {
			out[i] = value
		}
	}
	return out
}

func StableUUIDFromSeed(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return uuid.NewString()
	}
	sum := sha256.Sum256([]byte(seed))
	raw := make([]byte, 16)
	copy(raw, sum[:16])
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func FirstClaudeUserText(payload []byte) string {
	messages := gjson.GetBytes(payload, "messages")
	if !messages.IsArray() {
		return ""
	}
	first := ""
	messages.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		content := msg.Get("content")
		if content.Type == gjson.String {
			first = content.String()
			return false
		}
		if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				if block.Get("type").String() == "text" {
					first = block.Get("text").String()
					return false
				}
				return true
			})
		}
		return false
	})
	return first
}

// isValidUserID checks if a user ID is safe to send as Anthropic metadata.user_id.
func isValidUserID(userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" || len(userID) > 256 {
		return false
	}
	if parsed, ok := ParseClaudeCodeMetadataUserID(userID); ok && parsed.DeviceID != "" && parsed.SessionID != "" {
		return true
	}
	if _, err := uuid.Parse(userID); err == nil {
		return true
	}
	if strings.ContainsAny(userID, "@+ \t\r\n/\\") {
		return false
	}
	return opaqueUserIDPattern.MatchString(userID)
}

func GenerateFakeUserID() string {
	return generateFakeUserID()
}

func IsValidUserID(userID string) bool {
	return isValidUserID(userID)
}

// ShouldCloak determines if request should be cloaked based on config and client User-Agent.
// Returns true if cloaking should be applied.
func ShouldCloak(cloakMode string, userAgent string) bool {
	switch strings.ToLower(cloakMode) {
	case "always":
		return true
	case "never":
		return false
	default: // "auto" or empty
		// If client is Claude Code, don't cloak
		return !strings.HasPrefix(userAgent, "claude-cli")
	}
}

// isClaudeCodeClient checks if the User-Agent indicates a Claude Code client.
func isClaudeCodeClient(userAgent string) bool {
	return strings.HasPrefix(userAgent, "claude-cli")
}
