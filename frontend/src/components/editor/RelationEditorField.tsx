"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { can, CATALOG_RELATION_EDIT } from "@/lib/permissions";
import { useDefinitions, getFieldName, getRelationName } from "@/lib/definitions";
import { api, type Entity } from "@/components/catalog/api";
import { checkoutWorkspace, getWorkspace, type Workspace, type WorkspaceOperation } from "@/lib/api/collaboration";
import { stageEditingWorkspace } from "@/lib/catalogCommits";
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

export function relationWorkspaceItems(current: Workspace, entityId: string, mainRelations: Relation[]): Relation[] {
  const projected = new Map(mainRelations.map((relation) => [relation.id, relation]));
  for (const object of current.objects) {
    if (object.target !== "relation") continue;
    projected.delete(object.id);
    const relation = object.document as unknown as Relation | null;
    if (relation && (relation.source_id === entityId || relation.target_id === entityId)) {
      projected.set(object.id, { ...relation, id: object.id, version: object.base_version });
    }
  }
  return [...projected.values()].sort((a, b) => (a.position || 0) - (b.position || 0) || a.id.localeCompare(b.id));
}

export async function checkoutRelationObjects(current: Workspace, relationIds: string[], signal?: AbortSignal): Promise<Workspace> {
  for (let offset = 0; offset < relationIds.length; offset += 100) {
    signal?.throwIfAborted();
    current = await checkoutWorkspace(current.id, {
      expected_version: current.version, entity_ids: [], relation_ids: relationIds.slice(offset, offset + 100),
    }, signal);
  }
  signal?.throwIfAborted();
  return current;
}

/** 尚未分配副本实体 ID 时，随条目一起保存的关系。 */
export interface RelationDraft {
  key: string;
  type: string;
  forward: boolean;
  targetId: string;
  position: number;
  attributes: Record<string, any>;
}

interface Props {
  workspaceId: string;
  workspaceVersion: number;
  onWorkspaceChange?: (workspace: Workspace) => void;
  /** 新建条目首次保存之前为空。 */
  entityId?: string;
  entityKind: string;
  drafts?: RelationDraft[];
  onDraftsChange?: (next: RelationDraft[]) => void;
}

type TypeOption = { code: string; forward: boolean; targetKinds: string[] };

// 选项值编码"关系＋方向"：两端同 kind 的关系（如 Work→Work）正向/反向是两个选项。
const optionKey = (o: TypeOption) => `${o.code}|${o.forward ? "f" : "r"}`;

/**
 * RelationEditorField：通用编辑器内的关系维护区（catalog 8-kind 维度）。
 *
 * 增删改及排序只保存到服务器工作副本，公开事实在 push 或 PR 合并后更新。
 * 词表来自服务端 definitions：本实体可作 source 的类型按正向展示，只能作
 * target 的按反向展示；两端都可时正反均为选项（提交时按方向放置端点）。
 * 关系可携带的属性字段由关系定义的 fields 声明，创建与编辑共用同一套动态表单；
 * 同一副本中的并发写入受工作副本版本控制。
 */
