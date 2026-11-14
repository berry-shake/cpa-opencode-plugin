import { Bar, Meter, StatItem, UsagePending } from "@core/components/keys/parts";
import type { KeyCardProps, ProviderComponents } from "@core/contract";
import type { CredentialView } from "@core/api/types";
import type { T } from "@core/i18n";
import { fmtTokens, fmtUSD, formatShort } from "@core/lib/format";
import type { State } from "@core/state/store";
import { goLeft, modelTags, summarizeKeys } from "./lib";
import type { GoUsage, GoWindow, ModelView } from "./types";

// Totals over the Go workspaces whose figures have been loaded; keys of the
// same workspace count once.
function KeySummary({ t, s, creds }: { t: T; s: State; creds: CredentialView[] }) {
  const sum = summarizeKeys(creds, s.quota);
  const blank = sum.loading ? "…" : "—";
  const pct = (v: number | null) => (v === null ? blank : Math.round(v * 100) + "%");
  const goNote: string[] = [];
  if (sum.limited) goNote.push(t("summary.limited", { n: sum.limited }));
  if (sum.noGo) goNote.push(t("summary.noGo", { n: sum.noGo }));
  const missingNote: string[] = [];
  if (sum.failed) missingNote.push(t("summary.failed", { n: sum.failed }));
  if (sum.pending) missingNote.push(t("summary.pending", { n: sum.pending }));
  return (
    <div className="summary strip" id="key-summary">
      <StatItem label={t("summary.workspaces")} value={sum.accounts || sum.loaded ? String(sum.accounts) : blank} note={goNote.join(" · ")} />
      <StatItem label={t("usage.left.rolling")} value={pct(sum.rolling)} note={sum.accounts > 1 ? t("summary.average") : ""} />
      <StatItem label={t("usage.left.weekly")} value={pct(sum.weekly)} />
      <StatItem label={t("usage.left.monthly")} value={pct(sum.monthly)} note={missingNote.join(" · ")} />
    </div>
  );
}

const WINDOWS = ["rolling", "weekly", "monthly"] as const;

function pctText(left: number | null): string {
  return left === null ? "—" : Math.round(left * 100) + "%";
}

/** Used share and reset time of a Go window, for its tooltip. */
function windowTip(t: T, w: GoWindow | undefined, locale: string): string {
  if (!w) return "";
  return [
    t("usage.used", { v: Math.round(Number(w.percent) || 0) + "%" }),
    w.resets_at ? t("usage.resets", { time: formatShort(w.resets_at, locale) }) : "",
  ].filter(Boolean).join(" · ");
}

/**
 * When Go lets requests through again: the latest reset among the windows it
 * is limiting. null when none is, "" when the time is unknown.
 */
function limitedUntil(go: GoUsage): string | null {
  let until: string | null = null;
  let limited = false;
  for (const key of WINDOWS) {
    const w = go[key];
    if (!w || !w.limited) continue;
    limited = true;
    if (w.resets_at && (!until || new Date(w.resets_at) > new Date(until))) until = w.resets_at;
  }
  return limited ? until || "" : null;
}

// The card's Go windows (or why there are none), the monthly bar and notes.
function KeyUsage({ t, cred, q, locale }: KeyCardProps) {
  if (cred.read_error) return null;
  const d = q && q.data;
  if (!d) return <UsagePending t={t} cred={cred} q={q} />;
  if (d.no_go) {
    return (
      <>
        <div className="usage-state"><span>{t("usage.noGo")}</span></div>
        <div className="usage-note sub">{t("usage.noGoNote")}</div>
      </>
    );
  }
  const go = d.go || {};
  const until = limitedUntil(go);
  const monthly = go.monthly;
  const monthlyLabel = t("usage.window.monthly") + (monthly && monthly.resets_at ? " · " + t("usage.resets", { time: formatShort(monthly.resets_at, locale) }) : "");
  const monthlyLeft = goLeft(monthly);
  return (
    <>
      <div className="meters">
        {WINDOWS.map((key) => (
          <Meter key={key} label={t("usage.left." + key)} value={pctText(goLeft(go[key]))} tip={windowTip(t, go[key], locale)} />
        ))}
        {until === null
          ? <Meter label={t("usage.state")} value={d.go ? t("usage.ok") : "—"} />
          : <Meter label={t("usage.state")} value={t("usage.limited")} tip={until ? t("usage.limitedUntil", { time: formatShort(until, locale) }) : ""} />}
      </div>
      {monthly && monthlyLeft !== null ? (
        <Bar t={t} label={monthlyLabel} left={monthlyLeft} total={1} value={pctText(monthlyLeft)} />
      ) : null}
      {until !== null ? (
        <div className="usage-note warn-text">{until ? t("usage.limitedUntil", { time: formatShort(until, locale) }) : t("usage.limited")}</div>
      ) : null}
      {d.error ? <div className="usage-note danger-text">{t("usage.failed", { msg: d.error })}</div> : null}
      {q && q.state === "error" ? <div className="usage-note danger-text">{t("usage.failed", { msg: q.error })}</div> : null}
    </>
  );
}

// Input and output price per million tokens, with the cache, long-context
// and peak-hour prices beneath.
function PriceCell({ t, m }: { t: T; m: ModelView }) {
  const p = m.pricing;
  if (!p) return <td className="nowrap"><span className="sub">—</span></td>;
  if (p.free) return <td className="nowrap"><span className="ok-text">{t("catalog.free")}</span></td>;
  const notes: string[] = [];
  if (p.cache_read || p.cache_write) {
    notes.push(t("catalog.cache", { read: fmtUSD(p.cache_read || 0), write: fmtUSD(p.cache_write || 0) }));
  }
  if (p.long_above) {
    notes.push(t("catalog.longPrice", { above: fmtTokens(p.long_above), input: fmtUSD(p.long_input), output: fmtUSD(p.long_output) }));
  }
  if (p.peak_factor && p.peak_factor !== 1) notes.push(t("catalog.peak", { factor: p.peak_factor }));
  return (
    <td className="nowrap">
      <div>{t("catalog.price", { input: fmtUSD(p.input), output: fmtUSD(p.output) })}</div>
      {notes.map((note) => <div key={note} className="sub">{note}</div>)}
    </td>
  );
}

function ModelCells({ t, m }: { t: T; m: ModelView }) {
  return (
    <>
      <td>
        <div className="mono strong">{m.id}</div>
        {m.name || m.family ? <div className="sub" title={m.description || ""}>{[m.name, m.family].filter(Boolean).join(" · ")}</div> : null}
      </td>
      <td>
        <div>{modelTags(t, m) || "—"}</div>
        {Array.isArray(m.reasoning_efforts) && m.reasoning_efforts.length ? (
          <div className="sub">{t("catalog.efforts", { v: m.reasoning_efforts.join(" / ") })}</div>
        ) : null}
        {m.chat === false ? <div className="sub warn-text">{t("catalog.notChat")}</div> : null}
      </td>
      <td className="nowrap">
        <div>{m.context_length ? fmtTokens(m.context_length) : "—"}</div>
        {m.max_output ? <div className="sub">{t("catalog.maxOutput", { v: fmtTokens(m.max_output) })}</div> : null}
      </td>
      <PriceCell t={t} m={m} />
    </>
  );
}

export const components: ProviderComponents = {
  KeySummary,
  KeyUsage,
  modelColumns: ["col.model", "col.capabilities", "col.context", "col.price"],
  ModelCells,
};
