"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import Link from "next/link";
import { ListTree } from "lucide-react";
import { Entity, title as entityTitle } from "@/components/catalog/api";
import { fetchAllPages } from "@/components/catalog/api";
import { useI18n } from "@/i18n/I18nProvider";
import { getKindName, getRelationName, getTermName, useDefinitions } from "@/lib/definitions";
import { fetchEntityRelationships } from "@/lib/catalogRelationships";
import { useAuth } from "@/lib/authContext";
// 日期值的呈现与信息面板共用同一实现（本地化/图例口径一致，不另写格式化）。
import { FieldValue } from "@/components/catalog/TemplateAttributeSections";

type WorkContentDirectoryProps = {
  workId: string;
  data: WorkDirectoryData;
  /** 模板声明的目录形态（definitions.templates[*].directory）：tree 保留层级缩进，
   *  list 拍平为单层编号列表。两者共用同一份数据，只改变呈现，不改变内容。 */
  directory?: string;
};

// 目录条目视图：结构子项与定义为聚合的关系各自成区块，不互斥。
//   ① 篇目树（content_unit）——小说章节、动画分集；
//   ② 表达（expression）——无卷章树的作品（如 OST 的各个录音）；
//   ③ 聚合关系的正向组成项；④ 聚合关系的反向所属项。
// 情形 ①②③④ 可以同时存在：专辑既有自身表达，又由独立歌曲构成时，
// 旧实现只显示其中一个，把其余内容全部隐藏。entry_role 是篇目类型，组成作品用 kind 标签区分。
type DirectoryEntry = {
  id: string;
  parentId: string;
  position: number;
  number: string;
  entryRole: string;
  /** 条目对应的实体 kind：组成作品列为 "work"，其余为篇目/表达本身。 */
  kind: string;
  title: string;
  /** 篇目放送日（definitions 声明的 air_date）：原样呈现 ISO 日期，与页头日期徽章口径一致。 */
  airDate: string;
  relationCode?: string;
};

function toEntry(e: Entity, locale: string): DirectoryEntry {
  // 表达（expression）通过 content_unit_id 挂载到所属章节，章节（content_unit）通过 parent_id 支持树形层级
  const parentId = e.kind === "expression"
    ? (e.content_unit_id || e.parent_id || "")
    : (e.parent_id || "");
  return {
    id: e.id || "",
    parentId,
    position: e.position || 0,
    number: e.number || "",
    entryRole: String(e.attributes?.entry_role || ""),
    kind: e.kind || "",
    title: entityTitle(e, locale) || e.title || e.id || "",
    // 放送日是篇目自带的标量属性，与发行日期同样按 ISO 字符串直出（站上既有口径），
    // 不做本地化格式化：日期在不同语言下含义相同，避免多一套格式化实现。
    airDate: String(e.attributes?.air_date || "").trim(),
  };
}

// componentEntries 从定义声明的聚合关系里取关联实体，并按方向区分语义：
//   self 是包含方（source_id） → 对端是"组成内容"；
//   self 是被包含方（target_id） → 对端是"所属集合/作品"。
export type RelationRow = {
  type: string;
  source_id: string;
  target_id: string;
  position?: number;
};

export type WorkDirectoryData = {
  status: "loading" | "ready" | "error";
  items: Entity[];
  relations: RelationRow[];
  relatedEntities: Record<string, Entity>;
  retry: () => void;
};

/** 作品页与目录面板共用一次读取，标签可见性和实际内容不会各查各的。 */
export function useWorkDirectoryData(workId: string): WorkDirectoryData {
  const { user } = useAuth();
  const viewerId = user?.id || "";
  const readKey = JSON.stringify([workId, viewerId]);
  const currentReadKey = useRef(readKey);
  currentReadKey.current = readKey;
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<Omit<WorkDirectoryData, "retry"> & { readKey: string }>({
    readKey: "", status: "loading", items: [], relations: [], relatedEntities: {},
  });

  useEffect(() => {
    if (!workId) {
      setState({ readKey, status: "ready", items: [], relations: [], relatedEntities: {} });
      return;
    }
    let active = true;
    const current = () => active && currentReadKey.current === readKey;
    setState({ readKey, status: "loading", items: [], relations: [], relatedEntities: {} });
    (async () => {
      try {
        const [units, exprs, rel] = await Promise.all([
          fetchAllPages<Entity>(`/catalog/entities?kind=content_unit&work_id=${encodeURIComponent(workId)}`),
          fetchAllPages<Entity>(`/catalog/entities?kind=expression&work_id=${encodeURIComponent(workId)}`),
          fetchEntityRelationships(workId),
        ]);
        if (current()) setState({
          readKey, status: "ready", items: [...units, ...exprs],
          relations: Array.isArray(rel.items) ? rel.items : [],
          relatedEntities: rel.entities && typeof rel.entities === "object" ? rel.entities : {},
        });
      } catch {
        if (current()) setState({ readKey, status: "error", items: [], relations: [], relatedEntities: {} });
      }
    })();
    return () => { active = false; };
  }, [workId, viewerId, attempt]);

  const retry = () => setAttempt((n) => n + 1);
  return state.readKey === readKey
    ? { ...state, retry }
    : { status: workId ? "loading" : "ready", items: [], relations: [], relatedEntities: {}, retry };
}

