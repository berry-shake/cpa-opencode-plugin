import type { ReactNode } from "react";
import type { CredentialView, GoUsage, GoWindow } from "../../api/types";
import type { T } from "../../i18n";
import { barView, credState, credSubline, displayName, goLeft, isDirect, type QuotaEntry } from "../../lib/credentials";
import { formatShort, formatTime } from "../../lib/format";
import { store } from "../../state/store";

// A remaining-allowance bar: what it is and when it resets on the left, what
// is left on the right, coloured like the bar.
function Bar({ t, label, left, total, value }: { t: T; label: string; left: number; total: number; value: string }) {
  const bar = barView(left, total);
  return (
    <div className={"cap" + bar.level}>
      <div className="cap-head">
        <span className="cap-label" title={label}>{label}</span>
        <span className="cap-left">{t("usage.left")} <strong>{value}</strong></span>
      </div>
      <div className="track" role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(bar.fraction * 100)}>
        <span className="fill" style={{ width: bar.width }}></span>
      </div>
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

// One figure of a card's usage grid: label over value, details in the tooltip.
function Meter({ label, value, tip }: { label: string; value: string; tip?: string }) {
  return (
    <div className="meter">
      <div className="meter-label" title={tip ? label + " · " + tip : label}>{label}</div>
      <div className="meter-value" title={tip ? value + " · " + tip : value}>{value}</div>
    </div>
  );
}

// Four facts laid out like the usage figures; details are in the tooltip.
function Fact({ label, text, tip, extra, flag }: { label: string; text: string; tip?: string; extra?: ReactNode; flag?: string }) {
  return (
    <div className="meter">
      <div className="meter-label" title={label}>{label}{flag ? <span className="warn-text">{" · " + flag}</span> : null}</div>
      <div className="meter-value text" title={tip || text}>{text}{extra || null}</div>
    </div>
  );
}

function Facts({ t, cred }: { t: T; cred: CredentialView }) {
  const models = Array.isArray(cred.models) ? cred.models : [];
  const missing = Array.isArray(cred.missing_models) ? cred.missing_models : [];
  const modelsText = cred.all_models ? t("models.all") : t("models.selected", { count: models.length });
  const prefixText = cred.effective_prefix || t("prefix.none");
  let proxyText = t("proxy.inherit");
  let proxyTip: string | undefined;
  if (cred.proxy_name) {
    proxyText = cred.proxy_name;
    proxyTip = cred.proxy_url;
  } else if (cred.proxy_url) {
    proxyText = isDirect(cred.proxy_url) ? t("proxy.direct") : cred.proxy_url;
  }
  const ok = cred.success || 0;
  const failed = cred.failed || 0;
  return (
    <div className="facts">
      <Fact
        label={t("card.models")}
        text={modelsText}
        tip={[
          modelsText,
          cred.registered_known ? t("models.registered", { count: cred.registered_models }) : "",
          missing.length ? t("models.missing", { list: missing.join(", ") }) : "",
        ].filter(Boolean).join("\n")}
        flag={missing.length ? t("models.missingCount", { count: missing.length }) : ""}
      />
      <Fact
        label={t("card.prefix")}
        text={prefixText}
        tip={prefixText + "\n" + t(cred.prefix === null || cred.prefix === undefined ? "prefix.default" : "prefix.custom")}
      />
      <Fact label={t("card.proxy")} text={proxyText} tip={proxyTip} />
      <Fact
        label={t("card.requestsLabel")}
        text={ok + " / "}
        tip={t("card.requests", { ok, failed })}
        extra={<span className={failed ? "danger-text" : ""}>{String(failed)}</span>}
      />
    </div>
  );
}

// The card's figures (or why there are none), then the plan bars and notes.
function UsageParts({ t, cred, q, locale }: { t: T; cred: CredentialView; q: QuotaEntry | undefined; locale: string }) {
  if (cred.read_error) return null;
  const d = q && q.data;
  if (!d) {
    const load = () => void store.loadQuota(cred.auth_index);
    return (
      <div className="usage-state">
        {q && q.state === "loading" ? (
          <span>{t("usage.loading")}</span>
        ) : q && q.state === "error" ? (
          <>
            <span className="danger-text">{t("usage.failed", { msg: q.error })}</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={load}>{t("usage.retry")}</button>
          </>
        ) : (
          <>
            <span>{t(cred.disabled ? "usage.disabled" : "usage.notLoaded")}</span>
            <button type="button" className="btn btn-secondary btn-sm" onClick={load}>{t("usage.check")}</button>
          </>
        )}
      </div>
    );
  }
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

export function CredCard({ t, cred, q, armed, busy, locale }: {
  t: T;
  cred: CredentialView;
  q: QuotaEntry | undefined;
  armed: boolean;
  busy: Record<string, true>;
  locale: string;
}) {
  const [badgeClass, badgeKey] = credState(cred);
  // Why the key is off or cooling down, under the figures on its own line.
  const stateNote = cred.read_error || cred.status_message || "";
  const name = displayName(cred);
  return (
    <article className={"cred" + (cred.disabled ? " off" : "")}>
      <div className="cred-head">
        <div className="cred-row">
          <div className="cred-title">
            <span className="cred-name" title={name}>{name}</span>
            <span className={"badge " + badgeClass}>{t(badgeKey)}</span>
          </div>
        </div>
        <div className="cred-row">
          <div className="cred-file" title={cred.name}>{credSubline(cred, t("card.priority"))}</div>
        </div>
      </div>
      <div className="cred-body">
        {cred.read_error ? null : <Facts t={t} cred={cred} />}
        <UsageParts t={t} cred={cred} q={q} locale={locale} />
        {cred.note ? <div className="usage-note sub" title={cred.note}>{t("card.noteLine", { msg: cred.note })}</div> : null}
        {stateNote ? <div className={"usage-note " + (cred.read_error ? "danger-text" : "warn-text")}>{t("card.statusNote", { msg: stateNote })}</div> : null}
      </div>
      <div className="cred-foot">
        <div className="cred-meta">
          {q && q.data && q.data.fetched_at ? <span>{t("usage.fetched", { time: formatTime(q.data.fetched_at, locale) })}</span> : null}
          {!cred.read_error && q && q.data ? (
            <button type="button" className="btn-link" disabled={q.state === "loading"} onClick={() => void store.loadQuota(cred.auth_index)}>
              {t(q.state === "loading" ? "usage.loading" : "usage.refresh")}
            </button>
          ) : null}
        </div>
        <div className="row-actions">
          <button type="button" className="btn btn-secondary btn-sm" disabled={!!cred.read_error} onClick={() => store.openEditor("edit", cred)}>
            {t("row.edit")}
          </button>
          <button type="button" className="btn btn-secondary btn-sm" disabled={!!busy["toggle:" + cred.name]} onClick={() => void store.toggleDisabled(cred)}>
            {t(cred.disabled ? "row.enable" : "row.disable")}
          </button>
          <button
            type="button"
            className={"btn btn-danger btn-sm" + (armed ? " armed" : "")}
            disabled={!!busy["delete:" + cred.name]}
            onClick={() => void store.deleteCredential(cred)}
          >
            {t(armed ? "row.confirmDelete" : "row.delete")}
          </button>
        </div>
      </div>
    </article>
  );
}
