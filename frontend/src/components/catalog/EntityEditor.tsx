"use client";
import React, { useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Loader2, Upload } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { api, Entity, emptyEntity, local, Source } from "./api";
import { changeDraftKind, hasKindSpecificDraftData } from "@/lib/draftKind";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { canPublishEntity } from "@/lib/permissions";
import { localizeCatalogError } from "@/lib/catalogErrors";
import { newSubmissionSession, submissionKey } from "@/lib/idempotency";
import { useAuth } from "@/lib/authContext";
import { getAuthLoginUrl } from "@/lib/services";
import { EntityPicker, Evidence, FieldInput, ErrorMessage, GroupFieldInput, NamesEditor } from "./Fields";
import { catalogEvidenceError } from "@/lib/catalogEvidence";
import { EntityCover } from "@/components/common/EntityCover";
import { LanguagePicker } from "@/components/common/LanguagePicker";
import { RelationEditorField, type RelationDraft } from "@/components/editor/RelationEditorField";
import { Select } from "@/components/ui/Select";
import { Combobox } from "@/components/ui/Combobox";
import { effectiveSchemeFields, getKindName, getTermName, matchSchemes, resolveKindOptions, templatesForEntity, useDefinitions } from "@/lib/definitions";
import { COVER_PICTURE_INDEX, MAX_ENTITY_PICTURES, PICTURE_ROLE_VOCABULARY } from "@/lib/cover";
import {
  assetContentUrl,
  bindAsset,
  completeUpload,
  initiateUpload,
  isAssetUuid,
  putFile,
  sha256HexOfFile,
  storageErrorKey,
  streamUploadUrl,
  StorageRequestError,
} from "@/lib/storage";
import { canonicalLanguageCode, languageLabel } from "@/lib/languages";
import { ApiError } from "@/lib/api/client";
import { fetchIdentityCandidates } from "@/lib/api/identity";
import { identityConflictIDs, identityReviewKey, reviewedIdentityIDs, type IdentityReviewState } from "@/lib/identityReview";
import { IdentityReviewPanel } from "./IdentityReviewPanel";

/** 标签分隔符：中英文逗号/顿号/换行都算新增，避免只能靠回车。 */
const TAG_SEPARATORS = /[,，、\n]/;

function EditorPanel({ name, active, editorId, children }: {
  name: string; active: string; editorId: string; children: React.ReactNode;
}) {
  return <section role="tabpanel" id={`${editorId}-${name}-panel`} aria-labelledby={`${editorId}-${name}-tab`}
    hidden={active !== name} className="cv-editor-panel">{children}</section>;
}

