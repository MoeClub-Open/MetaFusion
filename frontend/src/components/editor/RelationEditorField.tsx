"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { can, CATALOG_RELATION_EDIT } from "@/lib/permissions";
import { useDefinitions, getFieldName, getRelationName } from "@/lib/definitions";
import { api, Entity, Source } from "@/components/catalog/api";
import { commitSubmitter, type CommitOperation } from "@/lib/catalogCommits";
import { fetchEntityRelationships, editableRelations } from "@/lib/catalogRelationships";
import { EntityPicker, FieldInput } from "@/components/catalog/Fields";
import { FieldValue } from "@/components/catalog/TemplateAttributeSections";
import { ConfirmDialog } from "@/components/oauth/ConfirmDialog";
import { Plus, Trash2, Pencil, ArrowLeftRight, AlertCircle, ChevronUp, ChevronDown } from "lucide-react";
import { Combobox } from "@/components/ui/Combobox";

interface Relation {
  id: string;
  version: number;
  type: string;
  source_id: string;
  target_id: string;
  position?: number;
  attributes?: Record<string, any>;
}

/** 新建条目时的待提交关系：条目还没有 id，关系先入队，保存成功后由编辑器逐条写入。 */
export interface RelationDraft {
  key: string;
  type: string;
  forward: boolean;
  targetId: string;
  position: number;
  attributes: Record<string, any>;
}

interface Props {
  /** 新建未保存时为空：此时提示先保存条目。 */
  entityId?: string;
  entityKind: string;
  /** 复用编辑器 Evidence 区的修改说明与来源：关系写入同样强制证据。 */
  note: string;
  sources: Source[];
  /** 新建条目时使用：关系先入队（见 RelationDraft），保存条目后统一写入。 */
  drafts?: RelationDraft[];
  onDraftsChange?: (next: RelationDraft[]) => void;
}

type TypeOption = { code: string; forward: boolean; targetKinds: string[] };

// 选项值编码"关系＋方向"：两端同 kind 的关系（如 Work→Work）正向/反向是两个选项。
const optionKey = (o: TypeOption) => `${o.code}|${o.forward ? "f" : "r"}`;

/**
 * RelationEditorField：通用编辑器内的关系维护区（catalog 8-kind 维度）。
 *
 * 关系是独立资源（POST/PUT/DELETE /catalog/relations），不随实体 PUT 提交，
 * 因此本组件逐条立即写入并复用表单 Evidence（edit_note + sources）。
 * 词表来自服务端 definitions：本实体可作 source 的类型按正向展示，只能作
 * target 的按反向展示；两端都可时正反均为选项（提交时按方向放置端点）。
 * 关系可携带的属性字段由关系定义的 fields 声明，创建与编辑共用同一套动态表单；
 * 已有关系的属性可就地修改（PUT，端点与类型不可变，version 乐观锁）。
 */
