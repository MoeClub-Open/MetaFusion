"use client";

// 开发者中心的应用表单（新建 / 编辑）。
//
// 与账号服务的分工（契约只读核对，见 metafusion-auth/internal/handler/developer.go）：
// 这里只提交"开发者自己能改"的字段；trusted / disabled / verified 不在这条路径的写入形状里，
// 因此界面上不提供开关，也不做"看起来能改其实会被拒"的假入口。
//
// 编辑走局部更新：只有真正改动过的字段才进请求体，避免"回填即提交"把没碰过的字段一起写库。

import React, { useEffect, useState } from "react";
import { Loader2, Save } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { Modal } from "@/components/ui/Modal";
import {
  createApp,
  updateApp,
  describeDeveloperError,
  invalidHomepage,
  invalidRedirect,
  parseRedirects,
  scopeText,
  DEVELOPER_CLIENT_ID_RE,
  type DeveloperApp,
  type DeveloperAppDraft,
  type ScopeInfo,
} from "@/lib/developer";

/** 保存结果：created 时带回一次性密钥，由调用方弹展示框。 */
export type AppFormOutcome = { mode: "created" | "updated"; app: DeveloperApp; secret?: string };

type FormState = {
  clientId: string;
  name: string;
  description: string;
  homepage: string;
  redirects: string;
  scopes: string[];
};

function formOf(app: DeveloperApp | null, scopes: ScopeInfo[]): FormState {
  if (!app) {
    return {
      clientId: "",
      name: "",
      description: "",
      homepage: "",
      redirects: "",
      scopes: scopes.map((item) => item.code),
    };
  }
  return {
    clientId: app.client_id,
    name: app.name,
    description: app.description || "",
    homepage: app.homepage_url || "",
    redirects: (app.redirect_uris || []).join("\n"),
    scopes: [...(app.scopes || [])],
  };
}

function sameList(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((value, index) => value === b[index]);
}