export function EntityEditor({
  initial,
  initialKind,
  onSaved,
  initialEditNote = "",
  initialSources,
}: {
  initial?: Entity;
  /** 新建时的预选层级（来自 /new?kind=…，如贡献页与 /works/new 等旧入口重定向）；
   *  只接受骨架里的合法层级，非法值回落到 emptyEntity 的默认值。 */
  initialKind?: string;
  onSaved?: (e: Entity) => void;
  initialEditNote?: string;
  initialSources?: Source[];
}) {
  const { t, tr, locale } = useI18n();
  // 会话来自 useAuth（唯一来源），定义来自 useDefinitions（唯一缓存）：
  // CatalogProvider 不再存这两份，本组件也不该依赖它被挂载。
  const { user } = useAuth();
  const { definitions, kinds: serverKinds } = useDefinitions();
  const router = useRouter();
  // definitions：定位字段等由它声明，避免编辑器写死字段码。
  const defs = definitions ?? undefined;
  // 层级名与可选项：服务端 definitions.kinds 优先，服务端未给时才退回内置骨架清单，
  // 字典只作名称兜底（缺键退原始码）；后台改骨架名/停用种类前端即跟随。
  const kindLabel = (code: string) =>
    getKindName(serverKinds, code, locale, tr(`catalog.kind.${code}`, code));
  const kindOptions = useMemo(() => resolveKindOptions(serverKinds), [serverKinds]);
  const [e, setE] = useState<Entity>(() => ({
    ...emptyEntity(initial?.kind || (initialKind && kindOptions.includes(initialKind) ? initialKind : undefined)),
    ...initial,
    attributes: initial?.attributes || {},
    translations: initial?.translations || {},
    pictures: initial?.pictures || [],
    external_ids: initial?.external_ids || {},
    contents: initial?.contents || [],
    subjects: initial?.subjects || [],
  }));
  const [selectedFields, setSelectedFields] = useState<string[]>(Object.keys(initial?.attributes || {}));
  const [selectedTemplate, setSelectedTemplate] = useState("");
  // 结构属性按 scheme 收敛：与后端同一匹配规则；无匹配 scheme 时显示
  // 全部全局子字段。定位子字段顺序：有匹配时按 scheme 并集
  // 顺序（relative_to 锚点置前），无匹配时按全局声明顺序（锚点置前）。
  const kindKey = e.kind;
  const [mediumFormat, setMediumFormat] = useState("");
  const mediumID = e.kind === "track" ? e.medium_id : undefined;
  useEffect(() => {
    setMediumFormat("");
    if (!mediumID) return;
    let active = true;
    api<Entity>(`/catalog/entities/${encodeURIComponent(mediumID)}`)
      .then((medium) => {
        if (active && medium.kind === "medium") {
          setMediumFormat(typeof medium.attributes?.format === "string" ? medium.attributes.format : "");
        }
      })
      .catch(() => { if (active) setMediumFormat(""); });
    return () => { active = false; };
  }, [mediumID]);
  const locatorFieldKeys = React.useMemo(() => {
    const matched = matchSchemes(defs as any, "locator", kindKey, mediumFormat);
    const union = effectiveSchemeFields(matched);
    const f: any = defs?.fields?.["locator"];
    const keys = union.length > 0 ? union.filter((k) => f?.fields?.[k]) : Object.keys(f?.fields || {});
    const anchor = f?.anchor_key;
    return anchor && keys.includes(anchor) ? [anchor, ...keys.filter((k) => k !== anchor)] : keys;
  }, [defs, kindKey, mediumFormat]);
  // 两个 GroupFieldInput 的收敛码：无匹配时传 undefined（显示全部全局子字段）。
  const subjectCodes = React.useMemo(() => {
    const union = effectiveSchemeFields(matchSchemes(defs as any, "subject_attributes", kindKey));
    return union.length > 0 ? union : undefined;
  }, [defs, kindKey]);
  const inclusionCodes = React.useMemo(() => {
    const union = effectiveSchemeFields(matchSchemes(defs as any, "inclusion_attributes", kindKey, mediumFormat));
    return union.length > 0 ? union : undefined;
  }, [defs, kindKey, mediumFormat]);
  const [note, setNote] = useState(initialEditNote);
  // 新建条目时关系先入队：条目拿到 id 之后再逐条写入（见 save）。
  const [pendingRelations, setPendingRelations] = useState<RelationDraft[]>([]);
  // 幂等键的会话部分：同一次编辑会话内稳定，载荷指纹在提交时算（见 lib/idempotency）。
  const submitSession = React.useRef("");
  const submissionScope = () => (submitSession.current ||= newSubmissionSession());
  const [sources, setSources] = useState<Source[]>(initialSources || [
    { kind: "url", citation: "", url: "" },
  ]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [identityReview, setIdentityReview] = useState<IdentityReviewState | null>(null);
  const currentIdentityKey = identityReviewKey(e);
  const identityKeyRef = useRef(currentIdentityKey);
  identityKeyRef.current = currentIdentityKey;
  const identityReviewRequest = useRef(0);
  useEffect(() => {
    setIdentityReview((review) => review?.key === currentIdentityKey ? review : null);
  }, [currentIdentityKey]);
  useEffect(() => () => { identityReviewRequest.current += 1; }, []);
  const editorId = React.useId();
  const [requestedSection, setRequestedSection] = useState("basic");
  const [fieldSearch, setFieldSearch] = useState("");
  const errorRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<HTMLFormElement>(null);
  const baselineRef = useRef(JSON.stringify({ entity: e, note, sources, relations: pendingRelations }));
  const initialKindAppliedRef = useRef(!!initial || kindOptions.length > 0);
  useEffect(() => {
    // 首次渲染时 definitions 可能尚未到达；预选只在种类清单加载后应用一次。
    if (initialKindAppliedRef.current || kindOptions.length === 0) return;
    initialKindAppliedRef.current = true;
    if (!initialKind || !kindOptions.includes(initialKind) || e.kind === initialKind) return;
    const selected = { ...e, kind: initialKind };
    baselineRef.current = JSON.stringify({ entity: selected, note, sources, relations: pendingRelations });
    setE(selected);
  }, [initialKind, kindOptions, e, note, sources, pendingRelations]);
  const dirty = baselineRef.current !== JSON.stringify({ entity: e, note, sources, relations: pendingRelations });
  useEffect(() => {
    if (!dirty) return;
    const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", beforeUnload);
    return () => window.removeEventListener("beforeunload", beforeUnload);
  }, [dirty]);
  useEffect(() => {
    if (!error) return;
    errorRef.current?.focus();
    errorRef.current?.scrollIntoView({ block: "center" });
  }, [error]);
  // 新增语种走可搜索的语言选择器，组件里不再留"待确认的语言代码"输入态。
  const [externalKey, setExternalKey] = useState("");
  // 标签输入框的待确认文本（回车/逗号才落到 attributes.tags）。
  const [tagInput, setTagInput] = useState("");
  // 当前编辑的语种；空串表示跟随原始语言（用户还没手动切换过）。
  const [localePick, setLocalePick] = useState("");
  const [requestedKind, setRequestedKind] = useState("");
  const pictureFileRef = useRef<HTMLInputElement | null>(null);
  const [pendingUploadIndex, setPendingUploadIndex] = useState<number | null>(null);
  const [uploadingIndex, setUploadingIndex] = useState<number | null>(null);
  const [uploadProgress, setUploadProgress] = useState(0);
  const [uploadError, setUploadError] = useState("");
  if (!definitions) return <p>{t("catalog.loading")}</p>;
  if (!user) {
    // ?edit=1 已由 AuthGate 纳入登录闸门（未登录先跳 /login 并带回完整目标）。这里只兜底
    // 闸门判定生效前的一帧与编辑中途掉登录态：所以必须留登录出口——原来只回一句没有链接、
    // 没有跳转的文本，分享出去的编辑链接就是个死胡同。
    // getAuthLoginUrl() 不带参即用 window.location.href 做回跳目标，深链逐字保留；
    // 本分支只可能在客户端渲染（上面 definitions 未就绪时已经 return）。
    return (
      <div className="space-y-3 py-6">
        <p className="text-sm text-text-muted">{t("catalog.loginToEdit")}</p>
        <a
          href={getAuthLoginUrl()}
          className="inline-flex items-center gap-1.5 px-4 h-9 rounded-lg bg-primary text-white keep-white text-sm font-semibold mf-focus"
        >
          {t("nav.login")}
        </a>
      </div>
    );
  }
  const applyKindChange = (kind: string) => {
    setE((current) => changeDraftKind(current, emptyEntity(kind)));
    setSelectedFields([]);
    setSelectedTemplate("");
    setPendingRelations([]);
    setRequestedKind("");
    setRequestedSection("basic");
    setFieldSearch("");
  };
  const d = definitions;
  const applicableFields = Object.entries(d.fields).filter(([, field]) =>
    field.enabled && field.applicable_kinds?.includes(kindKey)
  );
  // 模板仅提供字段布局。候选由模板字段与当前 kind 的适用范围交集决定。
  const templatePickOptions = (() => {
    return Object.entries(d.templates || {})
      .filter(([, tpl]) => (!tpl.kinds?.length || tpl.kinds.includes(kindKey)) &&
        tpl.sections?.some((sec) => sec.fields?.some((code) =>
          d.fields[code]?.applicable_kinds?.includes(kindKey)
        )))
      .map(([code, tpl]) => {
        const label = local(tpl.names, locale, code);
        return { value: code, label, search: `${label} ${code}` };
      });
  })();
  const applyTemplate = (tplCode: string) => {
    const template = d.templates[tplCode];
    if (!template) return;
    setSelectedTemplate(tplCode);
    const preset = Object.fromEntries((template.match || []).filter((condition) =>
      condition.operator === "equals" && d.fields[condition.field]?.applicable_kinds?.includes(kindKey)
    ).map((condition) => [condition.field,condition.value]));
    setE((current) => ({...current,attributes:{...preset,...current.attributes}}));
    setSelectedFields((current) => Array.from(new Set([
      ...current,
      ...Object.keys(preset),
      ...template.sections.flatMap((sec) => sec.fields).filter((code) => d.fields[code]?.applicable_kinds?.includes(kindKey)),
    ])));
  };
  const patch = (v: Partial<Entity>) => setE({ ...e, ...v });
  // ---- 标签：开放分类/检索词，不预置封闭的媒体或作品类型清单。----
  const tags: string[] = Array.isArray(e.attributes?.tags)
    ? (e.attributes.tags as unknown[]).map((v) => String(v ?? "").trim()).filter(Boolean)
    : [];
  const setTags = (next: string[]) => patch({ attributes: { ...e.attributes, tags: next } });
  const addTags = (raw: string) => {
    const parts = raw.split(TAG_SEPARATORS).map((s) => s.trim()).filter(Boolean);
    if (!parts.length) return;
    const seen = new Set(tags);
    setTags([...tags, ...parts.filter((v) => !seen.has(v))]);
    setTagInput("");
  };

  // ---- 多语言：选择器只渲染当前语种，语种一多不再一次铺开 ----
  // 语种标签走语言单一来源：表内语种显示「自称 (规范码)」，表外语种回落代码本身。
  const localeLabel = (code: string) => languageLabel(code);
  // chips：原始语言 + 已添加语种；原始语言不可删除（题名只读，来自实体基础题名）。
  const localeCodes = (() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const raw of [e.original_language, ...Object.keys(e.translations)]) {
      const code = String(raw || "").trim();
      if (code && !seen.has(code)) {
        seen.add(code);
        out.push(code);
      }
    }
    return out;
  })();
  const activeLocale =
    localePick || e.original_language || Object.keys(e.translations)[0] || "";
  const isOriginalLocale = !!activeLocale && activeLocale === e.original_language;
  const activeTr = e.translations[activeLocale] || { title: "", summary: "", aliases: [] };
  const setTranslation = (v: { title?: string; summary?: string; aliases?: string[] }) => {
    if (!activeLocale) return;
    patch({
      translations: {
        ...e.translations,
        [activeLocale]: {
          // 原始语言的题名就是实体基础题名（在身份区维护），这里不覆盖它。
          title: isOriginalLocale ? e.title : v.title ?? activeTr.title ?? "",
          summary: v.summary ?? activeTr.summary ?? "",
          aliases: v.aliases ?? activeTr.aliases ?? [],
        },
      },
    });
  };
  const addLocale = (raw: string) => {
    // 归一后再写入：ja / JA / jpn 都落成 ja-JP，避免同一语种在 translations 里出现两个键；
    // 不合法（含空格、空串）直接丢弃——语种是否在候选表里不设限制。
    const code = canonicalLanguageCode(raw);
    if (!code) return;
    if (!e.translations[code]) {
      // 新语种行沿用旧行为：题名先预填实体基础题名，避免空题名发不出去。
      patch({
        translations: {
          ...e.translations,
          [code]: { title: e.title, summary: "", aliases: [] },
        },
      });
    }
    setLocalePick(code);
  };
  const removeLocale = (code: string) => {
    // 原始语言行不可删：它的题名就是实体基础题名，删掉多语言区就失去锚点。
    if (code === e.original_language) return;
    patch({
      translations: Object.fromEntries(
        Object.entries(e.translations).filter(([k]) => k !== code),
      ),
    });
    if (localePick === code) setLocalePick("");
  };
  const fields = Array.from(
    new Set([
      ...applicableFields.filter(([, field]) => field.required).map(([code]) => code),
      ...selectedFields,
      ...Object.keys(e.attributes),
    ]),
  ).filter((code) => !!d.fields[code]);
  // 动态结构：按所选布局或已存字段匹配模板 sections；剩余字段归入"其它信息"。
  // hidden 只表示"不进详情信息面板"，不代表不可编辑：这类字段（存档/检索用）
  // 收进折叠区仍可维护，否则 hidden + required 会变成填不出、存不下的死锁。
  // 注意：此处位于条件 return 之后，必须用普通计算，不得改成 useMemo。
  const sections: { names: Record<string, string>; fields: string[] }[] = [];
  let restFields: string[] = [];
  let foldedFields: string[] = [];
  {
    const declared = new Set(fields);
    const seen = new Set<string>();
    // 同名分区合并，避免重复的"基本信息"区块。
    const byName = new Map<string, { names: Record<string, string>; fields: string[] }>();
    const templates = selectedTemplate
      ? [d.templates[selectedTemplate]].filter(Boolean)
      : templatesForEntity(d, kindKey, e.attributes);
    for (const tpl of templates) {
      for (const sec of tpl?.sections || []) {
        const fs = (sec.fields || []).filter(
          (f: string) =>
            declared.has(f) &&
            !seen.has(f) &&
            (!d.fields[f]?.hidden || f === "tags") &&
            d.fields[f],
        );
        if (!fs.length) continue;
        fs.forEach((f: string) => seen.add(f));
        const key = JSON.stringify(sec.names || {});
        const merged = byName.get(key);
        if (merged) merged.fields.push(...fs);
        else byName.set(key, { names: sec.names || {}, fields: [...fs] });
      }
    }
    sections.push(...Array.from(byName.values()));
    // tags 已在身份区有专用标签输入，不再在"其它信息"里重复列出。
    restFields = fields.filter((f) => f !== "tags" && !seen.has(f) && !d.fields[f]?.hidden);
    // 折叠区：hidden 字段（tags 已有专用编辑入口，不重复列出）。
    foldedFields = fields.filter(
      (f) => !seen.has(f) && !!d.fields[f]?.hidden && f !== "tags",
    );
  }
  // ---- 图片：数组顺序就是展示顺序，pictures[0] 即封面（契约见 lib/cover.ts 的 coverPicture）。
  // 换封面 = 挪到首位，不存在第二个"主图"布尔位：那样两套事实迟早互相打脸，
  // 而且 PUT 是整实体替换，顺序写错就等于把作者的排序覆盖了一遍。----
  type PictureDraft = Entity["pictures"][number];
  const setPictures = (list: PictureDraft[]) => patch({ pictures: list });
  // 只改传入的键：删某一张走下面的 filter，绝不连带重写其它图的字段。
  const patchPicture = (i: number, v: Partial<PictureDraft>) =>
    setE((current) => ({
      ...current,
      pictures: current.pictures.map((p, j) => (i === j ? { ...p, ...v } : p)),
    }));
  const swapPictures = (from: number, to: number) => {
    if (to < 0 || to >= e.pictures.length) return;
    const next = e.pictures.slice();
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    setPictures(next);
  };
  // 同实体内 URL 重复：服务端按 trim 后的原文拒 duplicate_picture，这里先给行内提示，
  // 让作者在同源热链/同一自托管对象这类真实撞车里立刻看出来是哪两张。
  const pictureUrlCounts = (() => {
    const counts = new Map<string, number>();
    for (const p of e.pictures) {
      const key = String(p?.url || "").trim();
      if (!key) continue;
      counts.set(key, (counts.get(key) || 0) + 1);
    }
    return counts;
  })();
  // 用途码词表：后台增删条目即跟随；只列启用项，但已选中的停用码留着可选
  // （历史数据里存着的码不该因为后台停用就从这个表单里消失、被迫改写）。
  const pictureRoleOptions = (code: string): { code: string; label: string }[] => {
    const terms = d.vocabularies?.[PICTURE_ROLE_VOCABULARY]?.terms || {};
    const options = Object.entries(terms)
      .filter(([k, v]) => v?.enabled || k === code)
      .map(([k]) => ({ code: k, label: getTermName(d, PICTURE_ROLE_VOCABULARY, k, locale) }));
    // 词表整体没这个码（旧文档/被删词条）时仍把当前值列出来：否则下拉看着像"未声明"，
    // 数据里其实带着一个码，作者会在不自知的情况下把它编辑掉。缺词条时标签退成原始码。
    if (code && !options.some((o) => o.code === code)) {
      options.unshift({ code, label: getTermName(d, PICTURE_ROLE_VOCABULARY, code, locale) });
    }
    return options;
  };
  // ---- 图片直传：复用 storage 服务的 sha256 → initiate → putFile → complete 链路。
  // 封面上传完整复用 storage 服务链路：sha256→initiate→put→complete→bind(role=cover_image)。
  // 新建实体无 id 时跳过 bind（asset 仍由 pictures[].asset_id 引用，保存后可补绑）。
  // 上传成功后把 asset_id 与 assetContentUrl 一次性写回该行，缩略图随之回显。
  const startPictureUpload = async (index: number, file: File) => {
    if (!file || file.size <= 0) return;
    setUploadError("");
    setUploadingIndex(index);
    setUploadProgress(0);
    try {
      const sha256 = await sha256HexOfFile(file);
      const init = await initiateUpload({
        fileName: file.name,
        fileSize: file.size,
        sha256Hash: sha256,
        mimeType: file.type || "application/octet-stream",
        // 自托管图片用途码：与 storage 预设 cover_image 一致。
        bindingRole: "cover_image",
      });
      if (!init.is_instant_upload) {
        const report = (loaded: number, total: number) =>
          setUploadProgress(total > 0 ? Math.min(99, Math.round((loaded / total) * 100)) : 0);
        const direct = init.direct_upload_url;
        const presigned = init.presigned_urls && init.presigned_urls[0];
        const runPut = (url: string) =>
          putFile(url, file, {
            onProgress: report,
            errorCode: (code, status) => new StorageRequestError(code, status),
          }).promise;
        if (direct) {
          await runPut(new URL(direct, window.location.origin).toString());
        } else if (presigned) {
          try {
            await runPut(presigned);
          } catch (err) {
            // 预签名地址不可达/CORS：回退服务端流式接收（与 EntityResourceFiles 同策略）。
            const status = err instanceof StorageRequestError ? err.status : -1;
            if (status !== 0 && status !== 403) throw err;
            await runPut(streamUploadUrl(init.asset_id));
          }
        } else {
          await runPut(streamUploadUrl(init.asset_id));
        }
      }
      setUploadProgress(100);
      await completeUpload(init.asset_id, init.upload_id);
      // 已有实体时绑定 asset 到实体（role=cover_image），新建实体跳过。
      if (e.id) { await bindAsset(init.asset_id, e.id, "cover_image"); }
      patchPicture(index, {
        asset_id: init.asset_id,
        url: assetContentUrl(init.asset_id),
      });
    } catch (err) {
      const mapped = storageErrorKey(err);
      setUploadError(mapped.vars ? t(mapped.key, mapped.vars) : t(mapped.key));
    } finally {
      setUploadingIndex(null);
      setUploadProgress(0);
    }
  };
  const onPickPictureFile = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files && e.target.files[0];
    e.target.value = "";
    if (file && pendingUploadIndex !== null) {
      void startPictureUpload(pendingUploadIndex, file);
    }
  };
  const save = async (ev: React.FormEvent) => {
    ev.preventDefault();
    // 服务端按字段的 applicable_kinds 校验；编辑器不提交布局或业务分类。
    // 应用层证据校验：HTML required 的原生气泡在部分环境不可见，
    // 曾表现为"点保存没反应"；noValidate 后统一在此给出明确提示。
    const evidenceError = catalogEvidenceError(note, sources, window.location.origin);
    if (!e.title.trim()) {
      setRequestedSection("basic");
      setError(t("editor.titleRequired"));
      return;
    }
    if (evidenceError) {
      setRequestedSection("evidence");
      setError(localizeCatalogError(evidenceError, t));
      return;
    }
    const reviewedIDs = reviewedIdentityIDs(identityReview, currentIdentityKey);
    if (identityReview?.key === currentIdentityKey && reviewedIDs === undefined) {
      setError(t("catalog.identityReview.required"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      // 建实体带上幂等键：双击保存 / 超时重发复用同一个键，服务端只建一个实体。
      // PUT 不带（幂等键服务端只覆盖两个 POST 端点，见 lib/idempotency 的说明）。
      const body = {
        entity: e, expected_version: e.version, edit_note: note, sources,
        ...(reviewedIDs !== undefined ? { reviewed_candidate_ids: reviewedIDs } : {}),
      };
      const out = await api<Entity>(
        e.id ? `/catalog/entities/${e.id}` : "/catalog/entities",
        e.id ? "PUT" : "POST",
        body,
        e.id ? undefined : { "Idempotency-Key": submissionKey(submissionScope(), body) },
      );
      // 排队的关系：条目已在，逐条写入。失败不静默——列出失败项让用户决定重试哪条。
      // 部分成功时：成功实体 id/version 写回状态（重试走 PUT，不重复建条目），
      // 失败草稿保留、只重试失败项；全部成功才清空并继续跳转。
      if ((out.id || e.id) && pendingRelations.length > 0) {
        const savedId = out.id || e.id;
        const failures: string[] = [];
        const failed: RelationDraft[] = [];
        let flushed = 0;
        for (const d of pendingRelations) {
          try {
            // 同一条待提交关系在重试里复用同一个键（键 = 会话 + 关系载荷指纹），
            // 不再每次现生成 UUID——否则超时后重试会重复建边。
            const relation = {
              type: d.type,
              source_id: d.forward ? savedId : d.targetId,
              target_id: d.forward ? d.targetId : savedId,
              position: d.position,
              attributes: d.attributes,
            };
            await api(
              "/catalog/relations",
              "POST",
              { relation, expected_version: 0, edit_note: note, sources },
              { "Idempotency-Key": submissionKey(submissionScope(), relation) },
            );
            flushed += 1;
          } catch (err) {
            failures.push(d.type + ": " + (err as Error).message);
            failed.push(d);
          }
        }
        // 写后回读：服务端返回即全量回读，写回状态使重试走 PUT。
        setE(out);
        setPendingRelations(failed);
        if (failures.length > 0) {
          setRequestedSection("relations");
          setError(
            flushed > 0
              ? t("editor.relation.flushPartial", {
                  ok: flushed,
                  fail: failures.length,
                  list: failures.join("；"),
                })
              : t("editor.relation.flushFailed", { list: failures.join("；") }),
          );
          setBusy(false);
          return;
        }
      }
      if (onSaved) onSaved(out);
      else router.push(`/catalog/${out.id}`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.message === "identity_candidates_changed") {
        setError(t("catalog.error.identityCandidatesChanged"));
        await loadIdentityReview(e, identityConflictIDs(err.details));
      } else {
        setError(localizeCatalogError((err as Error).message, t));
      }
    } finally {
      setBusy(false);
    }
  };
  const loadIdentityReview = async (entity: Entity, expectedIDs: string[] | null) => {
    const key = identityReviewKey(entity);
    if (identityKeyRef.current !== key) return;
    const request = ++identityReviewRequest.current;
    setIdentityReview({ key, expectedIDs, status: "loading", confirmed: false });
    try {
      const data = await fetchIdentityCandidates(entity);
      if (request === identityReviewRequest.current && identityKeyRef.current === key) {
        setIdentityReview({ key, expectedIDs, status: "ready", data, confirmed: false });
      }
    } catch (err) {
      if (request === identityReviewRequest.current && identityKeyRef.current === key) {
        const message = err instanceof Error ? err.message : "";
        const error = ["identity_candidates_incomplete", "identity_candidate_unresolved", "invalid_identity_review"].includes(message)
          ? localizeCatalogError(message, t) : t("catalog.identityReview.failed");
        setIdentityReview({ key, expectedIDs, status: "failed", confirmed: false, error });
      }
    }
  };
  const activeIdentityReview = identityReview?.key === currentIdentityKey ? identityReview : null;
  const identityReviewPending = activeIdentityReview !== null && reviewedIdentityIDs(activeIdentityReview, currentIdentityKey) === undefined;
  const hasStructure = !!((defs?.structure?.[e.kind]?.fields || []).length || defs?.structure?.[e.kind]?.subjects || defs?.structure?.[e.kind]?.contents);
  const editorSections = [
    { id: "basic", label: t("editor.section.basic") },
    ...(hasStructure ? [{ id: "structure", label: t("editor.section.structure") }] : []),
    ...(applicableFields.length || Object.keys(e.attributes).length ? [{ id: "attributes", label: t("catalog.attributes") }] : []),
    { id: "media", label: t("editor.section.media") },
    { id: "relations", label: t("editor.section.relations") },
    { id: "evidence", label: t("editor.section.evidence") },
  ];
  const activeSection = editorSections.some((section) => section.id === requestedSection) ? requestedSection : "basic";
  const sectionIndex = editorSections.findIndex((section) => section.id === activeSection);
  const changeSection = (id: string) => {
    setRequestedSection(id);
    editorRef.current?.scrollIntoView({ block: "start" });
  };
  const searchableFields = applicableFields.filter(([code, field]) => code !== "tags" &&
    `${code} ${local(field.names, locale, "", code)}`.toLocaleLowerCase().includes(fieldSearch.trim().toLocaleLowerCase()));
  return (
    <form ref={editorRef} onSubmit={save} noValidate className="cv-form cv-entity-editor"
      style={{ "--cv-editor-top": initial ? "calc(var(--mf-header-h) + 3rem)" : "var(--mf-header-h)" } as React.CSSProperties}>
      <div className="cv-heading">
        <h1>{t(initial ? "catalog.edit" : "catalog.create")}</h1>
        <span className="cv-hint" aria-live="polite">{t(dirty ? "editor.unsaved" : "editor.draftUnchanged")}</span>
      </div>
      <div ref={errorRef} tabIndex={-1} hidden={!error}><ErrorMessage error={error} /></div>
      {activeIdentityReview && <IdentityReviewPanel review={activeIdentityReview} busy={busy}
        definitions={definitions} kindLabel={kindLabel}
        onConfirm={(confirmed) => setIdentityReview((review) => review?.key === currentIdentityKey && review.status === "ready"
          ? { ...review, confirmed } : review)}
        onReload={() => { void loadIdentityReview(e, activeIdentityReview.expectedIDs); }} />}
      <div className="cv-editor-navigation">
        <div role="tablist" aria-label={t("editor.sections")} className="cv-editor-tabs">
          {editorSections.map((section, index) => <button type="button" role="tab" key={section.id}
            id={`${editorId}-${section.id}-tab`} aria-controls={`${editorId}-${section.id}-panel`}
            aria-selected={activeSection === section.id} tabIndex={activeSection === section.id ? 0 : -1}
            disabled={busy} onClick={() => changeSection(section.id)} onKeyDown={(event) => {
              let next = index;
              if (event.key === "ArrowRight") next = (index + 1) % editorSections.length;
              else if (event.key === "ArrowLeft") next = (index - 1 + editorSections.length) % editorSections.length;
              else if (event.key === "Home") next = 0;
              else if (event.key === "End") next = editorSections.length - 1;
              else return;
              event.preventDefault(); changeSection(editorSections[next].id);
              document.getElementById(`${editorId}-${editorSections[next].id}-tab`)?.focus();
            }}>{section.label}</button>)}
        </div>
        <div className="cv-editor-context">
          <p><span>{kindLabel(e.kind)}</span><strong>{e.title.trim() || t("editor.untitled")}</strong></p>
          <button type="submit" className="cv-primary" disabled={busy || uploadingIndex !== null || identityReviewPending}>
            {t(busy ? "catalog.saving" : "catalog.save")}
          </button>
        </div>
        {uploadingIndex !== null && <p className="cv-hint" role="status">{t("editor.pictureUploading", { progress: uploadProgress })}</p>}
      </div>
      <EditorPanel name="basic" active={activeSection} editorId={editorId}>
      <fieldset>
        <legend>{t("catalog.identity")}</legend>
        <div className="cv-grid">
          <label>
            {t("catalog.kindLabel")}
            <Select
              value={e.kind}
              disabled={!!e.id || busy || uploadingIndex !== null}
              onChange={(value) => {
                if (value === e.kind) return;
                if (hasKindSpecificDraftData(e, pendingRelations.length)) setRequestedKind(value);
                else applyKindChange(value);
              }}
              options={kindOptions.map((k) => ({ value: k, label: kindLabel(k) }))}
              aria-label={t("catalog.kindLabel")}
            />
          </label>
          <label>
            {t("catalog.title")}
            <input
              required
              value={e.title}
              onChange={(x) => patch({ title: x.target.value })}
            />
          </label>
          <label>
            {t("catalog.originalLanguage")}
            <LanguagePicker variant="field" label={languageLabel(e.original_language) || t("catalog.originalLanguage")}
              ariaLabel={t("catalog.originalLanguage")} onSelect={(code) => patch({ original_language: code })}/>
          </label>
          <label>
            {t("catalog.status")}
            <Select
              value={e.status}
              onChange={(value) => patch({ status: value })}
              options={(
                initial?.status === "published"
                  ? ["published"]
                  : [
                      "draft",
                      "pending_review",
                      // user 走审核制（草稿/待审）；editor/admin 可直接发布
                      // 自己的条目（新建无 created_by 即视为自己）。
                      ...(canPublishEntity(user, initial) ? ["published"] : []),
                    ]
              ).map((k) => ({ value: k, label: t(`catalog.state.${k}`) }))}
              aria-label={t("catalog.status")}
            />
          </label>
        </div>
        {/* 自由标签：只承载检索/分组用标签，值落在 attributes.tags，
            详情页标签区块与 /explore?tags= 检索都读它，不兼任业务分类。
            这里恒显示/提交**原始 tag code**：展示端的本地化名（getTagName）只用于读，
            不能作为输入框回显值，否则保存会把本地化名写回数据、检索参数随之失效。 */}
        <div className="cv-tags">
          <label>
            {t("catalog.tagsLabel")}
            <input
              aria-label={t("catalog.tagsLabel")}
              placeholder={t("catalog.tagsPlaceholder")}
              value={tagInput}
              onChange={(x) => setTagInput(x.target.value)}
              onKeyDown={(ev) => {
                // 输入法组合中的回车/逗号是候选确认，不能当分隔符
                if (ev.nativeEvent.isComposing) return;
                if (ev.key === "Enter" || ev.key === "," || ev.key === "，") {
                  ev.preventDefault();
                  addTags(tagInput);
                }
              }}
              onBlur={() => addTags(tagInput)}
            />
          </label>
          {tags.length > 0 && (
            <div className="cv-taglist">
              {tags.map((tag) => (
                <span key={tag} className="cv-tag">
                  {tag}
                  <button
                    type="button"
                    aria-label={`${tag} · ${t("catalog.remove")}`}
                    onClick={() => setTags(tags.filter((v) => v !== tag))}
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>
          )}
          <p className="cv-hint">{t("catalog.tagsHint")}</p>
        </div>
      </fieldset>
      <fieldset>
        <legend>{t("catalog.translations")}</legend>
        {/* 语种选择器 + 当前语种字段：语种一多不再每语种铺一块。
            下拉选项来自已添加语种；新增语种走可搜索的语言选择器（不必知道代码）。 */}
        {/* 语种切换：已有语种以 chip 直接点击切换（下方 chips），新增语种走可搜索的
            LanguagePicker（不必知道代码）。不再保留冗余的原生下拉。 */}
        <div className="cv-row cv-localebar">
          <LanguagePicker
            selected={localeCodes}
            onSelect={addLocale}
            label={t("catalog.addLocale")}
            ariaLabel={t("catalog.addLocale")}
            variant="field"
          />
        </div>
        {/* 已添加语种 chips：点击切换、× 删除；原始语言行不可删（题名只读，来自基础题名）。 */}
        <div className="cv-localechips">
          {localeCodes.map((loc) => {
            const isOriginal = loc === e.original_language;
            return (
              <span
                key={loc}
                className={`cv-localechip${loc === activeLocale ? " is-active" : ""}`}
              >
                <button
                  type="button"
                  aria-pressed={loc === activeLocale}
                  onClick={() => setLocalePick(loc)}
                >
                  {localeLabel(loc)}
                  {isOriginal && ` · ${t("revisions.fieldOriginalLanguage")}`}
                </button>
                {!isOriginal && (
                  <button
                    type="button"
                    aria-label={`${loc} · ${t("catalog.remove")}`}
                    onClick={() => removeLocale(loc)}
                  >
                    ×
                  </button>
                )}
              </span>
            );
          })}
        </div>
        {activeLocale ? (
          /* 原始语言与其它语种共用一套字段（题名/简介/别名），只是原始语言的题名只读：
             原语言简介必须落在 translations[original_language].summary，
             曾把简介写进别名字段，多语言简介等于缺失。 */
          <div className="cv-group">
            <strong>{localeLabel(activeLocale)}</strong>{" "}
            {isOriginalLocale && (
              <span className="text-xs opacity-60">
                {t("revisions.fieldOriginalLanguage")}
              </span>
            )}
            <label>
              {t("catalog.title")}
              <input
                aria-label={`${activeLocale} · ${t("catalog.title")}`}
                value={isOriginalLocale ? e.title : activeTr.title}
                disabled={isOriginalLocale}
                onChange={(x) => setTranslation({ title: x.target.value })}
              />
            </label>
            <label>
              {t("catalog.summary")}
              <textarea
                aria-label={`${activeLocale} · ${t("catalog.summary")}`}
                value={activeTr.summary || ""}
                onChange={(x) => setTranslation({ summary: x.target.value })}
              />
            </label>
            <label>
              {t("catalog.aliases")}
              <textarea
                aria-label={`${activeLocale} · ${t("catalog.aliases")}`}
                value={(activeTr.aliases || []).join("\n")}
                onChange={(x) =>
                  setTranslation({ aliases: x.target.value.split("\n").filter(Boolean) })
                }
              />
            </label>
          </div>
        ) : (
          <p className="cv-hint">{t("catalog.translationEmpty")}</p>
        )}
        <p className="text-xs opacity-60">{t("catalog.translationHint")}</p>
      </fieldset>
      </EditorPanel>
      {/* 结构区仅在当前层级有字段或收录入口时显示。 */}
      {hasStructure && (
      <EditorPanel name="structure" active={activeSection} editorId={editorId}>
      <fieldset>
        <legend>{t("catalog.structure")}</legend>
        <div className="cv-grid">
          {/* 结构字段和候选范围来自 definitions.structure。 */}
          {(defs?.structure?.[e.kind]?.fields || []).map((f) => {
            const targets = f.target_kinds && f.target_kinds.length > 0 ? f.target_kinds : [e.kind];
            const scope = f.scoped_by ? String((e as Record<string, unknown>)[f.scoped_by] || "") : "";
            return (
              <label key={f.code}>
                {local(f.names, locale, "", t("catalog.parent"))}
                <EntityPicker
                  kinds={targets}
                  query={scope ? `&${f.scoped_by}=${scope}` : ""}
                  value={String((e as Record<string, unknown>)[f.code] || "")}
                  onChange={(id) => patch({ [f.code]: id } as Partial<typeof e>)}
                />
              </label>
            );
          })}
          {["content_unit", "expression", "release", "medium", "track"].includes(e.kind) && (
            <>
              <label>
                {t("catalog.position")}
                <input
                  type="number"
                  min="0"
                  value={e.position}
                  onChange={(x) => patch({ position: Number(x.target.value) })}
                />
              </label>
              <label>
                {t("catalog.number")}
                <input
                  value={e.number}
                  onChange={(x) => patch({ number: x.target.value })}
                />
              </label>
            </>
          )}
        </div>
        {/* 发行对象入口由 definitions.structure 声明。 */}
        {defs?.structure?.[e.kind]?.subjects === true && (
          <>
            <h3>{t("catalog.subjects")}</h3>
            {e.subjects.map((s, i) => (
              <div className="cv-row" key={i}>
                <EntityPicker
                  kinds={["work"]}
                  value={s.work_id}
                  onChange={(id) =>
                    patch({
                      subjects: e.subjects.map((v, j) =>
                        i === j ? { ...v, work_id: id } : v,
                      ),
                    })
                  }
                />
                <Select
                  value={s.role}
                  onChange={(value) =>
                    patch({
                      subjects: e.subjects.map((v, j) =>
                        i === j ? { ...v, role: value } : v,
                      ),
                    })
                  }
                  options={Object.entries(d.vocabularies.release_role.terms)
                    .filter(([k, v]) => v.enabled || k === s.role)
                    .map(([k, v]) => ({ value: k, label: local(v.names, locale, "", k) }))}
                  aria-label={tr("catalog.subjectRole", "署名角色")}
                />
                {/* 发行对象附加属性：按 scheme 收敛（无匹配显示全部全局子字段）。 */}
                <GroupFieldInput
                  defs={defs}
                  code="subject_attributes"
                  codes={subjectCodes}
                  value={s.attributes}
                  onChange={(attrs) =>
                    patch({
                      subjects: e.subjects.map((v, j) =>
                        i === j ? { ...v, attributes: attrs } : v,
                      ),
                    })
                  }
                />
                <button
                  type="button"
                  onClick={() =>
                    patch({ subjects: e.subjects.filter((_, j) => i !== j) })
                  }
                >
                  {t("catalog.remove")}
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() =>
                patch({
                  subjects: [
                    ...e.subjects,
                    {
                      work_id: "",
                      role: "primary",
                      position: e.subjects.length,
                    },
                  ],
                })
              }
            >
              {t("catalog.addSubject")}
            </button>
          </>
        )}
        {/* 收录内容：由 definitions 声明（structure.track.contents）。 */}
        {defs?.structure?.[e.kind]?.contents === true && (
          <>
            <h3>{t("catalog.contents")}</h3>
            {e.contents.map((c, i) => (
              <div className="cv-group" key={i}>
                <EntityPicker
                  kinds={["expression"]}
                  value={c.expression_id}
                  onChange={(id) =>
                    patch({
                      contents: e.contents.map((v, j) =>
                        i === j ? { ...v, expression_id: id } : v,
                      ),
                    })
                  }
                />
                <div className="cv-grid">
                  <label>
                    {t("catalog.position")}
                    <input
                      type="number"
                      min="0"
                      value={c.position}
                      onChange={(x) =>
                        patch({
                          contents: e.contents.map((v, j) =>
                            i === j
                              ? { ...v, position: Number(x.target.value) }
                              : v,
                          ),
                        })
                      }
                    />
                  </label>
                  {/* 定位方案与参照选项由 definitions 的 locator 组字段声明，后台可扩展。
                      用通用 FieldInput 递归渲染：枚举、数字、文本、布尔、实体引用与
                      嵌套结构一律由字段类型决定，不在这里按类型另写一份分支。 */}
                  {locatorFieldKeys.map((k) => {
                    const def: any = defs?.fields?.locator?.fields?.[k];
                    if (!def) return null;
                    return (
                      <label key={k}>
                        {local(def.names, locale, "", k)}
                        <FieldInput
                          field={def}
                          value={c.locator[k]}
                          onChange={(value) => {
                            const locator = { ...c.locator };
                            if (value === "" || value === undefined || value === null) delete locator[k];
                            else locator[k] = value;
                            patch({
                              contents: e.contents.map((v, j) =>
                                i === j ? { ...v, locator } : v,
                              ),
                            });
                          }}
                        />
                      </label>
                    );
                  })}
                </div>
                {/* 收录附加属性：按 scheme 收敛（无匹配显示全部全局子字段）。 */}
                <GroupFieldInput
                  defs={defs}
                  code="inclusion_attributes"
                  codes={inclusionCodes}
                  value={c.attributes}
                  onChange={(attrs) =>
                    patch({
                      contents: e.contents.map((v, j) =>
                        i === j ? { ...v, attributes: attrs } : v,
                      ),
                    })
                  }
                />
                <button
                  type="button"
                  onClick={() =>
                    patch({ contents: e.contents.filter((_, j) => i !== j) })
                  }
                >
                  {t("catalog.remove")}
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() =>
                patch({
                  contents: [
                    ...e.contents,
                    {
                      expression_id: "",
                      position: e.contents.length,
                      locator: {},
                    },
                  ],
                })
              }
            >
              {t("catalog.addContent")}
            </button>
          </>
        )}
      </fieldset>
      </EditorPanel>
      )}
      {/* 关系维护：独立资源逐条提交，不复用实体 PUT；词表来自服务端 definitions。 */}
      <EditorPanel name="relations" active={activeSection} editorId={editorId}>
      <p className="cv-hint">{t(e.id ? "editor.relationsImmediate" : "editor.relationsQueued")}</p>
      <button type="button" onClick={() => changeSection("evidence")}>{t("editor.completeEvidence")}</button>
      <RelationEditorField
        entityId={e.id}
        entityKind={e.kind}
        note={note}
        sources={sources}
        drafts={pendingRelations}
        onDraftsChange={setPendingRelations}
      />
      </EditorPanel>
      {editorSections.some((section) => section.id === "attributes") && <EditorPanel name="attributes" active={activeSection} editorId={editorId}>
      <fieldset>
        <legend>{t("editor.fieldsAndLayout")}</legend>
        <p className="cv-hint">{t("editor.fieldsHint")}</p>
        {templatePickOptions.length > 0 && <label>
          {tr("editor.templatePick", "字段布局模板")}
          <Combobox value={selectedTemplate} onChange={applyTemplate} options={templatePickOptions}
            placeholder={tr("editor.templatePickPlaceholder", "选择布局模板…")}
            searchPlaceholder={tr("editor.templateSearch", "搜索模板…")}
            aria-label={tr("editor.templatePick", "字段布局模板")}/>
        </label>}
        <details className="cv-tags">
          <summary className="mf-focus"><strong>{t("catalog.availableFields")}</strong></summary>
          <label>{t("editor.searchFields")}<input type="search" value={fieldSearch} onChange={(event) => setFieldSearch(event.target.value)}/></label>
          <div className="cv-editor-field-options">
            {searchableFields.map(([code, field]) => <label key={code}>
              <input type="checkbox" checked={fields.includes(code)}
                disabled={field.required || Object.prototype.hasOwnProperty.call(e.attributes, code)}
                onChange={(event) => setSelectedFields((current) => event.target.checked ? [...current, code] : current.filter((item) => item !== code))}/>
              {local(field.names, locale, "", code)}{field.required && " *"}
            </label>)}
          </div>
          {!searchableFields.length && <p className="cv-hint">{t("editor.noMatchingFields")}</p>}
        </details>
      </fieldset>
      {(sections.length > 0 || restFields.length > 0 || foldedFields.length > 0) && (
        <fieldset>
          <legend>{t("catalog.attributes")}</legend>
          {/* 动态结构：字段按适用模板的 sections 分组（分区名/字段/次序
              全部来自服务端 definitions）；模板未覆盖的字段落入末尾"其它信息"，
              保证任何声明过的数据都可编辑。 */}
          {sections.map((sec, i) => (
            <div key={`sec${i}`} className="cv-section">
              <h4 className="cv-section-title">
                {local(sec.names, locale, "")}
              </h4>
              <div className="cv-grid">
                {sec.fields.map((k) => (
                  <label key={k}>
                    {local(d.fields[k]?.names, locale, "", k)}
                    {d.fields[k]?.required && " *"}
                    <FieldInput
                      field={d.fields[k]}
                      value={e.attributes[k]}
                      onChange={(v) =>
                        patch({ attributes: { ...e.attributes, [k]: v } })
                      }
                    />
                    {e.attributes[k] !== undefined && (
                      <button
                        type="button"
                        onClick={() => {
                          const attributes = { ...e.attributes };
                          delete attributes[k];
                          patch({ attributes });
                        }}
                      >
                        {t("catalog.remove")}
                      </button>
                    )}
                  </label>
                ))}
              </div>
            </div>
          ))}
          {restFields.length > 0 && (
            <div className="cv-section">
              <h4 className="cv-section-title">{t("catalog.otherInfo")}</h4>
              <div className="cv-grid">
                {restFields.map((k) => (
                  <label key={k}>
                    {local(d.fields[k]?.names, locale, "", k)}
                    {d.fields[k]?.required && " *"}
                    <FieldInput
                      field={d.fields[k]}
                      value={e.attributes[k]}
                      onChange={(v) =>
                        patch({ attributes: { ...e.attributes, [k]: v } })
                      }
                    />
                    {e.attributes[k] !== undefined && (
                      <button
                        type="button"
                        onClick={() => {
                          const attributes = { ...e.attributes };
                          delete attributes[k];
                          patch({ attributes });
                        }}
                      >
                        {t("catalog.remove")}
                      </button>
                    )}
                  </label>
                ))}
              </div>
            </div>
          )}
          {foldedFields.length > 0 && (
            <details className="cv-section">
              <summary className="cv-section-title">{t("catalog.hiddenFields")}</summary>
              <div className="cv-grid">
                {foldedFields.map((k) => (
                  <label key={k}>
                    {local(d.fields[k]?.names, locale, "", k)}
                    {d.fields[k]?.required && " *"}
                    <FieldInput
                      field={d.fields[k]}
                      value={e.attributes[k]}
                      onChange={(v) =>
                        patch({ attributes: { ...e.attributes, [k]: v } })
                      }
                    />
                    {e.attributes[k] !== undefined && (
                      <button
                        type="button"
                        onClick={() => {
                          const attributes = { ...e.attributes };
                          delete attributes[k];
                          patch({ attributes });
                        }}
                      >
                        {t("catalog.remove")}
                      </button>
                    )}
                  </label>
                ))}
              </div>
            </details>
          )}
        </fieldset>
      )}
      </EditorPanel>}
      <EditorPanel name="media" active={activeSection} editorId={editorId}>
      <fieldset>
        <legend>{t("catalog.externalIds")}</legend>
        {Object.entries(e.external_ids).map(([k, v]) => (
          <div key={k} className="cv-row">
            <label>
              {k}
              <input
                value={v}
                onChange={(x) =>
                  patch({
                    external_ids: { ...e.external_ids, [k]: x.target.value },
                  })
                }
              />
            </label>
            <button
              type="button"
              onClick={() =>
                patch({
                  external_ids: Object.fromEntries(
                    Object.entries(e.external_ids).filter(([key]) => key !== k),
                  ),
                })
              }
            >
              {t("catalog.remove")}
            </button>
          </div>
        ))}
        <div className="cv-row">
          <input
            value={externalKey}
            aria-label={t("catalog.code")}
            onChange={(x) => setExternalKey(x.target.value)}
          />
          <button
            type="button"
            disabled={!externalKey}
            onClick={() => {
              patch({ external_ids: { ...e.external_ids, [externalKey]: "" } });
              setExternalKey("");
            }}
          >
            {t("catalog.add")}
          </button>
        </div>
      </fieldset>
      <fieldset>
        <legend>{t("catalog.pictures")}</legend>
        <p className="cv-hint">{t("catalog.pictureOrderHint")}</p>
        <p className="cv-hint">
          {t("catalog.pictureCount", { count: e.pictures.length, max: MAX_ENTITY_PICTURES })}
        </p>
        {e.pictures.map((p, i) => {
          const urlKey = String(p.url || "").trim();
          const isDuplicate = !!urlKey && (pictureUrlCounts.get(urlKey) || 0) > 1;
          const assetId = String(p.asset_id || "").trim();
          const assetInvalid = !isAssetUuid(assetId);
          const caption = local(p.caption, locale, e.original_language, "");
          return (
            <div className="cv-group" key={i}>
              <div className="cv-row">
                <span className="flex items-center gap-1.5">
                  <strong>{t("catalog.picturePosition", { n: i + 1 })}</strong>
                  {/* 首位=封面是顺序事实，不另设"主图"勾选：一个布尔位与数组顺序并存迟早打脸。 */}
                  {i === COVER_PICTURE_INDEX && (
                    <span className="px-1.5 py-0.5 rounded-md bg-primary/15 text-primary font-mono text-[10px] font-semibold whitespace-nowrap">
                      {t("catalog.pictureCoverBadge")}
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  disabled={i === COVER_PICTURE_INDEX}
                  onClick={() => swapPictures(i, i - 1)}
                >
                  {t("catalog.pictureMoveUp")}
                </button>
                <button
                  type="button"
                  disabled={i === e.pictures.length - 1}
                  onClick={() => swapPictures(i, i + 1)}
                >
                  {t("catalog.pictureMoveDown")}
                </button>
                <button
                  type="button"
                  disabled={i === COVER_PICTURE_INDEX}
                  onClick={() => swapPictures(i, COVER_PICTURE_INDEX)}
                >
                  {t("catalog.pictureMakeCover")}
                </button>
                <button
                  type="button"
                  disabled={uploadingIndex !== null}
                  onClick={() => {
                    setPendingUploadIndex(i);
                    pictureFileRef.current?.click();
                  }}
                  title={t("catalog.upload")}
                >
                  {uploadingIndex === i ? (
                    <span className="inline-flex items-center gap-1">
                      <Loader2 className="w-3.5 h-3.5 animate-spin" />
                      {uploadProgress}%
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1">
                      <Upload className="w-3.5 h-3.5" />
                      {t("catalog.upload")}
                    </span>
                  )}
                </button>
                <button
                  type="button"
                  onClick={() => setPictures(e.pictures.filter((_, j) => i !== j))}
                >
                  {t("catalog.remove")}
                </button>
              </div>
              <div className="cv-row">
                <label>
                  {t("catalog.imageUrl")}
                  <input
                    type="url"
                    required
                    value={p.url}
                    onChange={(x) => patchPicture(i, { url: x.target.value })}
                  />
                </label>
                {/* 边填边看：地址写错、防盗链取不到图，在这里就看出来，不用等保存后回详情页。 */}
                <span
                  className="w-14 h-[4.5rem] shrink-0 rounded-md overflow-hidden border border-line bg-black/[0.04] dark:bg-black/40"
                  aria-hidden="true"
                >
                  {urlKey ? (
                    <EntityCover src={urlKey} title={caption || p.url} id={e.id} />
                  ) : null}
                </span>
              </div>
              {isDuplicate && (
                <p className="cv-error">{t("catalog.pictureDuplicateUrl")}</p>
              )}
              <div className="flex flex-col gap-1">
                <strong>{t("catalog.imageCaption")}</strong>
                <NamesEditor
                  value={p.caption || {}}
                  onChange={(captionNames) => patchPicture(i, { caption: captionNames })}
                />
              </div>
              <div className="cv-grid">
                <label>
                  {t("catalog.imageRole")}
                  <Select
                    value={p.role || ""}
                    onChange={(value) => patchPicture(i, { role: value })}
                    options={[
                      { value: "", label: t("catalog.imageRoleUndeclared") },
                      ...pictureRoleOptions(p.role || "").map((o) => ({ value: o.code, label: o.label })),
                    ]}
                    aria-label={t("catalog.imageRole")}
                  />
                </label>
                <label>
                  {t("catalog.imageTakenAt")}
                  <input
                    placeholder="2020-08-07"
                    value={p.taken_at || ""}
                    onChange={(x) => patchPicture(i, { taken_at: x.target.value })}
                  />
                </label>
              </div>
              <div className="flex flex-col gap-1">
                <strong>{t("catalog.pictureVersionLabel")}</strong>
                <NamesEditor
                  value={p.version_label || {}}
                  onChange={(version_label) => patchPicture(i, { version_label })}
                />
              </div>
              <div className="cv-grid">
                <label>
                  {t("catalog.pictureInUseFrom")}
                  <input
                    placeholder="2025-01"
                    value={p.usage_period?.begin || ""}
                    onChange={(x) => {
                      const begin = x.target.value;
                      const end = p.usage_period?.end || "";
                      patchPicture(i, { usage_period: begin || end ? { begin, end } : undefined });
                    }}
                  />
                </label>
                <label>
                  {t("catalog.pictureInUseUntil")}
                  <input
                    placeholder="2025-12"
                    value={p.usage_period?.end || ""}
                    onChange={(x) => {
                      const begin = p.usage_period?.begin || "";
                      const end = x.target.value;
                      patchPicture(i, { usage_period: begin || end ? { begin, end } : undefined });
                    }}
                  />
                </label>
              </div>
              <p className="cv-hint">{t("catalog.picturePeriodHint")}</p>
              <div className="cv-row">
                <label>
                  {t("catalog.pictureAssetId")}
                  <input
                    placeholder="00000000-0000-0000-0000-000000000000"
                    value={p.asset_id || ""}
                    onChange={(x) => patchPicture(i, { asset_id: x.target.value })}
                  />
                </label>
                {/* 自托管封面的可直链地址就在存储服务上：一键填进 URL，省得手拼错路径。 */}
                <button
                  type="button"
                  disabled={assetInvalid || !assetId}
                  onClick={() => patchPicture(i, { url: assetContentUrl(assetId) })}
                >
                  {t("catalog.pictureUseAssetUrl")}
                </button>
              </div>
              <p className="cv-hint">{t("catalog.pictureAssetHint")}</p>
              {assetInvalid && <p className="cv-error">{t("catalog.pictureAssetInvalid")}</p>}
              <p className="cv-hint">{t("catalog.evidenceSourceHint")}</p>
              <label>
                {t("catalog.sourceKind")}
                <Select
                  value={p.source.kind}
                  aria-label={t("catalog.sourceKind")}
                  onChange={(kind) => patchPicture(i, { source: { ...p.source, kind } })}
                  options={[
                    ...(!["url", "publication"].includes(p.source.kind) ? [{
                      value: p.source.kind, label: tr(`catalog.source.${p.source.kind}`, p.source.kind), disabled: true,
                    }] : []),
                    ...["url", "publication"].map((kind) => ({ value: kind, label: t(`catalog.source.${kind}`) })),
                  ]}
                />
              </label>
              <label>
                {t("catalog.citation")}
                <input
                  required
                  value={p.source.citation}
                  onChange={(x) =>
                    patchPicture(i, {
                      source: { ...p.source, citation: x.target.value },
                    })
                  }
                />
              </label>
              <label>
                {t("catalog.sourceUrl")}{p.source.kind === "url" && " *"}
                <input
                  type="url"
                  required={p.source.kind === "url"}
                  value={p.source.url || ""}
                  onChange={(x) => patchPicture(i, { source: { ...p.source, url: x.target.value } })}
                />
              </label>
            </div>
          );
        })}
        <button
          type="button"
          disabled={e.pictures.length >= MAX_ENTITY_PICTURES}
          onClick={() =>
            setPictures([
              ...e.pictures,
              {
                url: "",
                caption: {},
                taken_at: "",
                source: { kind: "url", citation: "", url: "" },
              },
            ])
          }
        >
          {t("catalog.add")}
        </button>
        {e.pictures.length >= MAX_ENTITY_PICTURES && (
          <p className="cv-error">
            {t("catalog.pictureLimitReached", { max: MAX_ENTITY_PICTURES })}
          </p>
        )}
        {uploadError && <p className="cv-error">{uploadError}</p>}
        {/* 每张图片行的"上传"按钮共用这一个隐藏文件选择器：选完即走 storage 直传链路，
            成功后把 asset_id/url 写回对应行，缩略图随 url 回显。 */}
        <input
          ref={pictureFileRef}
          type="file"
          accept="image/*"
          className="hidden"
          aria-hidden="true"
          tabIndex={-1}
          onChange={onPickPictureFile}
        />
      </fieldset>
      </EditorPanel>
      <EditorPanel name="evidence" active={activeSection} editorId={editorId}>
      <p className="cv-hint">{t("editor.evidenceHint")}</p>
      <Evidence
        note={note}
        setNote={setNote}
        sources={sources}
        setSources={setSources}
      />
      </EditorPanel>
      <div className="cv-editor-footer">
        {sectionIndex > 0 && <button type="button" disabled={busy} onClick={() => changeSection(editorSections[sectionIndex - 1].id)}>{t("editor.previousSection")}</button>}
        {sectionIndex < editorSections.length - 1
          ? <button type="button" className="cv-primary" disabled={busy} onClick={() => changeSection(editorSections[sectionIndex + 1].id)}>{t("editor.nextSection")}</button>
          : <button type="submit" className="cv-primary" disabled={busy || uploadingIndex !== null || identityReviewPending}>{t(busy ? "catalog.saving" : "catalog.save")}</button>}
      </div>
      <ConfirmDialog
        open={!!requestedKind}
        title={t("catalog.changeKind.title")}
        message={t("catalog.changeKind.warning")}
        confirmLabel={t("catalog.changeKind.confirm")}
        onClose={() => setRequestedKind("")}
        onConfirm={() => { if (requestedKind) applyKindChange(requestedKind); }}
      />
    </form>
  );
}
