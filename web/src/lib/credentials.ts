import type { CredentialView, GoWindow, ModelView, PluginConfigView, QuotaView, StatusView } from "../api/types";
import { fileStem } from "./format";

export interface QuotaEntry {
  state: "loading" | "ok" | "error";
  data?: QuotaView;
  error?: string;
}

export type QuotaMap = Record<string, QuotaEntry | undefined>;

export function displayName(cred: Pick<CredentialView, "label" | "name" | "key_masked">): string {
  return cred.label || fileStem(cred.name) || cred.key_masked;
}

export function isDirect(proxy: string | undefined | null): boolean {
  const value = String(proxy || "").trim().toLowerCase();
  return value === "direct" || value === "none";
}

/** The credentials of a status that listed fine. */
export function credentialsOf(status: StatusView | null): CredentialView[] {
  return status && !status.list_error && Array.isArray(status.credentials) ? status.credentials : [];
}

/** Every credential of a status, even when listing reported an error. */
export function allCredentials(status: StatusView | null): CredentialView[] {
  return status && Array.isArray(status.credentials) ? status.credentials : [];
}

/** The plugin's default model prefix; "opencode" before the status arrived. */
export function defaultPrefix(status: StatusView | null): string {
  return status && status.config ? status.config.default_prefix || "" : "opencode";
}

export type BadgeClass = "danger" | "" | "warn" | "ok";

/** Badge class and message key of a credential's state. */
export function credState(cred: CredentialView): [BadgeClass, string] {
  if (cred.read_error) return ["danger", "state.readError"];
  if (cred.disabled) return ["", "state.disabled"];
  if (cred.unavailable) return ["warn", "state.unavailable"];
  return ["ok", "state.enabled"];
}

/** filter is already trimmed and lower-cased. */
export function credMatches(cred: CredentialView, filter: string): boolean {
  if (!filter) return true;
  return [cred.label, cred.key_masked, cred.name, cred.effective_prefix, cred.note]
    .some((value) => String(value || "").toLowerCase().includes(filter));
}

export type HealthState = "unknown" | "ok" | "warn" | "error";

export interface Health {
  state: HealthState;
  key: string;
  params: Record<string, number> | null;
}

export function statusHealth(data: StatusView): Health {
  const creds = Array.isArray(data.credentials) ? data.credentials : [];
  const enabled = creds.filter((c) => !c.disabled).length;
  if (data.list_error) return { state: "error", key: "health.error", params: null };
  if (!data.catalog || !data.catalog.count) return { state: "warn", key: "health.catalog", params: null };
  if (creds.length === 0) return { state: "warn", key: "health.noKeys", params: null };
  return { state: "ok", key: "health.ok", params: { enabled, total: creds.length } };
}

export type ForceState = "On" | "Off" | "Unknown";

export function forceState(config: Partial<PluginConfigView> | null | undefined): ForceState {
  const cfg = config || {};
  if (cfg.force_model_prefix === true) return "On";
  if (cfg.force_model_prefix === false) return "Off";
  return "Unknown";
}

export interface BarView {
  /** Remaining share, clamped to 0..1; 0 when total is not positive. */
  fraction: number;
  /** Extra class for the bar: " bad" under 10 %, " warn" under 30 %. */
  level: "" | " warn" | " bad";
  /** Bar width, e.g. "63.7%". */
  width: string;
}

/** A remaining-credit bar: left out of total (USD, or percent of 100). */
export function barView(left: unknown, total: unknown): BarView {
  const max = Number(total) || 0;
  const rest = Number(left) || 0;
  const fraction = max > 0 ? Math.max(0, Math.min(1, rest / max)) : 0;
  const level = fraction < 0.1 ? " bad" : fraction < 0.3 ? " warn" : "";
  return { fraction, level, width: (fraction * 100).toFixed(1) + "%" };
}

/** The share of a Go window still available, 0..1; 0 while Go is limiting it. */
export function goLeft(w: GoWindow | undefined): number | null {
  if (!w) return null;
  if (w.limited) return 0;
  return Math.max(0, Math.min(1, 1 - (Number(w.percent) || 0) / 100));
}

/** Totals for the summary strip of the credentials tab. */
export interface KeySummary {
  /** Distinct Go workspaces among the keys whose figures loaded. */
  accounts: number;
  /** Go workspaces that one of the windows is limiting right now. */
  limited: number;
  /** Workspaces without a Go subscription. */
  noGo: number;
  /** Average share left per window across the Go workspaces, or null. */
  rolling: number | null;
  weekly: number | null;
  monthly: number | null;
  loaded: number;
  pending: number;
  failed: number;
  loading: boolean;
}

