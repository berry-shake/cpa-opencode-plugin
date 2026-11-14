import type { ProviderPlugin } from "@core/contract";
import { fmtTokens } from "@core/lib/format";
import { modelTags } from "./lib";

export const plugin: ProviderPlugin = {
  id: "opencode",
  name: "OpenCode",
  repo: "https://github.com/berry-shake/cpa-opencode-plugin",
  models: {
    matches: (m, filter) =>
      String(m.name || "").toLowerCase().includes(filter) ||
      String(m.family || "").toLowerCase().includes(filter) ||
      (Array.isArray(m.inputs) ? m.inputs : []).some((c) => c.toLowerCase().includes(filter)),
    // "All models" covers the chat models only.
    inAll: (m) => m.chat !== false,
  },
  editor: {
    init: () => ({}),
    pickName: (t, m) => ({
      text: m.chat === false ? t("picker.notChat") : modelTags(t, m),
      title: [m.name, m.family].filter(Boolean).join(" · "),
    }),
    // Context length and output limit of a model, e.g. "262K context · 66K out".
    modelMeta(t, m) {
      const parts: string[] = [];
      if (m.context_length) parts.push(t("picker.ctx", { ctx: fmtTokens(m.context_length) }));
      if (m.max_output) parts.push(t("picker.out", { out: fmtTokens(m.max_output) }));
      return parts.join(" · ");
    },
  },
};