export function AppFormModal({
  open,
  app,
  scopes,
  onClose,
  onSaved,
}: {
  open: boolean;
  app: DeveloperApp | null;
  scopes: ScopeInfo[];
  onClose: () => void;
  onSaved: (outcome: AppFormOutcome) => void;
}) {
  const { t, locale } = useI18n();
  const [form, setForm] = useState<FormState>(() => formOf(app, scopes));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // 每次打开都按当前对象重建表单：上一次的残留输入不该出现在下一次编辑里。
  useEffect(() => {
    if (open) {
      setForm(formOf(app, scopes));
      setError(null);
      setBusy(false);
    }
  }, [open, app, scopes]);

  const editing = !!app;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const clientId = form.clientId.trim();
    const name = form.name.trim();
    const description = form.description.trim();
    const homepage = form.homepage.trim();
    const redirects = parseRedirects(form.redirects);

    if (!editing && clientId && !DEVELOPER_CLIENT_ID_RE.test(clientId)) {
      setError(t("developer.form.invalidClientId"));
      return;
    }
    if (!name) {
      setError(t("developer.form.requiredName"));
      return;
    }
    if (redirects.length === 0) {
      setError(t("developer.form.requiredRedirects"));
      return;
    }
    const badRedirect = redirects.find(invalidRedirect);
    if (badRedirect) {
      setError(t("developer.form.invalidRedirect", { uri: badRedirect }));
      return;
    }
    if (invalidHomepage(homepage)) {
      setError(t("developer.form.invalidHomepage", { uri: homepage }));
      return;
    }
    if (form.scopes.length === 0) {
      setError(t("developer.form.requiredScopes"));
      return;
    }

    const draft: DeveloperAppDraft = {};
    if (!editing) {
      if (clientId) draft.client_id = clientId;
      draft.name = name;
      if (description) draft.description = description;
      if (homepage) draft.homepage_url = homepage;
      draft.redirect_uris = redirects;
      draft.scopes = form.scopes;
    } else {
      // 只提交改动过的字段：服务端是补丁语义，多传一个字段就等于改了它。
      if (name !== app.name) draft.name = name;
      if (description !== (app.description || "")) draft.description = description;
      if (homepage !== (app.homepage_url || "")) draft.homepage_url = homepage;
      if (!sameList(redirects, app.redirect_uris || [])) draft.redirect_uris = redirects;
      if (!sameList(form.scopes, app.scopes || [])) draft.scopes = form.scopes;
      if (Object.keys(draft).length === 0) {
        onClose();
        return;
      }
    }

    setBusy(true);
    try {
      if (editing) {
        const updated = await updateApp(app.client_id, draft);
        onSaved({ mode: "updated", app: updated });
      } else {
        const created = await createApp(draft);
        onSaved({ mode: "created", app: created.app, secret: created.client_secret });
      }
    } catch (err) {
      setError(describeDeveloperError(err, t));
      setBusy(false);
    }
  };

  const fieldClass =
    "w-full px-3 py-2 rounded-lg bg-surfaceSubtle border border-line text-text-body text-xs placeholder:text-text-faint focus:border-primary/50 focus:outline-none transition-colors duration-fast ease-soft";
  const labelClass = "block text-[11px] font-medium text-text-muted mb-1";

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? t("developer.form.editTitle", { name: app?.name ?? "" }) : t("developer.form.createTitle")}
      icon={<Save className="w-4 h-4 text-primary" />}
    >
      <form onSubmit={submit} className="space-y-3 text-xs">
        {!editing ? (
          <div>
            <label className={labelClass} htmlFor="mf-dev-client-id">
              {t("developer.form.clientId")}
            </label>
            <input
              id="mf-dev-client-id"
              className={fieldClass}
              value={form.clientId}
              onChange={(e) => setForm({ ...form, clientId: e.target.value })}
              placeholder="mfc-…"
            />
            <p className="mt-1 text-[11px] text-text-faint leading-relaxed">{t("developer.form.clientIdHint")}</p>
          </div>
        ) : null}

        <div>
          <label className={labelClass} htmlFor="mf-dev-name">
            {t("developer.form.name")}
          </label>
          <input
            id="mf-dev-name"
            className={fieldClass}
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder={t("developer.form.namePlaceholder")}
          />
        </div>

        <div>
          <label className={labelClass} htmlFor="mf-dev-description">
            {t("developer.form.description")}
          </label>
          <textarea
            id="mf-dev-description"
            className={fieldClass + " min-h-[64px]"}
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            placeholder={t("developer.form.descriptionPlaceholder")}
          />
        </div>

        <div>
          <label className={labelClass} htmlFor="mf-dev-homepage">
            {t("developer.form.homepage")}
          </label>
          <input
            id="mf-dev-homepage"
            className={fieldClass}
            value={form.homepage}
            onChange={(e) => setForm({ ...form, homepage: e.target.value })}
            placeholder="https://example.com"
          />
          <p className="mt-1 text-[11px] text-text-faint leading-relaxed">{t("developer.form.homepageHint")}</p>
        </div>

        <div>
          <label className={labelClass} htmlFor="mf-dev-redirects">
            {t("developer.form.redirects")}
          </label>
          <textarea
            id="mf-dev-redirects"
            className={fieldClass + " min-h-[80px] font-mono"}
            value={form.redirects}
            onChange={(e) => setForm({ ...form, redirects: e.target.value })}
            placeholder={"https://example.com/auth/callback"}
          />
          <p className="mt-1 text-[11px] text-text-faint leading-relaxed">{t("developer.form.redirectsHint")}</p>
        </div>

        <div>
          <span className={labelClass}>{t("developer.form.scopes")}</span>
          <div className="space-y-1.5">
            {scopes.map((item) => (
              <label key={item.code} className="flex items-start gap-2 text-text-body">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={form.scopes.includes(item.code)}
                  onChange={(e) =>
                    setForm({
                      ...form,
                      scopes: e.target.checked
                        ? [...form.scopes, item.code]
                        : form.scopes.filter((code) => code !== item.code),
                    })
                  }
                />
                <span>
                  <span className="font-mono text-text-strong">{item.code}</span>
                  <span className="ml-2 text-text-muted">{scopeText(item, locale, "descriptions")}</span>
                </span>
              </label>
            ))}
          </div>
          <p className="mt-1 text-[11px] text-text-faint leading-relaxed">{t("developer.form.scopesHint")}</p>
        </div>

        {error ? (
          <div className="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 text-danger-soft leading-relaxed">{error}</div>
        ) : null}

        <div className="flex justify-end gap-2 pt-1">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="px-3 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer"
          >
            {t("developer.form.cancel")}
          </button>
          <button
            type="submit"
            disabled={busy}
            className="px-4 py-1.5 rounded-lg bg-primary hover:bg-primary/90 text-primary-foreground font-semibold transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer inline-flex items-center gap-1.5"
          >
            {busy ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : null}
            <span>{busy ? t("developer.form.saving") : t("developer.form.save")}</span>
          </button>
        </div>
      </form>
    </Modal>
  );
}