function average(values: (number | null)[]): number | null {
  const known = values.filter((v): v is number => v !== null);
  return known.length ? known.reduce((a, b) => a + b, 0) / known.length : null;
}

// The Go allowance belongs to the workspace, so keys of the same workspace
// (same figures, same account value) are counted once.
export function summarizeKeys(creds: CredentialView[], quota: QuotaMap): KeySummary {
  const accounts = new Map<string, QuotaView>();
  const sum: KeySummary = {
    accounts: 0, limited: 0, noGo: 0, rolling: null, weekly: null, monthly: null,
    loaded: 0, pending: 0, failed: 0, loading: false,
  };
  for (const cred of creds) {
    if (cred.read_error) continue;
    const q = quota[cred.auth_index];
    if (q && q.state === "loading") sum.loading = true;
    const d = q && q.data;
    if (!d) {
      if (q && q.state === "error") sum.failed++;
      else if (!cred.disabled) sum.pending++;
      continue;
    }
    sum.loaded++;
    if (d.no_go || !d.go) {
      if (d.no_go) sum.noGo++;
      continue;
    }
    const account = d.account || "auth:" + cred.auth_index;
    if (!accounts.has(account)) accounts.set(account, d);
  }
  const go = [...accounts.values()].map((d) => d.go!);
  sum.accounts = go.length;
  sum.limited = go.filter((g) => [g.rolling, g.weekly, g.monthly].some((w) => w && w.limited)).length;
  sum.rolling = average(go.map((g) => goLeft(g.rolling)));
  sum.weekly = average(go.map((g) => goLeft(g.weekly)));
  sum.monthly = average(go.map((g) => goLeft(g.monthly)));
  return sum;
}

/**
 * The keys whose Go usage to load, disabled ones included, grouped by egress
 * in list order. The plugin queries one key at a time per egress, so the
 * page loads each group in turn and the groups side by side. Without force
 * only keys with no figures yet; keys whose file cannot be read are skipped.
 */
export function quotaGroups(creds: CredentialView[], quota: QuotaMap, force: boolean): string[][] {
  const groups = new Map<string, string[]>();
  for (const c of creds) {
    if (c.read_error || (!force && quota[c.auth_index])) continue;
    const key = egressKey(c.proxy_url);
    const group = groups.get(key);
    if (group) group.push(c.auth_index);
    else groups.set(key, [c.auth_index]);
  }
  return [...groups.values()];
}

/** The egress a key's proxy setting picks, as far as the page can tell. */
export function egressKey(proxy: string | undefined | null): string {
  const value = String(proxy || "").trim();
  if (!value) return "inherit";
  return isDirect(value) ? "direct" : value;
}

/**
 * The line under a credential's name: the file name when a custom label
 * hides it, the masked key and the priority.
 */
export function credSubline(cred: CredentialView, priorityLabel: string): string {
  return [
    cred.label && cred.label !== fileStem(cred.name) ? fileStem(cred.name) : "",
    cred.key_masked,
    cred.priority ? priorityLabel + " " + cred.priority : "",
  ].filter(Boolean).join(" · ");
}

export interface ModelUse {
  keys: number;
  names: Set<string>;
}

/**
 * For each model ID: the names it is registered under in CPA and how many
 * enabled keys serve it. "All models" covers the chat models only.
 */
export function modelUsage(creds: CredentialView[], models: ModelView[]): Map<string, ModelUse> {
  const usage = new Map<string, ModelUse>();
  for (const cred of creds) {
    if (cred.disabled || cred.read_error) continue;
    const ids = cred.all_models ? models.filter((m) => m.chat !== false).map((m) => m.id) : Array.isArray(cred.models) ? cred.models : [];
    const prefix = cred.effective_prefix || "";
    for (const id of ids) {
      let entry = usage.get(id);
      if (!entry) {
        entry = { keys: 0, names: new Set() };
        usage.set(id, entry);
      }
      entry.keys++;
      entry.names.add(prefix ? prefix + "/" + id : id);
    }
  }
  return usage;
}

/** Catalog models whose id, name, family or one of whose input kinds contains the filter. */
export function filterModels<M extends { id: string; name?: string; family?: string; inputs?: string[] }>(models: M[], rawFilter: string): M[] {
  const filter = rawFilter.trim().toLowerCase();
  return models.filter((m) => !filter ||
    m.id.toLowerCase().includes(filter) ||
    String(m.name || "").toLowerCase().includes(filter) ||
    String(m.family || "").toLowerCase().includes(filter) ||
    (Array.isArray(m.inputs) ? m.inputs : []).some((c) => c.toLowerCase().includes(filter)));
}
