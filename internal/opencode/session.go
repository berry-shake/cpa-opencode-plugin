package opencode

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/berry-shake/cpa-provider-core/payload"
)

// sessionHeader is the header OpenCode routes a conversation by. Go refuses
// a request without it (400 MissingSessionID), so the plugin always sends one.
const sessionHeader = "X-Opencode-Session"

// clientSessionHeaders carry a conversation ID from the client, in order of
// preference: OpenCode's own, then the native ones of Claude Code and Codex
// (which Console also recognizes) and common generic names.
var clientSessionHeaders = []string{
	sessionHeader,
	"X-Claude-Code-Session-Id",
	"Session_id",
	"Session-Id",
	"X-Session-Id",
	"Conversation_id",
	"Conversation-Id",
}

// sessionMembers are the request members a session ID is derived from when
// the client sends none.
var sessionMembers = []string{"prompt_cache_key", "metadata", "messages", "input", "system", "instructions"}

// sessionID returns the conversation ID for a request: the client's session
// header, else a prompt_cache_key or a Claude Code session in metadata.user_id,
// else a hash of how the conversation starts (system prompt and the messages
// up to the first user message), which stays the same on every turn of one
// conversation.
func sessionID(headers http.Header, body []byte, found map[string]payload.Span) string {
	for _, name := range clientSessionHeaders {
		if value := strings.TrimSpace(headers.Get(name)); value != "" && len(value) <= 256 {
			return value
		}
	}
	if key := payload.String(body, found, "prompt_cache_key"); key != "" && len(key) <= 256 {
		return key
	}
	if at, ok := found["metadata"]; ok {
		var metadata struct {
			UserID string `json:"user_id"`
		}
		if json.Unmarshal(body[at.Start:at.End], &metadata) == nil {
			if session := claudeCodeSession(metadata.UserID); session != "" {
				return session
			}
		}
	}
	hash := sha256.New()
	wrote := false
	for _, key := range []string{"system", "instructions"} {
		if at, ok := found[key]; ok {
			wrote = writeText(hash, body[at.Start:at.End]) || wrote
		}
	}
	for _, key := range []string{"messages", "input"} {
		at, ok := found[key]
		if !ok {
			continue
		}
		raw := bytes.TrimSpace(body[at.Start:at.End])
		if len(raw) == 0 {
			continue
		}
		if raw[0] != '[' {
			wrote = writeText(hash, raw) || wrote
			continue
		}
		// Up to and including the first user message: every turn of a
		// conversation starts with the same ones.
		for _, item := range firstElements(raw, 4) {
			wrote = writeText(hash, item) || wrote
			var message struct {
				Role string `json:"role"`
			}
			if json.Unmarshal(item, &message) == nil && message.Role == "user" {
				break
			}
		}
	}
	if !wrote {
		return ""
	}
	return "cpa_" + hex.EncodeToString(hash.Sum(nil))[:32]
}

// claudeCodeSession reads the session of a Claude Code metadata.user_id:
// "user_<id>_account_<id>_session_<uuid>", or a JSON object with session_id.
func claudeCodeSession(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	if strings.HasPrefix(userID, "{") {
		var parsed struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(userID), &parsed) == nil {
			return strings.TrimSpace(parsed.SessionID)
		}
		return ""
	}
	if idx := strings.LastIndex(userID, "_session_"); idx >= 0 {
		return strings.TrimSpace(userID[idx+len("_session_"):])
	}
	return ""
}

// firstElements returns up to n leading elements of a JSON array without
// decoding the rest.
func firstElements(raw []byte, n int) [][]byte {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, errToken := decoder.Token(); errToken != nil || token != json.Delim('[') {
		return nil
	}
	var out [][]byte
	for len(out) < n && decoder.More() {
		var item json.RawMessage
		if decoder.Decode(&item) != nil {
			break
		}
		out = append(out, item)
	}
	return out
}

// writeText feeds the strings of a JSON value to hash, keys in order and
// cache_control markers left out (clients move them from turn to turn). It
// reports whether anything was written.
func writeText(hash interface{ Write([]byte) (int, error) }, raw []byte) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	wrote := false
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case string:
			_, _ = hash.Write([]byte(typed))
			_, _ = hash.Write([]byte{0})
			wrote = true
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				if key != "cache_control" {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				_, _ = hash.Write([]byte(key))
				walk(typed[key])
			}
		}
	}
	walk(value)
	return wrote
}