export function hasWorkDirectoryContent(
  data: WorkDirectoryData,
  selfId: string,
  isAggregate: (code: string) => boolean,
): boolean {
  if (data.items.length > 0) return true;
  return data.relations.some((r) => {
    if (!isAggregate(r.type)) return false;
    if (r.source_id !== selfId && r.target_id !== selfId) return false;
    const peerId = r.source_id === selfId ? r.target_id : r.source_id;
    const peer = data.relatedEntities[peerId];
    return Boolean(peer?.id);
  });
}

export function componentEntries(
  relations: RelationRow[],
  entities: Record<string, Entity>,
  selfId: string,
  locale: string,
  /** 由调用方按关系定义判定"这条关系是否表达组成/聚合"，避免写死关系码。 */
  isAggregate: (code: string) => boolean = () => false,
): { includes: DirectoryEntry[]; includedIn: DirectoryEntry[] } {
  const includes: DirectoryEntry[] = [];
  const includedIn: DirectoryEntry[] = [];
  const seen = new Set<string>();
  for (const r of relations) {
    if (!isAggregate(r.type)) continue;
    // Unified links also return edges that merely reference this entity in an
    // attribute; those are not its membership edges.
    if (r.source_id !== selfId && r.target_id !== selfId) continue;
    const outgoing = r.source_id === selfId;
    const peerId = outgoing ? r.target_id : r.source_id;
    // 同一对实体可以有不同聚合语义；仅去除同一关系码、方向、对端的重复行。
    const key = `${r.type}:${outgoing ? "out" : "in"}:${peerId}`;
    if (!peerId || peerId === selfId || seen.has(key)) continue;
    const peer = entities[peerId];
    if (!peer?.id) continue;
    seen.add(key);
    const entry: DirectoryEntry = {
      id: peerId,
      parentId: "",
      position: r.position || 0,
      number: "",
      entryRole: "",
      kind: peer.kind,
      title: entityTitle(peer, locale) || peer.title || peerId,
      airDate: "",
      relationCode: r.type,
    };
    (outgoing ? includes : includedIn).push(entry);
  }
  const byOrder = (a: DirectoryEntry, b: DirectoryEntry) =>
    a.position !== b.position ? a.position - b.position : a.title.localeCompare(b.title);
  includes.sort(byOrder);
  includedIn.sort(byOrder);
  return { includes, includedIn };
}