export function RelationEditorField({ workspaceId, workspaceVersion, onWorkspaceChange, entityId, entityKind, drafts, onDraftsChange }: Props) {
  const { t, locale } = useI18n();
  const { definitions: defs } = useDefinitions();
  const { user } = useAuth();
  const workspace = React.useRef<Workspace | null>(null);
  const scopeRequests = React.useRef(new AbortController());
  const loadRequests = React.useRef<AbortController | null>(null);
  const editingWorkspace = React.useRef<Workspace | null>(null);
  const removeWorkspace = React.useRef<Workspace | null>(null);
  const verifyingCreate = React.useRef(false);
  const loadGeneration = React.useRef(0);
  const workspaceChange = React.useRef(onWorkspaceChange);
  workspaceChange.current = onWorkspaceChange;
  const [saved, setSaved] = useState(false);
  const [uncertainCreate, setUncertainCreate] = useState(false);
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

  // 首次保存前的关系随新实体原子保存到同一副本。
  const pendingMode = !entityId && !!onDraftsChange && canWrite;
  const queue = drafts || [];

  // 待提交关系的对端标题：只对这些 id 批量取一次，避免列表里只剩裸 UUID。
  useEffect(() => {
    const ids = queue.map((d) => d.targetId).filter((id) => id && !draftTitles[id] && !peers[id]);
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
  }, [queue, draftTitles, peers]);

  const load = React.useCallback(async () => {
    if (!workspaceId) return;
    const generation = ++loadGeneration.current;
    loadRequests.current?.abort();
    const controller = new AbortController();
    loadRequests.current = controller;
    const signal = AbortSignal.any([scopeRequests.current.signal, controller.signal]);
    setLoading(true);
    setError("");
    try {
      let current = await getWorkspace(workspaceId, signal);
      if (!entityId) {
        if (loadGeneration.current !== generation || signal.aborted) return;
        workspace.current = current;
        setPeers(Object.fromEntries(current.objects.filter((object) => object.target === "entity" && object.document)
          .map((object) => [object.id, { ...object.document, id: object.id } as Entity])));
        workspaceChange.current?.(current);
        return;
      }
      const ownEntity = current.objects.find((object) => object.target === "entity" && object.id === entityId);
      const res = ownEntity && ownEntity.base === null
        ? { items: [], entities: {} }
        : await fetchEntityRelationships(entityId);
      const mainRelations = editableRelations(res.items);
      const checkedOut = new Set(current.objects.filter((object) => object.target === "relation").map((object) => object.id));
      const missing = mainRelations.filter((relation) => !checkedOut.has(relation.id)).map((relation) => relation.id);
      if (missing.length > 0 && canWrite) {
        if (loadGeneration.current !== generation || signal.aborted) return;
        current = await checkoutRelationObjects(current, missing, signal);
        if (loadGeneration.current !== generation || signal.aborted) return;
      }
      const projected = relationWorkspaceItems(current, entityId, mainRelations);
      const nextPeers: Record<string, Entity> = { ...res.entities };
      for (const object of current.objects) {
        if (object.target === "entity" && object.document) nextPeers[object.id] = { ...object.document, id: object.id } as Entity;
      }
      if (loadGeneration.current !== generation) return;
      workspace.current = current;
      workspaceChange.current?.(current);
      setItems(projected);
      const peerIds = [...new Set(projected.map((relation) => relation.source_id === entityId ? relation.target_id : relation.source_id))];
      const extraPeers = await Promise.all(peerIds.filter((id) => !nextPeers[id]).map((id) => api<Entity>(`/catalog/entities/${id}`).catch(() => null)));
      if (loadGeneration.current !== generation) return;
      for (const peer of extraPeers) if (peer?.id) nextPeers[peer.id] = peer;
      setPeers(nextPeers);
      if (verifyingCreate.current) {
        verifyingCreate.current = false;
        setUncertainCreate(false);
      }
    } catch (e) {
      if (loadGeneration.current === generation) setError(t("editor.relation.loadFailed"));
    } finally {
      if (loadGeneration.current === generation) setLoading(false);
    }
  }, [entityId, workspaceId, workspaceVersion, canWrite, t]);

  const stage = async (operations: WorkspaceOperation[], snapshot = workspace.current) => {
    if (!snapshot || snapshot.id !== workspaceId) throw new Error(t("collaboration.editor.reload"));
    const signal = scopeRequests.current.signal;
    let result;
    try {
      result = await stageEditingWorkspace(snapshot, operations, signal);
    } catch (reason) {
      if (signal.aborted) return null;
      throw reason;
    }
    if (signal.aborted) return null;
    loadRequests.current?.abort();
    loadGeneration.current += 1;
    workspace.current = result.workspace;
    workspaceChange.current?.(result.workspace);
    setSaved(true);
    return result;
  };

  useEffect(() => {
    const controller = new AbortController();
    scopeRequests.current = controller;
    workspace.current = null;
    editingWorkspace.current = null;
    removeWorkspace.current = null;
    verifyingCreate.current = false;
    setItems([]);
    setPeers({});
    setSaved(false);
    setUncertainCreate(false);
    setBusy(false);
    setEditingId("");
    setPendingRemove(null);
    setEditAttrs({});
    return () => { controller.abort(); loadGeneration.current += 1; };
  }, [entityId, workspaceId, user?.id]);

  useEffect(() => {
    void load();
    return () => { loadRequests.current?.abort(); loadGeneration.current += 1; };
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
  // 关系定义声明的可携带属性字段（definitions.fields 引用），无声明则不出现表单。
  const relationFieldCodes = (code: string): string[] => defs?.relations?.[code]?.fields || [];

  const missingRequired = (code: string, attrs: Record<string, any>): string[] =>
    relationFieldCodes(code).filter((fc) => {
      const f = defs?.fields?.[fc];
      const v = attrs?.[fc];
      return !!f?.required && (v === undefined || v === null || v === "");
    });

  const submit = async () => {
    if (uncertainCreate || !selected || !addTarget) return;
    // 新实体还没有副本 ID，先与实体表单一起保存。
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
      const relation = {
        type: selected.code,
        source_id: selected.forward ? entityId : addTarget,
        target_id: selected.forward ? addTarget : entityId,
        position: nextPosition,
        attributes: addAttrs,
      };
      if (!await stage([{target:"relation",action:"create",ref:"relation",document:relation}])) return;
      setAddType("");
      setAddTarget("");
      setAddAttrs({});
      await load();
    } catch (e) {
      if ((e as Error).message === "workspace_write_uncertain") {
        setUncertainCreate(true);
        setSaved(false);
        setError("");
      } else setError(friendly((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  // move：与相邻的同类型同向关系交换 position，用于调整署名/展示次序。
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
      if (!await stage([{target:"relation",action:"update",id:r.id,patch:[{path:"/position",value:b===a?a+delta:b}]},
        {target:"relation",action:"update",id:other.id,patch:[{path:"/position",value:a}]}])) return;
      await load();
    } catch (e) {
      setError(friendly((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  const startEdit = (r: Relation) => {
    editingWorkspace.current = workspace.current;
    setEditingId(r.id);
    setEditAttrs({ ...(r.attributes || {}) });
    setError("");
  };

  const saveEdit = async (r: Relation) => {
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
      if (!await stage([{target:"relation",action:"update",id:r.id,patch:[{path:"/attributes",value:editAttrs}]}], editingWorkspace.current)) return;
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
    setBusy(true);
    setError("");
    try {
      if (!await stage([{target:"relation",action:"delete",id:rel.id}], removeWorkspace.current)) return;
      await load();
    } catch (e) {
      setError(friendly((e as Error).message));
    } finally {
      setBusy(false);
    }
  };

  const friendly = (msg: string) => msg === "forbidden" ? t("editor.relation.forbidden")
    : msg === "workspace_conflict" ? t("collaboration.editor.conflict") : msg;

  const peerLabel = (id: string) => {
    const p = peers[id];
    if (!p) return id.slice(0, 8);
    return p.title || id.slice(0, 8);
  };

  const peerHref = (id: string) => {
    const object = workspace.current?.objects.find((item) => item.target === "entity" && item.id === id);
    const query = new URLSearchParams({ workspace: workspaceId });
    if (object?.base === null && object.document) {
      query.set("entity", id);
      if (typeof object.document.kind === "string") query.set("kind", object.document.kind);
      return `/new?${query}`;
    }
    return `/catalog/${id}?${query}`;
  };

  const reload = () => {
    if (uncertainCreate) {
      verifyingCreate.current = true;
      setAddType("");
      setAddTarget("");
      setAddAttrs({});
    }
    void load();
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

  // 无实体 ID 时由实体表单提供待保存队列。
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

      {saved && <p className="text-sm text-text-muted">{t("collaboration.editor.saved")} <Link className="text-primary underline" href={`/workspaces/${workspaceId}`}>{t("collaboration.editor.openWorkspace")}</Link></p>}
      {(error || uncertainCreate) && (
        <div className="space-y-2">
          <p role="alert" className="cv-error inline-flex items-center gap-1.5 text-sm text-red-600 dark:text-danger"><AlertCircle className="w-4 h-4 shrink-0" />{uncertainCreate ? t("collaboration.editor.uncertainSave") : error}</p>
          <button type="button" disabled={busy || loading} onClick={reload}>{t("collaboration.editor.reload")}</button>
        </div>
      )}

      {/* 新条目的关系随实体保存到工作副本。 */}
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
                    <span className="truncate text-text-strong">{draftTitles[d.targetId] || peers[d.targetId]?.title || d.targetId}</span>
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
                    <Link href={peerHref(peerId)} className="truncate text-primary hover:underline">
                      {peerLabel(peerId)}
                    </Link>
                    {attrSummary(r)}
                  </span>
                  <span className="flex shrink-0 items-center gap-2">
                    {/* 署名次序：与相邻同类型同向关系交换 position。无写权限时整组隐藏。 */}
                    {canWrite && (<>
                    <button
                      type="button"
                      disabled={busy || loading}
                      onClick={() => move(r, -1)}
                      className="inline-flex items-center opacity-60 hover:opacity-100 hover:text-primary disabled:opacity-30"
                      title={t("editor.relation.moveUp")}
                      aria-label={t("editor.relation.moveUp")}
                    >
                      <ChevronUp className="w-3.5 h-3.5" />
                    </button>
                    <button
                      type="button"
                      disabled={busy || loading}
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
                        disabled={busy || loading || editingId === r.id}
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
                      disabled={busy || loading}
                      onClick={() => { removeWorkspace.current = workspace.current; setPendingRemove(r); }}
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
                        disabled={busy || loading}
                        onClick={() => saveEdit(r)}
                        className="inline-flex items-center gap-1.5 whitespace-nowrap"
                      >
                        {t("common.save")}
                      </button>
                      <button
                        type="button"
                        disabled={busy || loading}
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
            disabled={busy || loading || uncertainCreate || !addType || !addTarget || (!!entityId && !workspace.current)}
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