export function RelationEditorField({ entityId, entityKind, note, sources, drafts, onDraftsChange }: Props) {
  const { t, locale } = useI18n();
  const { definitions: defs } = useDefinitions();
  const { user } = useAuth();
  // 同一提交载荷重试时复用原请求 ID，见 commitSubmitter。
  const submitCommit = React.useRef(commitSubmitter());
  const [submission,setSubmission] = useState("");
  const push = async (ops:CommitOperation[]) => {
    const out=await submitCommit.current(ops,note,sources);
    if(!out.applied) setSubmission(out.id);
    return out;
  };
  // 关系写走独立端点（POST/PUT/DELETE /catalog/relations），服务端要求 catalog.relation.edit：
  // 无码时不渲染写入控件，避免把用户引到注定 403 的按钮上（只读列表仍展示）。
  const canWrite = can(user, CATALOG_RELATION_EDIT);

  const [items, setItems] = useState<Relation[]>([]);
  const [peers, setPeers] = useState<Record<string, Entity>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [addType, setAddType] = useState("");
  const [addTarget, setAddTarget] = useState("");
  const [addAttrs, setAddAttrs] = useState<Record<string, any>>({});
  const [editingId, setEditingId] = useState("");
  const [editAttrs, setEditAttrs] = useState<Record<string, any>>({});
  // 待确认的删除目标：确认框自绘（见文件尾），这里只记"要删哪条关系"。
  const [pendingRemove, setPendingRemove] = useState<Relation | null>(null);
  const [draftTitles, setDraftTitles] = useState<Record<string, string>>({});

  // 新建中（没有 entityId）时，关系先入队；有 onDraftsChange 且持写权限才启用队列 UI
  // （队列在条目保存后由编辑器逐条写入，无权限时同样会 403）。
  const pendingMode = !entityId && !!onDraftsChange && canWrite;
  const queue = drafts || [];

  // 待提交关系的对端标题：只对这些 id 批量取一次，避免列表里只剩裸 UUID。
  useEffect(() => {
    const ids = queue.map((d) => d.targetId).filter((id) => id && !draftTitles[id]);
    if (ids.length === 0) return;
    let active = true;
    void Promise.all(ids.map((id) => api<Entity>(`/catalog/entities/${id}`).catch(() => null))).then((list) => {
      if (!active) return;
      setDraftTitles((prev) => {
        const next = { ...prev };
        for (const e of list) if (e && e.id) next[e.id] = e.title;
        return next;
      });
    });
    return () => {
      active = false;
    };
  }, [queue, draftTitles]);

  const load = React.useCallback(async () => {
    if (!entityId) return;
    setLoading(true);
    setError("");
    try {
      const res = await fetchEntityRelationships(entityId);
      setItems(editableRelations(res.items));
      setPeers(res.entities && typeof res.entities === "object" ? res.entities : {});
    } catch (e) {
      setError(t("editor.relation.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [entityId, t]);

  useEffect(() => {
    void load();
  }, [load]);

  // 本实体可用的"关系＋方向"选项：kind 在 source_kinds → 正向；在 target_kinds → 反向。
  const typeOptions = useMemo(() => {
    const out: TypeOption[] = [];
    for (const [code, rel] of Object.entries(defs?.relations || {})) {
      if (!rel.enabled) continue;
      const inSource = rel.source_kinds?.includes(entityKind) ?? false;
      const inTarget = rel.target_kinds?.includes(entityKind) ?? false;
      // 正向：本实体为 source，对端是 target；反向反之。
      if (inSource)
        out.push({ code, forward: true, targetKinds: rel.target_kinds || [] });
      if (inTarget)
        out.push({ code, forward: false, targetKinds: rel.source_kinds || [] });
    }
    out.sort((a, b) => optionKey(a).localeCompare(optionKey(b)));
    return out;
  }, [defs, entityKind]);

  const selected = typeOptions.find((o) => optionKey(o) === addType);
  const evidenceReady =
    note.trim().length > 0 &&
    sources.length > 0 &&
    sources.every((s) => s.citation.trim().length > 0);

  // 关系定义声明的可携带属性字段（definitions.fields 引用），无声明则不出现表单。
  const relationFieldCodes = (code: string): string[] => defs?.relations?.[code]?.fields || [];

  const missingRequired = (code: string, attrs: Record<string, any>): string[] =>
    relationFieldCodes(code).filter((fc) => {
      const f = defs?.fields?.[fc];
      const v = attrs?.[fc];
      return !!f?.required && (v === undefined || v === null || v === "");
    });

  const submit = async () => {
    if (!selected || !addTarget) return;
    // 新建条目时还没有 id：关系先入队，条目保存成功后由 EntityEditor 统一写入。
    // 这一步不校验证据——证据来自同一个表单，保存时才会强制。
    if (pendingMode) {
      const missingDraft = missingRequired(selected.code, addAttrs);
      if (missingDraft.length > 0) {
        setError(
          t("editor.relation.requiredMissing", {
            fields: missingDraft.map((c) => getFieldName(defs, c, locale)).join("、"),
          }),
        );
        return;
      }
      const siblings = queue.filter((d) => d.type === selected.code && d.forward === selected.forward);
      const nextPos = siblings.reduce((max, d) => Math.max(max, d.position), -1) + 1;
      onDraftsChange?.([
        ...queue,
        {
          key: `${selected.code}|${optionKey(selected)}|${addTarget}|${nextPos}`,
          type: selected.code,
          forward: selected.forward,
          targetId: addTarget,
          position: nextPos,
          attributes: addAttrs,
        },
      ]);
      setAddType("");
      setAddTarget("");
      setAddAttrs({});
      setError("");
      return;
    }
    if (!evidenceReady) {
      setError(t("editor.relation.evidenceRequired"));
      return;
    }
    const missing = missingRequired(selected.code, addAttrs);
    if (missing.length > 0) {
      setError(
        t("editor.relation.requiredMissing", {
          fields: missing.map((c) => getFieldName(defs, c, locale)).join("、"),
        }),
      );
      return;
    }
    // 追加到同类同向关系末尾：服务端按 position 稳定排序，写死 0 会让新关系
    // 与已有关系并列、顺序不定，署名次序无法表达。
    const siblings = items.filter(
      (r) =>
        r.type === selected.code &&
        (r.source_id === entityId) === selected.forward,
    );
    const nextPosition =
      siblings.reduce((max, r) => Math.max(max, r.position || 0), -1) + 1;
    setBusy(true);
    setError("");
    try {
      // 幂等键 = 会话 + 关系载荷指纹：同一份表单内容重试（超时后重新点"添加"）复用同一个键，
      // 服务端只建一条边；换了对端/类型/属性就是新的提交意图，键随之改变。
      const relation = {
        type: selected.code,
        source_id: selected.forward ? entityId : addTarget,
        target_id: selected.forward ? addTarget : entityId,
        position: nextPosition,
        attributes: addAttrs,
      };
      await push([{target:"relation",action:"create",ref:"relation",document:relation}]);
      setAddType("");
      setAddTarget("");
      setAddAttrs({});
      await load();
    } catch (e) {
      setError(friendly((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  // move：与相邻的同类型同向关系交换 position，用于调整署名/展示次序。
  // 逐条 PUT 只改 position，不动端点与属性（PUT 需要完整关系体与 version）。
  const move = async (r: Relation, delta: -1 | 1) => {
    const forwardOf = (x: Relation) => x.source_id === entityId;
    const siblings = items.filter(
      (x) => x.type === r.type && forwardOf(x) === forwardOf(r),
    );
    const idx = siblings.findIndex((x) => x.id === r.id);
    const other = siblings[idx + delta];
    if (!other) return;
    setBusy(true);
    setError("");
    try {
      const [a,b] = [r.position || 0,other.position || 0];
      await push([{target:"relation",action:"update",id:r.id,base_version:r.version,patch:[{path:"/position",value:b===a?a+delta:b}]},
        {target:"relation",action:"update",id:other.id,base_version:other.version,patch:[{path:"/position",value:a}]}]);
      await load();
    } catch (e) {
      setError(friendly((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  const startEdit = (r: Relation) => {
    setEditingId(r.id);
    setEditAttrs({ ...(r.attributes || {}) });
    setError("");
  };

  const saveEdit = async (r: Relation) => {
    if (!evidenceReady) {
      setError(t("editor.relation.evidenceRequired"));
      return;
    }
    const missing = missingRequired(r.type, editAttrs);
    if (missing.length > 0) {
      setError(
        t("editor.relation.requiredMissing", {
          fields: missing.map((c) => getFieldName(defs, c, locale)).join("、"),
        }),
      );
      return;
    }
    setBusy(true);
    setError("");
    try {
      await push([{target:"relation",action:"update",id:r.id,base_version:r.version,patch:[{path:"/attributes",value:editAttrs}]}]);
      setEditingId("");
      setEditAttrs({});
      await load();
    } catch (e) {
      setError(friendly((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (rel: Relation) => {
    setPendingRemove(null);
    if (!evidenceReady) {
      setError(t("editor.relation.evidenceRequired"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await push([{target:"relation",action:"delete",id:rel.id,base_version:rel.version}]);
      await load();
    } catch (e) {
      setError(friendly((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  const friendly = (msg: string) =>
    msg === "forbidden" ? t("editor.relation.forbidden") : msg;

  const peerLabel = (id: string) => {
    const p = peers[id];
    if (!p) return id.slice(0, 8);
    return p.title || id.slice(0, 8);
  };

  // 关系属性动态输入区：创建与编辑共用，字段集合/类型/必填全部来自后台定义。
  const attrInputs = (
    code: string,
    attrs: Record<string, any>,
    onChange: (next: Record<string, any>) => void,
  ) => {
    const codes = relationFieldCodes(code).filter((fc) => defs?.fields?.[fc]);
    if (codes.length === 0) return null;
    return (
      <div className="cv-grid mt-2">
        {codes.map((fc) => {
          const f = defs?.fields?.[fc]!;
          return (
            <label key={fc} className="text-sm">
              {getFieldName(defs, fc, locale)}
              {f.required ? " *" : ""}
              <FieldInput
                field={f}
                value={attrs?.[fc]}
                onChange={(v) => onChange({ ...attrs, [fc]: v })}
              />
            </label>
          );
        })}
      </div>
    );
  };

  // 已有关系的属性摘要：仅显示有值的字段，枚举/实体引用按 definitions 本地化。
  const attrSummary = (r: { type: string; attributes?: Record<string, any> }) => {
    const codes = relationFieldCodes(r.type).filter((fc) => {
      const v = r.attributes?.[fc];
      return v !== undefined && v !== null && v !== "";
    });
    if (codes.length === 0) return null;
    return (
      <span className="flex w-full flex-wrap gap-1.5 text-xs opacity-80">
        {codes.map((fc) => (
          <span key={fc} className="rounded bg-black/[0.04] px-1.5 py-0.5 dark:bg-white/[0.06]">
            {getFieldName(defs, fc, locale)}:{" "}
            <FieldValue defs={defs} code={fc} value={r.attributes?.[fc]} locale={locale} />
          </span>
        ))}
      </span>
    );
  };

  // 没有 entityId 且调用方不支持队列（旧用法）：保留「先保存条目」的提示；
  // 无写权限时改为说明只读原因，不误导用户去保存条目。
  if (!entityId && !pendingMode) {
    return (
      <fieldset>
        <legend>{t("catalog.relations")}</legend>
        <p className="text-sm opacity-60">{t(canWrite ? "editor.relation.needSave" : "editor.relation.noPermission")}</p>
      </fieldset>
    );
  }

  return (
    <fieldset>
      <legend>{t("catalog.relations")}</legend>

      {submission && <Link className="text-primary underline" href={`/submissions/${submission}`}>{t("catalog.review.pending_review")} · {t("catalog.review.submission")}</Link>}
      {error && (
        <p role="alert" className="cv-error inline-flex items-center gap-1.5 text-sm text-red-600 dark:text-danger">
          <AlertCircle className="w-4 h-4 shrink-0" />
          {error}
        </p>
      )}

      {/* 待提交队列（新建条目）：保存条目时统一写入 */}
      {pendingMode && (
        <div className="space-y-1.5">
          <p className="font-mono text-xs text-text-muted">{t("editor.relation.pendingTitle")}</p>
          {queue.length === 0 ? (
            <p className="text-sm opacity-60">{t("editor.relation.pendingEmpty")}</p>
          ) : (
            <ul className="cv-relation-list space-y-1.5">
              {queue.map((d) => (
                <li key={d.key} className="cv-row flex flex-wrap items-center justify-between gap-2 text-sm">
                  <span className="inline-flex min-w-0 flex-1 flex-wrap items-center gap-2">
                    <span className="shrink-0 rounded bg-black/[0.05] px-1.5 py-0.5 font-mono text-xs dark:bg-white/[0.08]">
                      {defs?.relations?.[d.type] ? getRelationName(defs, d.type, d.forward, locale) : d.type}
                    </span>
                    {!d.forward && (
                      <ArrowLeftRight className="w-3 h-3 shrink-0 opacity-50" aria-label={t("editor.relation.reverse")} />
                    )}
                    <span className="truncate text-text-strong">{draftTitles[d.targetId] || d.targetId}</span>
                    {attrSummary(d)}
                  </span>
                  <button
                    type="button"
                    onClick={() => onDraftsChange?.((drafts || []).filter((x) => x.key !== d.key))}
                    className="inline-flex shrink-0 items-center gap-1 text-xs opacity-60 hover:opacity-100 hover:text-red-600 dark:hover:text-danger"
                    title={t("editor.relation.remove")}
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                    {t("editor.relation.remove")}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {/* 已有关系列表 */}
      {pendingMode ? null : loading ? (
        <p className="text-sm opacity-60">{t("catalog.loading")}</p>
      ) : items.length === 0 ? (
        <p className="text-sm opacity-60">{t("editor.relation.none")}</p>
      ) : (
        <ul className="cv-relation-list space-y-1.5">
          {items.map((r) => {
            const forward = r.source_id === entityId;
            const rel = defs?.relations?.[r.type];
            const label = rel
              ? getRelationName(defs, r.type, forward, locale)
              : r.type;
            const peerId = forward ? r.target_id : r.source_id;
            return (
              <React.Fragment key={r.id}>
                <li className="cv-row flex flex-wrap items-center justify-between gap-2 text-sm">
                  <span className="inline-flex min-w-0 flex-1 flex-wrap items-center gap-2">
                    <span className="shrink-0 rounded bg-black/[0.05] px-1.5 py-0.5 font-mono text-xs dark:bg-white/[0.08]">
                      {label}
                    </span>
                    {!forward && <ArrowLeftRight className="w-3 h-3 shrink-0 opacity-50" aria-label={t("editor.relation.reverse")} />}
                    <Link href={`/catalog/${peerId}`} className="truncate text-primary hover:underline">
                      {peerLabel(peerId)}
                    </Link>
                    {attrSummary(r)}
                  </span>
                  <span className="flex shrink-0 items-center gap-2">
                    {/* 署名次序：与相邻同类型同向关系交换 position。无写权限时整组隐藏。 */}
                    {canWrite && (<>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => move(r, -1)}
                      className="inline-flex items-center opacity-60 hover:opacity-100 hover:text-primary disabled:opacity-30"
                      title={t("editor.relation.moveUp")}
                      aria-label={t("editor.relation.moveUp")}
                    >
                      <ChevronUp className="w-3.5 h-3.5" />
                    </button>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => move(r, 1)}
                      className="inline-flex items-center opacity-60 hover:opacity-100 hover:text-primary disabled:opacity-30"
                      title={t("editor.relation.moveDown")}
                      aria-label={t("editor.relation.moveDown")}
                    >
                      <ChevronDown className="w-3.5 h-3.5" />
                    </button>
                    {defs?.relations?.[r.type] && (defs.relations[r.type].fields || []).length > 0 && (
                      <button
                        type="button"
                        disabled={busy || editingId === r.id}
                        onClick={() => startEdit(r)}
                        className="inline-flex items-center gap-1 text-xs opacity-60 hover:opacity-100 hover:text-primary disabled:opacity-30"
                        title={t("common.edit")}
                      >
                        <Pencil className="w-3.5 h-3.5" />
                        {t("common.edit")}
                      </button>
                    )}
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => setPendingRemove(r)}
                      className="inline-flex items-center gap-1 text-xs opacity-60 hover:opacity-100 hover:text-red-600 dark:hover:text-danger disabled:opacity-30"
                      title={t("editor.relation.remove")}
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                      {t("editor.relation.remove")}
                    </button>
                    </>)}
                  </span>
                </li>
                {editingId === r.id && (
                  <li className="cv-row rounded border p-2.5 border-line">
                    {attrInputs(r.type, editAttrs, setEditAttrs)}
                    <div className="mt-2 flex gap-2">
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => saveEdit(r)}
                        className="inline-flex items-center gap-1.5 whitespace-nowrap"
                      >
                        {t("common.save")}
                      </button>
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setEditingId("");
                          setEditAttrs({});
                        }}
                        className="inline-flex items-center gap-1.5 whitespace-nowrap"
                      >
                        {t("common.cancel")}
                      </button>
                    </div>
                  </li>
                )}
              </React.Fragment>
            );
          })}
        </ul>
      )}

      {/* 添加行：无写权限时整块不渲染（改为只读说明）。 */}
      {canWrite ? (
      <div className="cv-row mt-3 space-y-2">
        <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] sm:items-end">
          <label className="block text-sm">
            {t("editor.relation.addType")}
            <Combobox
              value={addType}
              onChange={(x) => {
                setAddType(x);
                setAddTarget("");
                setAddAttrs({});
              }}
              options={typeOptions.map((o) => {
                const name = getRelationName(defs, o.code, o.forward, locale);
                return {
                  value: optionKey(o),
                  label: o.forward ? name : `${name}（${t("editor.relation.reverse")}）`,
                  search: `${optionKey(o)} ${name}`,
                };
              })}
              placeholder={t("editor.relation.pickType")}
              searchPlaceholder={t("editor.relation.pickType")}
              aria-label={t("editor.relation.addType")}
            />
          </label>
          <div className="text-sm">
            {t("editor.relation.addTarget")}
            {selected && (
              <EntityPicker
                kinds={selected.targetKinds}
                value={addTarget}
                onChange={setAddTarget}
              />
            )}
          </div>
          <button
            type="button"
            disabled={busy || !addType || !addTarget}
            onClick={submit}
            className="inline-flex items-center justify-center gap-1.5 whitespace-nowrap"
          >
            <Plus className="w-4 h-4" />
            {t(pendingMode ? "editor.relation.addPending" : "editor.relation.add")}
          </button>
        </div>
        {selected && attrInputs(selected.code, addAttrs, setAddAttrs)}
      </div>
      ) : (
        <p className="text-sm opacity-60">{t("editor.relation.noPermission")}</p>
      )}

      {/* 删除关系是破坏性动作：确认框自绘（原生 confirm 不可本地化），说明写清"与谁、哪一类关系"。 */}
      {pendingRemove && (
        <ConfirmDialog
          open
          title={t("editor.relation.remove")}
          message={t("editor.relation.removeConfirm", {
            label: defs?.relations?.[pendingRemove.type]
              ? getRelationName(defs, pendingRemove.type, pendingRemove.source_id === entityId, locale)
              : pendingRemove.type,
            peer: peerLabel(pendingRemove.source_id === entityId ? pendingRemove.target_id : pendingRemove.source_id),
          })}
          confirmLabel={t("common.delete")}
          busy={busy}
          onClose={() => setPendingRemove(null)}
          onConfirm={() => void remove(pendingRemove)}
        />
      )}
    </fieldset>
  );
}

export default RelationEditorField;
