// The OpenCode shapes of the management API: the catalog model with its
// price, and the Go allowance of the workspace behind a key.

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

export interface CredentialExtra {}

export interface CatalogExtra {}

export interface CredentialSaveExtra {}

export interface EditorExtra {}
