"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Globe, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { ApiError, fetchApi } from "@/lib/api/client";

interface PublicWikiSite {
  id: string;
  name: string;
  api_url: string;
  article_url: string;
  enabled: boolean;
  license: string;
  attribution: string;
}

interface PublicSourceSettings {
  enabled: boolean;
  wiki_sites: PublicWikiSite[];
  updated_at: string;
}

const inputClass = "w-full rounded-lg border border-line bg-surface px-3 py-2 text-sm text-text-strong focus:border-info focus:outline-none disabled:opacity-60";
const buttonClass = "inline-flex items-center justify-center gap-1.5 rounded-lg border border-line px-3 py-2 text-xs text-text-strong hover:bg-surfaceSubtle disabled:cursor-not-allowed disabled:opacity-50";

export function PublicSourcesAdmin() {
  const { t } = useI18n();
  const [settings, setSettings] = useState<PublicSourceSettings | null>(null);
  const [saved, setSaved] = useState<PublicSourceSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [errorKey, setErrorKey] = useState("");
  const [success, setSuccess] = useState(false);
  const request = useRef(0);
  const dirty = settings !== null && JSON.stringify(settings) !== JSON.stringify(saved);

  const load = useCallback(async () => {
    const version = ++request.current;
    setLoading(true);
    setErrorKey("");
    setSuccess(false);
    try {
      const result = await fetchApi<PublicSourceSettings>("/admin/public-sources");
      if (request.current !== version) return;
      setSettings(result);
      setSaved(result);
    } catch (error) {
      if (request.current !== version) return;
      setErrorKey(error instanceof ApiError && error.status === 403 ? "forbidden" : "loadFailed");
    } finally {
      if (request.current === version) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    return () => { request.current++; };
  }, [load]);

  function updateSite(index: number, change: Partial<PublicWikiSite>) {
    setSuccess(false);
    setSettings((current) => current && ({
      ...current,
      wiki_sites: current.wiki_sites.map((site, i) => i === index ? { ...site, ...change } : site),
    }));
  }

  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!settings || !saved || saving) return;
    setSuccess(false);
    setErrorKey("");
    if (new Set(settings.wiki_sites.map((site) => site.id)).size !== settings.wiki_sites.length) {
      setErrorKey("duplicateId");
      return;
    }
    setSaving(true);
    try {
      const result = await fetchApi<PublicSourceSettings>("/admin/public-sources", {
        method: "PUT",
        body: JSON.stringify({ settings, expected_updated_at: saved.updated_at }),
      });
      setSettings(result);
      setSaved(result);
      setSuccess(true);
    } catch (error) {
      setErrorKey(error instanceof ApiError && error.status === 409 ? "conflict"
        : error instanceof ApiError && error.status === 403 ? "forbidden"
        : error instanceof ApiError && error.status === 400 ? "invalidSettings" : "saveFailed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <form onSubmit={save} className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-sm font-semibold text-text-strong"><Globe className="size-4" />{t("admin.publicSources.title")}</h2>
          <p className="mt-1 max-w-3xl text-xs leading-relaxed text-text-muted">{t("admin.publicSources.description")}</p>
        </div>
        <button type="button" className={buttonClass} onClick={() => void load()} disabled={loading || saving || dirty}>
          <RefreshCw className="size-3.5" />{t("common.refresh")}
        </button>
      </div>

      {errorKey && <p role="alert" className="rounded-lg border border-danger/30 bg-danger/10 p-3 text-sm text-danger">{t(`admin.publicSources.${errorKey}`)}</p>}
      {success && <p role="status" className="text-sm text-success">{t("admin.publicSources.saved")}</p>}

      {loading ? <p role="status" className="py-8 text-sm text-text-muted">{t("common.loading")}</p> : settings && (
        <>
          <fieldset disabled={saving} className="space-y-4">
            <label className="flex items-start gap-3 rounded-lg border border-line bg-surface p-4">
              <input type="checkbox" checked={settings.enabled} className="mt-0.5 size-4 accent-sky-500" onChange={(event) => {
                setSuccess(false);
                setSettings({ ...settings, enabled: event.target.checked });
              }} />
              <span><span className="block text-sm font-medium text-text-strong">{t("admin.publicSources.enabled")}</span><span className="mt-1 block text-xs text-text-muted">{t("admin.publicSources.enabledHint")}</span></span>
            </label>

            <div className="flex items-center justify-between gap-3">
              <h3 className="text-sm font-medium text-text-strong">{t("admin.publicSources.sites")}</h3>
              <button type="button" className={buttonClass} disabled={settings.wiki_sites.length >= 32} onClick={() => {
                setSuccess(false);
                setSettings({ ...settings, wiki_sites: [...settings.wiki_sites, { id: "", name: "", api_url: "", article_url: "", enabled: true, license: "", attribution: "" }] });
              }}><Plus className="size-3.5" />{t("admin.publicSources.add")}</button>
            </div>

            <p className="text-xs leading-relaxed text-text-muted">{t("admin.publicSources.templateHint")}</p>
            {settings.wiki_sites.length === 0 && <p className="rounded-lg border border-dashed border-line p-6 text-center text-sm text-text-muted">{t("admin.publicSources.empty")}</p>}
            {settings.wiki_sites.map((site, index) => (
              <fieldset key={index} className="rounded-lg border border-line bg-surface p-4">
                <legend className="px-1 text-sm font-medium text-text-strong">{site.name || t("admin.publicSources.newSite")}</legend>
                <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
                  <label className="inline-flex items-center gap-2 text-sm text-text-strong"><input type="checkbox" checked={site.enabled} className="size-4 accent-sky-500" onChange={(event) => updateSite(index, { enabled: event.target.checked })} />{t("admin.publicSources.siteEnabled")}</label>
                  <button type="button" className={`${buttonClass} text-danger`} aria-label={t("admin.publicSources.removeSite", { name: site.name || site.id || String(index + 1) })} onClick={() => {
                    setSuccess(false);
                    setSettings({ ...settings, wiki_sites: settings.wiki_sites.filter((_, i) => i !== index) });
                  }}><Trash2 className="size-3.5" />{t("common.delete")}</button>
                </div>
                <div className="grid gap-4 md:grid-cols-2">
                  <label className="space-y-1 text-xs text-text-muted"><span>{t("admin.publicSources.id")}</span><input className={inputClass} value={site.id} required maxLength={40} pattern="[a-z][a-z0-9_-]{0,39}" onChange={(event) => updateSite(index, { id: event.target.value })} /><span className="block">{t("admin.publicSources.idHint")}</span></label>
                  <label className="space-y-1 text-xs text-text-muted"><span>{t("admin.publicSources.name")}</span><input className={inputClass} value={site.name} required maxLength={200} onChange={(event) => updateSite(index, { name: event.target.value })} /></label>
                  <label className="space-y-1 text-xs text-text-muted"><span>{t("admin.publicSources.apiUrl")}</span><input className={`${inputClass} font-mono`} value={site.api_url} required maxLength={2000} onChange={(event) => updateSite(index, { api_url: event.target.value })} /></label>
                  <label className="space-y-1 text-xs text-text-muted"><span>{t("admin.publicSources.articleUrl")}</span><input className={`${inputClass} font-mono`} value={site.article_url} required maxLength={2000} onChange={(event) => updateSite(index, { article_url: event.target.value })} /></label>
                  <label className="space-y-1 text-xs text-text-muted"><span>{t("admin.publicSources.license")}</span><input className={inputClass} value={site.license} maxLength={2000} onChange={(event) => updateSite(index, { license: event.target.value })} /></label>
                  <label className="space-y-1 text-xs text-text-muted"><span>{t("admin.publicSources.attribution")}</span><textarea rows={2} className={inputClass} value={site.attribution} maxLength={2000} onChange={(event) => updateSite(index, { attribution: event.target.value })} /></label>
                </div>
              </fieldset>
            ))}
          </fieldset>
          <div className="flex flex-wrap items-center gap-3 border-t border-line pt-4">
            <button type="submit" className={`${buttonClass} bg-sky-500 text-black hover:bg-sky-400`} disabled={saving || !dirty}><Save className="size-3.5" />{t(saving ? "common.saving" : "common.save")}</button>
            <button type="button" className={buttonClass} disabled={saving || !dirty} onClick={() => { setSettings(saved); setErrorKey(""); setSuccess(false); }}>{t("admin.publicSources.discard")}</button>
            {dirty && <span className="text-xs text-text-muted">{t("admin.publicSources.unsaved")}</span>}
          </div>
        </>
      )}
    </form>
  );
}
