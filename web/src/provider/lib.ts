// OpenCode figures: the Go windows, the summary totals and model tags.

import type { CredentialView } from "@core/api/types";
import type { T } from "@core/i18n";
import type { QuotaMap } from "@core/lib/credentials";
import type { GoWindow, ModelView, QuotaView } from "./types";

/** The share of a Go window still available, 0..1; 0 while Go is limiting it. */
export function goLeft(w: GoWindow | undefined): number | null {
  if (!w) return null;
  if (w.limited) return 0;
  return Math.max(0, Math.min(1, 1 - (Number(w.percent) || 0) / 100));
}

/** The inputs shown next to a model besides text. */
export const SHOWN_INPUTS = ["image", "video", "audio", "pdf"];

/** A model's notable features, e.g. "Image · Reasoning · Tools". */
export function modelTags(t: T, model: Pick<ModelView, "inputs" | "tools" | "reasoning">): string {
  const inputs = Array.isArray(model.inputs) ? model.inputs : [];
  const tags = SHOWN_INPUTS.filter((c) => inputs.includes(c)).map((c) => t("cap." + c));
  if (model.reasoning) tags.push(t("cap.reasoning"));
  if (model.tools) tags.push(t("cap.tools"));
  return tags.join(" · ");
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
