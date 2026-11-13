// JSON shapes served by the plugin's management API. Field names match the Go
// structs' json tags; optional fields are the ones tagged omitempty.

export interface PluginConfigView {
  base_url: string;
  default_prefix: string;
  /** Mirrors CPA's routing.force-model-prefix; absent when unknown. */
  force_model_prefix?: boolean;
  models_refresh_minutes: number;
  request_timeout_seconds: number;
  stream_idle_timeout_seconds: number;
  global_proxy?: string;
}

export interface CatalogMeta {
  count: number;
  revision?: string;
  fetched_at?: string;
  checked_at?: string;
  last_error?: string;
}

export interface CredentialView {
  auth_index: string;
  id: string;
  name: string;
  label: string;
  key_masked: string;
  disabled: boolean;
  unavailable: boolean;
  status?: string;
  status_message?: string;
  priority: number;
  note?: string;
  base_url?: string;
  proxy_url?: string;
  /** Set when proxy_url is exactly the address of a proxy in the list. */
  proxy_id?: string;
  proxy_name?: string;
  /** null means the plugin's default prefix applies. */
  prefix: string | null;
  effective_prefix: string;
  all_models: boolean;
  models: string[] | null;
  missing_models?: string[];
  registered_models: number;
  registered_known: boolean;
  success: number;
  failed: number;
  read_error?: string;
}

export interface StatusView {
  plugin: string;
  version: string;
  now: string;
  config: PluginConfigView;
  catalog: CatalogMeta;
  credentials: CredentialView[];
  list_error?: string;
}

/** A model's text price in US dollars per million tokens. */
export interface ModelPricing {
  input: number;
  output: number;
  cache_read?: number;
  cache_write?: number;
  /** Above this context length long_input / long_output apply. */
  long_above?: number;
  long_input?: number;
  long_output?: number;
  /** Price multiplier at peak hours. */
  peak_factor?: number;
  free?: boolean;
}

/** An OpenCode model from /v1/models. */
export interface ModelView {
  id: string;
  name?: string;
  description?: string;
  family?: string;
  /** False for models that do not generate text; "all models" leaves them out. */
  chat: boolean;
  /** Accepted inputs: text, image, video, audio, pdf. */
  inputs?: string[];
  tools?: boolean;
  reasoning?: boolean;
  /** Named reasoning efforts, e.g. low / high / max. */
  reasoning_efforts?: string[];
  context_length?: number;
  max_output?: number;
  /** The API the model is served by natively; Console translates the others. */
  native_api?: string;
  open_weights?: boolean;
  release_date?: string;
  pricing?: ModelPricing;
}

export interface ModelsResponse {
  catalog: CatalogMeta;
  models: ModelView[];
}

/** One window of the Go allowance. */
export interface GoWindow {
  /** How much of the window's allowance is used, 0 to 100. */
  percent: number;
  /** Go refuses requests until the window resets. */
  limited: boolean;
  resets_at?: string;
}

/** The Go allowance: 5-hour rolling, weekly and monthly windows. */
export interface GoUsage {
  rolling?: GoWindow;
  weekly?: GoWindow;
  monthly?: GoWindow;
}

/** The Go allowance of the workspace behind a key; workspace-wide. */
export interface QuotaView {
  /** Same value for keys whose figures read the same (taken as one workspace). */
  account?: string;
  go?: GoUsage;
  /** The workspace does not subscribe to Go. */
  no_go?: boolean;
  error?: string;
  fetched_at: string;
}

/** Body of POST (add) and PATCH (edit) /credentials. */
export interface CredentialSaveRequest {
  auth_index?: string;
  name?: string;
  api_key?: string;
  skip_verify?: boolean;
  label?: string;
  /** "" removes the note. */
  note?: string;
  proxy_url?: string;
  /** Uses the address of this list proxy; wins over proxy_url. */
  proxy_id?: string;
  /** null resets to the plugin default. */
  prefix?: string | null;
  /** null means all models. */
  models?: string[] | null;
  /** null clears the priority. */
  priority?: number | null;
}

export interface CredentialSaveResponse {
  ok: boolean;
  credential?: CredentialView;
  unknown_models?: string[];
}

export interface LogEntry {
  id: string;
  request_id?: string;
  time: string;
  endpoint: string;
  model: string;
  upstream_model?: string;
  served_model?: string;
  reasoning_effort?: string;
  stream: boolean;
  status: string;
  http_status?: number;
  credential?: string;
  auth_id?: string;
  duration_ms: number;
  ttft_ms?: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cache_write_tokens?: number;
  reasoning_tokens?: number;
  output_tokens_per_second?: number;
  error_code?: string;
  error?: string;
  error_phase?: string;
  // Absent when CPA did not report them.
  client_path?: string;
  user_agent?: string;
  auth_index?: string;
  /** "direct", "env" or the proxy as scheme://host:port. */
  egress?: string;
  /** What OpenCode answered; 0 or absent when no response arrived. */
  upstream_status?: number;
  upstream_request_id?: string;
  retry_after?: number;
  finish_reason?: string;
  frames?: number;
  last_frame_ms?: number;
  /** Set only once OpenCode answered 2xx: whether the response carried usage. */
  usage_reported?: boolean;
  /** How many entries share the request ID (credentials CPA tried). */
  attempts?: number;
}

export interface LogSummary {
  requests: number;
  success: number;
  failed: number;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cache_hit_rate: number | null;
  output_tokens_per_second: number | null;
}

export interface LogPage {
  items: LogEntry[];
  total: number;
  offset: number;
  retention: number;
  retention_days: number;
  summary: LogSummary;
  error?: string;
}

/** A saved proxy; url has its password redacted. */
export interface ProxyView {
  id: string;
  name: string;
  url: string;
  /** scheme://host:port */
  label: string;
  created_at?: string;
  /** The latest check of the current address, saved by the plugin. */
  last_check?: ProxyCheckResult;
}

export interface ProxiesResponse {
  proxies: ProxyView[];
  /** CPA's global proxy-url, redacted; absent when not set. */
  global_proxy?: string;
  /** The latest checks of the two built-in rows. */
  builtin_checks?: Partial<Record<"direct" | "inherit", ProxyCheckResult>>;
  load_error?: string;
}

/** Body of POST (add, without id) and PATCH (edit) /proxies. */
export interface ProxySaveRequest {
  id?: string;
  name?: string;
  /** Absent when editing keeps the saved address. */
  url?: string;
}

export interface ProxySaveResponse {
  ok: boolean;
  proxy?: ProxyView;
  /** Keys moved to the new address when the address changed. */
  updated_credentials?: number;
  failed_credentials?: string[];
}

export interface ProxyImportResponse {
  ok: boolean;
  added: number;
  proxies: ProxyView[];
}

/** Response of POST /proxies/order: the list in its saved order. */
export interface ProxyOrderResponse {
  ok: boolean;
  proxies: ProxyView[];
}

export type ProxyCheckTarget = { id: string } | { target: "direct" | "inherit" };

/** An egress check; a failed check is ok: false with error, still HTTP 200. */
export interface ProxyCheckResult {
  ok: boolean;
  /** "direct", "env" or the proxy as scheme://host:port. */
  egress?: string;
  ip?: string;
  /** ISO 3166-1 alpha-2 code when the lookup service gave one. */
  country?: string;
  region?: string;
  city?: string;
  latency_ms?: number;
  source?: string;
  checked_at?: string;
  error?: string;
}

export interface ErrorResponse {
  error?: string;
  message?: string;
}
