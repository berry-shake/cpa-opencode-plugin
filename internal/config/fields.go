package config

import "github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"

// Fields describes the config keys for CPA's plugin settings form.
func Fields() []pluginapi.ConfigField {
	return []pluginapi.ConfigField{
		{Name: "base-url", Type: pluginapi.ConfigFieldTypeString, Default: defaultBaseURL, Description: "OpenCode inference base URL: https://opencode.ai/inference/go for Go, https://opencode.ai/inference for pay-as-you-go models. A trailing /openai/v1 or /anthropic is dropped. Auth files may override it with base_url."},
		{Name: "model-prefix", Type: pluginapi.ConfigFieldTypeString, Default: defaultModelPrefix, Description: `Default CPA prefix for OpenCode keys ("opencode" -> opencode/kimi-k3). Empty adds no prefix. An auth file's own prefix field wins.`},
		{Name: "models-refresh-minutes", Type: pluginapi.ConfigFieldTypeInteger, Default: defaultModelsRefreshMinutes, Description: "How often to reload OpenCode's model list; 0 disables polling."},
		{Name: "request-timeout-seconds", Type: pluginapi.ConfigFieldTypeInteger, Default: defaultRequestTimeoutSeconds, Description: "Timeout for non-streaming requests and for stream response headers."},
		{Name: "stream-idle-timeout-seconds", Type: pluginapi.ConfigFieldTypeInteger, Default: defaultStreamIdleSeconds, Description: "Abort a stream after this long without upstream bytes."},
		{Name: "data-dir", Type: pluginapi.ConfigFieldTypeString, Default: defaultDataDir, Description: "Directory for plugin data: the request log database (opencode.db) and the proxy list (proxies.json), relative to CPA's working directory."},
		{Name: "log-retention", Type: pluginapi.ConfigFieldTypeInteger, Default: defaultLogRetention, Description: "Most requests the request log keeps; 0 disables it."},
		{Name: "log-retention-days", Type: pluginapi.ConfigFieldTypeInteger, Default: defaultLogRetentionDays, Description: "Drop request log entries older than this many days; 0 keeps them until log-retention is reached."},
	}
}