export function WorkContentDirectory({ workId, data, directory = "tree" }: WorkContentDirectoryProps) {
  const { t, tr, locale } = useI18n();
  const { definitions: defs, kinds } = useDefinitions();
  // 条目角标的层级名：服务端 definitions.kinds 优先，字典只作兜底（缺键退原始码）。
  const kindLabel = (code: string) =>
    getKindName(kinds, code, locale, tr(`catalog.kind.${code}`, code));
  const items = useMemo(() => data.items.map((e) => toEntry(e, locale)), [data.items, locale]);
  const loading = data.status === "loading";

  // definitions 可能晚于目录数据返回，也可能在后台发布后更新；只重算关系语义，
  // 不因此重复请求整棵章节树与表达列表。
  const { includes: components, includedIn } = useMemo(
    () => componentEntries(
      data.relations,
      data.relatedEntities,
      workId,
      locale,
      (code) => defs?.relations?.[code]?.aggregate === true,
    ),
    [data.relations, data.relatedEntities, workId, locale, defs],
  );

  const children = useMemo(() => {
    const grouped = new Map<string, DirectoryEntry[]>();
    const visibleIds = new Set(items.map((item) => item.id));
    for (const item of items) {
      // A draft parent may be hidden while a child is visible. Keep the
      // visible child reachable instead of showing a tab with no rows.
      const key = item.parentId && visibleIds.has(item.parentId) ? item.parentId : "root";
      const list = grouped.get(key) || [];
      list.push(item);
      grouped.set(key, list);
    }
    // 服务端列表默认按 updated_at 倒序，不排序会让新建/修改章节改变目录次序。
    // 在每个父节点内按结构位置稳定排序，编号仅作同位次时的次序兜底。
    for (const list of Array.from(grouped.values())) {
      list.sort((a, b) => {
        if (a.position !== b.position) return a.position - b.position;
        return a.number.localeCompare(b.number, undefined, { numeric: true });
      });
    }
    return grouped;
  }, [items]);

  // 篇目用途名称以 definitions 的 entry_role 词表为准（后台新增用途即刻显示）；
  // 词表未声明该用途时回退内置文案，仍缺失则显示原始码。
  const roleLabel = (role: string): string => {
    const name = getTermName(defs, "entry_role", role, locale);
    return name !== role ? name : tr(`catalog.contents.role.${role}`, role);
  };

  const renderEntries = (parentKey: string, depth: number): ReactNode[] => {
    return (children.get(parentKey) || []).flatMap((entry) => {
      const role = entry.entryRole || "main";
      // directory=list 时拍平为单层（仍保留原次序并完整展开子章节），tree 时按父子层级缩进。
      const indent = directory === "list" ? 0 : depth * 22;
      const isCollectionOrWork = entry.kind === "work" || entry.kind === "collection";
      return [
        <div
          key={entry.id}
          className="flex items-center gap-3 px-3.5 py-2.5 border-b border-line-subtle last:border-b-0"
          style={{ paddingLeft: `${14 + indent}px` }}
        >
          <span className="w-10 shrink-0 text-right font-mono text-xs text-text-muted">
            {entry.number || entry.position || "—"}
          </span>
          <Link href={`/catalog/${entry.id}`} className="min-w-0 flex-1 truncate text-sm text-text-strong hover:text-primary">
            {entry.title}
          </Link>
          {/* 放送日：有值才显示，缺值不留空占位。呈现走站上共用的 FieldValue，
              日期类型由 definitions 判定（不在这里自造格式化），与信息面板同一口径。 */}
          {entry.airDate && (
            <span className="shrink-0 text-[11px] text-text-faint">
              <FieldValue code="air_date" value={entry.airDate} defs={defs} locale={locale} />
            </span>
          )}
          <span className="shrink-0 rounded-sm border border-line px-1.5 py-0.5 font-mono text-[10px] text-text-faint">
            {isCollectionOrWork || entry.kind === "expression" ? kindLabel(entry.kind) : roleLabel(role)}
          </span>
        </div>,
        ...renderEntries(entry.id, depth + 1),
      ];
    });
  };

  // 关系码决定分组名称：后台改正反向名后目录立即跟随；不同语义不混为一组。
  const blocks: { key: string; title: string; entries: DirectoryEntry[] }[] = [];
  if (items.length > 0) blocks.push({ key: "items", title: t("work.contents.title"), entries: items });
  for (const [direction, entries] of [["out", components], ["in", includedIn]] as const) {
    const groups = new Map<string, DirectoryEntry[]>();
    for (const entry of entries) {
      const code = entry.relationCode || "";
      groups.set(code, [...(groups.get(code) || []), entry]);
    }
    for (const [code, group] of Array.from(groups)) {
      blocks.push({
        key: `${direction}:${code}`,
        title: getRelationName(defs, code, direction === "out", locale),
        entries: group,
      });
    }
  }

  const renderSimple = (entries: DirectoryEntry[]) =>
    entries.map((entry) => (
      <div
        key={entry.id}
        className="flex items-center gap-3 px-3.5 py-2.5 border-b border-line-subtle last:border-b-0"
      >
        <span className="w-10 shrink-0 text-right font-mono text-xs text-text-muted">{entry.number || entry.position || "—"}</span>
        <Link href={`/catalog/${entry.id}`} className="min-w-0 flex-1 truncate text-sm text-text-strong hover:text-primary">
          {entry.title}
        </Link>
        <span className="shrink-0 rounded-sm border border-line px-1.5 py-0.5 font-mono text-[10px] text-text-faint">
          {kindLabel(entry.kind)}
        </span>
      </div>
    ));

  if (data.status === "error") return (
    <div role="alert" className="rounded-lg border border-line p-6 text-center text-sm space-y-2">
      <p>{t("catalog.listFailed")}</p>
      <button type="button" onClick={data.retry} className="text-primary hover:underline">{t("catalog.retry")}</button>
    </div>
  );
  if (loading) return <div className="p-6 text-center text-sm text-text-faint">{t("work.contents.loading")}</div>;
  if (blocks.length === 0) return null;

  return (
    <div className="space-y-3">
      {blocks.map((block) => (
        <section
          key={block.key}
          className="rounded-lg border border-line bg-surface/80 backdrop-blur-md shadow-soft overflow-hidden"
        >
          <div className="px-3.5 sm:px-4 py-3 border-b border-line-subtle flex items-center gap-2">
            <span className="w-9 h-9 grid place-items-center rounded-md bg-primary/10 border border-primary/20">
              <ListTree className="w-4 h-4 text-primary" strokeWidth={1.5} />
            </span>
            <h2 className="font-display text-base font-bold tracking-tight text-text-strong">
              {block.title}
            </h2>
            {!loading && (
              <span className="font-mono text-sm text-text-faint">{t("work.contents.count", { count: block.entries.length })}</span>
            )}
          </div>
          {loading ? (
            <div className="p-6 text-center font-mono text-sm text-text-faint">{t("work.contents.loading")}</div>
          ) : block.key === "items" ? (
            <div>{renderEntries("root", 0)}</div>
          ) : (
            <div>{renderSimple(block.entries)}</div>
          )}
        </section>
      ))}
    </div>
  );
}
