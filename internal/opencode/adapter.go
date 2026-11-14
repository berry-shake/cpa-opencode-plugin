package opencode

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/berry-shake/cpa-opencode-plugin/internal/web"
	"github.com/berry-shake/cpa-provider-core/config"
	"github.com/berry-shake/cpa-provider-core/credential"
	"github.com/berry-shake/cpa-provider-core/provider"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Adapter is the OpenCode provider for cpa-provider-core.
type Adapter struct {
	catalog *Catalog
}

// New returns the OpenCode adapter with an empty catalog.
func New() *Adapter {
	return &Adapter{catalog: NewCatalog()}
}

var (
	_ provider.Adapter         = (*Adapter)(nil)
	_ provider.HeaderDecorator = (*Adapter)(nil)
	_ provider.TokenCounter    = (*Adapter)(nil)
)

// Spec describes the plugin.
func (a *Adapter) Spec() provider.Spec {
	return provider.Spec{
		ID:          ProviderID,
		DisplayName: "OpenCode",
		Author:      "berry-shake",
		Repository:  "https://github.com/berry-shake/cpa-opencode-plugin",
		Config: config.Profile{
			BaseURL:     "https://opencode.ai/inference/go",
			ModelPrefix: ProviderID,
			DataDir:     "plugins/opencode-data",
			Database:    "opencode.db",
			// Accept a protocol base URL as copied from the docs, such as
			// https://opencode.ai/inference/go/openai/v1 or .../go/anthropic.
			BaseURLSuffixes:   []string{"/v1", "/openai", "/anthropic", "/google"},
			BaseURLHelp:       "OpenCode inference base URL: https://opencode.ai/inference/go for Go, https://opencode.ai/inference for pay-as-you-go models. A trailing /openai/v1 or /anthropic is dropped. Auth files may override it with base_url.",
			ModelPrefixHelp:   `Default CPA prefix for OpenCode keys ("opencode" -> opencode/kimi-k3). Empty adds no prefix. An auth file's own prefix field wins.`,
			ModelsRefreshHelp: "How often to reload OpenCode's model list; 0 disables polling.",
		},
		Page: web.Page,
		Logo: web.Logo,
		Texts: provider.Texts{
			LoginStart:          "OpenCode uses API keys: add one on the OpenCode plugin page or upload an opencode auth file",
			ModelsRoute:         "OpenCode model list with inputs, tools, reasoning efforts, context length and prices.",
			ModelsRefreshRoute:  "Reload the OpenCode model catalog and re-register changed models.",
			AddCredentialRoute:  `Add an OpenCode API key. Body: {"api_key","label","note","models","prefix","proxy_url"|"proxy_id"}.`,
			EditCredentialRoute: `Edit an OpenCode credential. Body: {"auth_index","label","note","models","prefix","proxy_url"|"proxy_id","priority"}.`,
			QuotaRoute:          "Go allowance (5-hour, weekly and monthly usage) of the OpenCode workspace behind one credential (?auth_index=).",
		},
		RequestMembers: sessionMembers,
	}
}

// Endpoint is where OpenCode serves a protocol.
func (a *Adapter) Endpoint(protocol provider.Protocol) string {
	switch protocol {
	case provider.Responses:
		return EndpointResponses
	case provider.Messages:
		return EndpointMessages
	default:
		return EndpointChat
	}
}

// Catalog is OpenCode's model list.
func (a *Adapter) Catalog() provider.CatalogSource {
	return a.catalog
}

// Decorate sets the session header OpenCode routes a conversation by (see
// sessionID). Without a session in the request it falls back to one per
// credential.
func (a *Adapter) Decorate(header http.Header, in provider.CallInput) {
	session := ""
	if in.ScanErr == nil {
		session = sessionID(in.Request.Headers, in.Request.Payload, in.Members)
	}
	if session == "" {
		session = "cpa_" + strings.ReplaceAll(strings.TrimSpace(in.Request.AuthID), " ", "_")
	}
	header.Set(sessionHeader, session)
}

// CountTokensEndpoint is where OpenCode counts a Messages request's tokens.
func (a *Adapter) CountTokensEndpoint() string {
	return EndpointCountTokens
}

// VerifyKey checks a new key with the Go usage endpoint. A 403 means the key
// works but its workspace has no Go subscription, which still leaves
// pay-as-you-go models, so only other errors fail the check.
func (a *Adapter) VerifyKey(ctx context.Context, env provider.Env, client *http.Client, cred credential.Credential) *pluginabi.Error {
	_, perr := env.Errors.GetJSON(ctx, client, GoUsageURL(env.BaseURL(cred)), cred.APIKey, env.UserAgent, EndpointGoUsage)
	if perr != nil && perr.HTTPStatus == http.StatusForbidden {
		return nil
	}
	return perr
}

// AccountKey caches account answers per key and Go usage URL.
func (a *Adapter) AccountKey(env provider.Env, cred credential.Credential) string {
	return credential.Fingerprint(cred.APIKey) + " " + GoUsageURL(env.BaseURL(cred))
}

// FetchAccount loads the Go usage of the workspace behind a key. A usage
// reading and "no Go subscription" (403) are complete answers.
func (a *Adapter) FetchAccount(env provider.Env, client *http.Client, cred credential.Credential) (any, bool, *pluginabi.Error) {
	ctx, cancel := context.WithTimeout(env.Context, provider.AccountCallTimeout)
	raw, perr := env.Errors.GetJSON(ctx, client, GoUsageURL(env.BaseURL(cred)), cred.APIKey, env.UserAgent, EndpointGoUsage)
	cancel()

	info := AccountInfo{FetchedAt: env.Now().UTC().Format(time.RFC3339)}
	switch {
	case perr != nil && perr.HTTPStatus == http.StatusForbidden:
		info.NoGo = true
	case perr != nil:
		info.Error = perr.Message
		return info, false, perr
	default:
		info.Go = GoUsageFrom(raw)
		info.Account = AccountFingerprint(info.Go)
	}
	return info, true, nil
}

// Quota turns an account answer into CPA's quota panel response.
func (a *Adapter) Quota(info any) pluginapi.QuotaFetchResponse {
	account, _ := info.(AccountInfo)
	return QuotaFromAccount(account)
}
