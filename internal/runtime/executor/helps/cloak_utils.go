package helps

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// userIDPattern matches Claude Code format: user_[64-hex]_account_[uuid]_session_[uuid]
var userIDPattern = regexp.MustCompile(`^user_[a-fA-F0-9]{64}_account_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}_session_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var opaqueUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,255}$`)

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

// isValidUserID checks if a user ID is safe to send as Anthropic metadata.user_id.
func isValidUserID(userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" || len(userID) > 256 {
		return false
	}
	if userIDPattern.MatchString(userID) {
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
