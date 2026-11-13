import type { CatalogMeta, ModelView } from "../../api/types";
import { localeTag, type T } from "../../i18n";
import { allCredentials, filterModels, modelUsage } from "../../lib/credentials";
import { modelTags } from "../../lib/editor";
import { fmtTokens, fmtUSD, formatWhen } from "../../lib/format";
import { store, type State } from "../../state/store";

const COLUMNS = ["col.model", "col.capabilities", "col.context", "col.price", "col.cpaName"];

/** The served catalog: the status's copy, else the one in the loaded list. */
export function catalogMeta(s: Pick<State, "status" | "catalog">): CatalogMeta | null {
  if (s.status && s.status.catalog) return s.status.catalog;
  return s.catalog && s.catalog.catalog ? s.catalog.catalog : null;
}

function catalogHint(t: T, meta: CatalogMeta | null, locale: string): string {
  const parts: string[] = [];
  if (meta && meta.fetched_at) {
    parts.push(t("catalog.summary", { count: meta.count || 0, time: formatWhen(meta.fetched_at, locale) }));
    if (meta.checked_at && meta.checked_at !== meta.fetched_at) parts.push(t("catalog.checked", { time: formatWhen(meta.checked_at, locale) }));
  }
  return parts.join(" · ");
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

function CatalogTable({ t, s }: { t: T; s: State }) {
  const models = s.catalog && Array.isArray(s.catalog.models) ? s.catalog.models : [];
  if (models.length === 0) return <div className="empty">{t("catalog.empty")}</div>;
  const visible = filterModels(models, s.catalogFilter);
  if (visible.length === 0) return <div className="empty">{t("picker.noMatch")}</div>;
  const usage = modelUsage(allCredentials(s.status), models);
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>{COLUMNS.map((key) => <th key={key}>{t(key)}</th>)}</tr>
        </thead>
        <tbody>
          {visible.map((m) => {
            const used = usage.get(m.id);
            return (
              <tr key={m.id}>
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
                <td>
                  {used ? (
                    <>
                      {[...used.names].sort().map((name) => <div key={name} className="mono">{name}</div>)}
                      <div className="sub">{t("catalog.usedBy", { count: used.keys })}</div>
                    </>
                  ) : (
                    <span className="sub">{t("catalog.unused")}</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export function ModelsPanel({ t, s }: { t: T; s: State }) {
  const meta = catalogMeta(s);
  const locale = localeTag(s.lang);
  const models = s.catalog && Array.isArray(s.catalog.models) ? s.catalog.models : [];
  let count = "";
  if (s.catalog && models.length) {
    const filter = s.catalogFilter.trim();
    count = filter
      ? t("catalog.shown", { shown: filterModels(models, filter).length, total: models.length })
      : t("catalog.count", { count: models.length });
  }
  const failed = !!s.catalogError && !s.catalogLoading;
  return (
    <section className="panel" role="tabpanel" id="panel-models" aria-labelledby="tab-models" hidden={s.tab !== "models"}>
      <section className="card">
        <div className="card-header">
          <div className="card-title">
            <h2>{t("catalog.title")}</h2>
            <span className="hint" id="catalog-hint">{catalogHint(t, meta, locale)}</span>
          </div>
          <div className="actions">
            <button id="catalog-button" className="btn btn-secondary btn-sm" type="button" disabled={!!s.busy.catalog} onClick={() => void store.reloadCatalog()}>
              {t("actions.reloadCatalog")}
            </button>
          </div>
        </div>
        <div className="toolbar">
          <input
            className="input input-sm search"
            id="catalog-search"
            type="search"
            spellCheck={false}
            placeholder={t("catalog.search")}
            value={s.catalogFilter}
            onChange={(event) => store.setCatalogFilter(event.target.value)}
          />
          <span className="count" id="catalog-count">{count}</span>
        </div>
        <div id="catalog-body">
          {meta && meta.last_error ? (
            <div className="notice catalog-notice" data-state="error">
              <span className="notice-text">{t("catalog.syncError", { msg: meta.last_error })}</span>
            </div>
          ) : null}
          {s.catalog ? (
            <CatalogTable t={t} s={s} />
          ) : (
            <div className={"empty" + (failed ? " danger-text" : "")}>
              {failed ? t("picker.error", { msg: s.catalogError || "" }) : s.catalogLoading ? t("picker.loading") : t("keys.notLoaded")}
            </div>
          )}
        </div>
      </section>
    </section>
  );
}
