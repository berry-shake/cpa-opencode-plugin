// Package opencode holds what the plugin knows about the OpenCode Console API
// itself: endpoints, error mapping, egress (proxies and HTTP clients) and the
// Go usage it reports.
package opencode

import (
	"net/url"
	"strings"
)

// ProviderID is the CPA provider key and the auth file type.
const ProviderID = "opencode"

// Inference endpoints, relative to the base URL. The Go base URL is
// https://opencode.ai/inference/go; pay-as-you-go Console models live under
// https://opencode.ai/inference with the same paths. Every text model answers
// on all three: Console translates a request for a model whose native API is
// another one.
const (
	EndpointChat      = "/openai/v1/chat/completions"
	EndpointResponses = "/openai/v1/responses"
	EndpointMessages  = "/anthropic/v1/messages"
	// EndpointCountTokens counts the input tokens of a Messages request.
	EndpointCountTokens = "/anthropic/v1/messages/count_tokens"
	// EndpointModels lists the models with their metadata; it needs no key.
	EndpointModels = "/v1/models"
)

// EndpointGoUsage reports the Go allowance of the key's workspace: how much of
// the 5-hour, weekly and monthly limits is used. It lives on the Console
// origin, outside the inference base URL.
const EndpointGoUsage = "/zen/go/v1/usage"

// GoUsageURL is the Go usage endpoint on the origin of baseURL.
func GoUsageURL(baseURL string) string {
	parsed, errParse := url.Parse(baseURL)
	if errParse != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(baseURL, "/") + EndpointGoUsage
	}
	return parsed.Scheme + "://" + parsed.Host + EndpointGoUsage
}

// EndpointForFormat maps a CPA executor format to the OpenCode endpoint.
func EndpointForFormat(format string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "openai", "chat-completions", "chat_completions", "openai-chat-completions":
		return EndpointChat, true
	case "openai-response", "openai-responses", "responses":
		return EndpointResponses, true
	case "claude", "anthropic":
		return EndpointMessages, true
	default:
		return "", false
	}
}
