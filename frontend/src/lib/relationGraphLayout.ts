import type { GraphLink, GraphNode } from "./api/catalog";

export type GraphLayoutMode = "hierarchy" | "radial" | "force";

export interface PositionedGraphNode extends GraphNode {
  x: number;
  y: number;
  width: number;
  height: number;
  layer: number;
}

export const GRAPH_CARD_WIDTH = 206;
export const GRAPH_CARD_HEIGHT = 56;
export const GRAPH_CENTER_WIDTH = 220;
export const GRAPH_CENTER_HEIGHT = 60;
export const GRAPH_HUB_WIDTH = 186;
export const GRAPH_HUB_HEIGHT = 46;

const SKELETON_TIER: Record<string, number> = {
  collection: 1,
  work: 2,
  content_unit: 3,
  release: 4,
  medium: 5,
  track: 6,
  expression: 7,
};

export function isHubNodeId(id: string): boolean {
  return id.startsWith("__hub:");
}

export function hubKeyFromNodeId(id: string): string {
  return isHubNodeId(id) ? id.slice("__hub:".length) : "";
}

export function nodeBoxSize(id: string, centerEntityId: string): { width: number; height: number } {
  if (id === centerEntityId) return { width: GRAPH_CENTER_WIDTH, height: GRAPH_CENTER_HEIGHT };
  if (isHubNodeId(id)) return { width: GRAPH_HUB_WIDTH, height: GRAPH_HUB_HEIGHT };
  return { width: GRAPH_CARD_WIDTH, height: GRAPH_CARD_HEIGHT };
}

/** Compute shortest hop distance and parent link from centerEntityId across all candidate links. */
export function computeHopTree(
  centerEntityId: string,
  links: readonly GraphLink[],
): { hopDist: Map<string, number>; parentEdge: Map<string, GraphLink>; parentNode: Map<string, string> } {
  const adj = new Map<string, { peer: string; link: GraphLink }[]>();
  for (const link of links) {
    if (!link.source || !link.target || link.source === link.target) continue;
    let sList = adj.get(link.source);
    if (!sList) { sList = []; adj.set(link.source, sList); }
    sList.push({ peer: link.target, link });
    let tList = adj.get(link.target);
    if (!tList) { tList = []; adj.set(link.target, tList); }
    tList.push({ peer: link.source, link });
  }
  // Prefer structural/hierarchical links first when building shortest paths.
  for (const list of adj.values()) {
    list.sort((a, b) => Number(Boolean(b.link.is_hierarchical)) - Number(Boolean(a.link.is_hierarchical)));
  }
  const hopDist = new Map<string, number>([[centerEntityId, 0]]);
  const parentEdge = new Map<string, GraphLink>();
  const parentNode = new Map<string, string>();
  const queue: string[] = [centerEntityId];
  for (let head = 0; head < queue.length; head++) {
    const curr = queue[head];
    const nextDist = (hopDist.get(curr) ?? 0) + 1;
    for (const { peer, link } of adj.get(curr) || []) {
      if (hopDist.has(peer)) continue;
      hopDist.set(peer, nextDist);
      parentEdge.set(peer, link);
      parentNode.set(peer, curr);
      queue.push(peer);
    }
  }
  return { hopDist, parentEdge, parentNode };
}

/** Keep ancestor bridge edges back to centerEntityId so filtered multi-hop nodes never float disconnected. */
export function withAncestorBridgeLinks(
  centerEntityId: string,
  allLinks: readonly GraphLink[],
  matchedLinks: readonly GraphLink[],
): GraphLink[] {
  if (matchedLinks.length === 0 || matchedLinks.length === allLinks.length) return [...matchedLinks];
  const { parentEdge, parentNode } = computeHopTree(centerEntityId, allLinks);
  const keep = new Set<GraphLink>(matchedLinks);
  for (const link of matchedLinks) {
    for (const start of [link.source, link.target]) {
      let cur: string | undefined = start;
      let guard = 0;
      while (cur && cur !== centerEntityId && guard++ < 16) {
        const pEdge = parentEdge.get(cur);
        if (pEdge) keep.add(pEdge);
        cur = parentNode.get(cur);
      }
    }
  }
  return allLinks.filter((l) => keep.has(l));
}

export interface DisplayGraphResult {
  canGroup: boolean;
  nodes: GraphNode[];
  links: GraphLink[];
}

/** Multi-level collapsible leaf grouping: folds large leaf fan-outs (>= 5 leaves) at any tree depth. */
export function buildDisplayGraph(
  centerEntityId: string,
  activeNodes: readonly GraphNode[],
  activeLinks: readonly GraphLink[],
  grouped: boolean,
  expandedHubs: ReadonlySet<string>,
  minGroupSize = 5,
): DisplayGraphResult {
  const byId = new Map(activeNodes.map((n) => [n.id, n]));
  const { hopDist } = computeHopTree(centerEntityId, activeLinks);
  const degree = new Map<string, number>();
  for (const l of activeLinks) {
    if (l.source === l.target) continue;
    degree.set(l.source, (degree.get(l.source) || 0) + 1);
    degree.set(l.target, (degree.get(l.target) || 0) + 1);
  }

  interface LeafBucket {
    key: string;
    parentId: string;
    type: string;
    label: string;
    color?: string;
    isHierarchical?: boolean;
    memberIds: string[];
    memberLinks: GraphLink[];
    sampleKind: string;
  }
  const buckets = new Map<string, LeafBucket>();
  for (const link of activeLinks) {
    if (!link.source || !link.target || link.source === link.target) continue;
    const ds = hopDist.get(link.source) ?? 99;
    const dt = hopDist.get(link.target) ?? 99;
    const parentId = ds <= dt ? link.source : link.target;
    const leafId = parentId === link.source ? link.target : link.source;
    if (leafId === centerEntityId) continue;
    if ((degree.get(leafId) || 0) !== 1) continue;
    const leafNode = byId.get(leafId);
    if (!leafNode) continue;
    const key = `${parentId}::${link.type}`;
    let b = buckets.get(key);
    if (!b) {
      b = {
        key,
        parentId,
        type: link.type,
        label: link.label || link.type,
        color: link.color,
        isHierarchical: link.is_hierarchical,
        memberIds: [],
        memberLinks: [],
        sampleKind: leafNode.type,
      };
      buckets.set(key, b);
    }
    if (!b.memberIds.includes(leafId)) {
      b.memberIds.push(leafId);
      b.memberLinks.push(link);
    }
  }

  const collapsible = [...buckets.values()].filter((b) => b.memberIds.length >= minGroupSize);
  const canGroup = collapsible.length > 0;
  if (!grouped || !canGroup) {
    return { canGroup, nodes: [...activeNodes], links: [...activeLinks] };
  }

  const hiddenLeafIds = new Set<string>();
  const foldedLinkSet = new Set<GraphLink>();
  const outNodes: GraphNode[] = [];
  const outLinks: GraphLink[] = [];

  for (const b of collapsible) {
    const hubId = `__hub:${b.key}`;
    const open = expandedHubs.has(b.key);
    for (const ml of b.memberLinks) foldedLinkSet.add(ml);
    if (!open) {
      for (const mid of b.memberIds) hiddenLeafIds.add(mid);
    }
    outNodes.push({
      id: hubId,
      name: b.label,
      type: "__hub",
      category: b.sampleKind,
      level: (hopDist.get(b.parentId) ?? 0) + 1,
      role: String(b.memberIds.length),
      order: byId.get(b.memberIds[0])?.order,
    });
    outLinks.push({
      id: `__hub-edge:${b.key}`,
      source: b.parentId,
      target: hubId,
      source_type: byId.get(b.parentId)?.type,
      target_type: b.sampleKind,
      type: b.type,
      label: b.label,
      qualifier: "__hub",
      color: b.color,
      is_hierarchical: b.isHierarchical,
    });
    if (open) {
      for (const mid of b.memberIds) {
        outLinks.push({
          id: `__hub-member:${b.key}:${mid}`,
          source: hubId,
          target: mid,
          source_type: "__hub",
          target_type: byId.get(mid)?.type,
          type: b.type,
          label: "",
          qualifier: "__hub-member",
          color: b.color,
          is_hierarchical: b.isHierarchical,
        });
      }
    }
  }

  const finalNodes: GraphNode[] = [];
  for (const n of activeNodes) {
    if (!hiddenLeafIds.has(n.id)) finalNodes.push(n);
  }
  finalNodes.push(...outNodes);

  const finalLinks: GraphLink[] = [];
  for (const l of activeLinks) {
    if (foldedLinkSet.has(l)) continue;
    if (hiddenLeafIds.has(l.source) || hiddenLeafIds.has(l.target)) continue;
    finalLinks.push(l);
  }
  finalLinks.push(...outLinks);
  return { canGroup, nodes: finalNodes, links: finalLinks };
}

function orientHierarchyEdge(
  link: GraphLink,
  byId: ReadonlyMap<string, GraphNode>,
  centerEntityType: string,
  hopDist: ReadonlyMap<string, number>,
): { upper: string; lower: string } {
  const sNode = byId.get(link.source);
  const tNode = byId.get(link.target);
  const sKind = sNode?.type || link.source_type || "";
  const tKind = tNode?.type || link.target_type || "";

  // Hub edges: if hub represents creators/agents on a non-agent center, place hub above parent; otherwise below.
  if (tKind === "__hub") {
    if (tNode?.category === "agent" && centerEntityType !== "agent" && sKind !== "agent") {
      return { upper: link.target, lower: link.source };
    }
    return { upper: link.source, lower: link.target };
  }
  if (sKind === "__hub") {
    if (link.qualifier === "__hub-member" && sNode?.category === "agent" && centerEntityType !== "agent") {
      return { upper: link.target, lower: link.source };
    }
    if (sNode?.category === "agent" && centerEntityType !== "agent" && tKind !== "agent") {
      return { upper: link.target, lower: link.source };
    }
    return { upper: link.source, lower: link.target };
  }

  // Agent <-> non-agent: creators/cast sit above works/releases unless center itself is an agent.
  if (sKind === "agent" && tKind !== "agent") {
    return { upper: link.source, lower: link.target };
  }
  if (tKind === "agent" && sKind !== "agent") {
    return centerEntityType === "agent"
      ? { upper: link.target, lower: link.source }
      : { upper: link.target, lower: link.source };
  }
  if (sKind === "agent" && tKind === "agent") {
    if (link.type === "member_of") {
      // Group/band above individual member.
      return { upper: link.target, lower: link.source };
    }
    const ds = hopDist.get(link.source) ?? 99;
    const dt = hopDist.get(link.target) ?? 99;
    if (ds !== dt) return ds < dt ? { upper: link.source, lower: link.target } : { upper: link.target, lower: link.source };
    return { upper: link.source, lower: link.target };
  }

  const sTier = SKELETON_TIER[sKind];
  const tTier = SKELETON_TIER[tKind];
  if (sTier !== undefined && tTier !== undefined && sTier !== tTier) {
    return sTier < tTier
      ? { upper: link.source, lower: link.target }
      : { upper: link.target, lower: link.source };
  }

  // Same-tier relationships: edition_of points release -> group, so group is upper.
  if (link.type === "edition_of" || link.type === "adaptation_of" || link.type === "sequel_of" || link.type === "spin_off_of" || link.type === "pressing_of" || link.type === "translation_of" || link.type === "cover_of") {
    return { upper: link.target, lower: link.source };
  }
  return { upper: link.source, lower: link.target };
}

export function computeGraphLayout(
  centerEntityId: string,
  centerEntityType: string,
  nodes: readonly GraphNode[],
  links: readonly GraphLink[],
  layoutMode: GraphLayoutMode,
): Map<string, PositionedGraphNode> {
  const map = new Map<string, PositionedGraphNode>();
  if (nodes.length === 0) return map;

  const byId = new Map<string, GraphNode>();
  for (const n of nodes) byId.set(n.id, n);
  if (!byId.has(centerEntityId)) {
    byId.set(centerEntityId, {
      id: centerEntityId,
      name: centerEntityId,
      type: centerEntityType,
      category: "center",
      level: 0,
    });
  }
  const allNodes = [...byId.values()];
  const otherNodes = allNodes.filter((n) => n.id !== centerEntityId);

  if (layoutMode === "radial") {
    const centerSize = nodeBoxSize(centerEntityId, centerEntityId);
    map.set(centerEntityId, { ...byId.get(centerEntityId)!, x: 0, y: 0, ...centerSize, layer: 0 });
    const { hopDist, parentNode } = computeHopTree(centerEntityId, links);
    const byRing = new Map<number, GraphNode[]>();
    for (const n of otherNodes) {
      const ring = Math.max(1, Math.min(4, hopDist.get(n.id) ?? 1));
      const list = byRing.get(ring) || [];
      list.push(n);
      byRing.set(ring, list);
    }
    const angleOf = new Map<string, number>();
    for (const ring of [...byRing.keys()].sort((a, b) => a - b)) {
      const list = byRing.get(ring)!;
      list.sort((a, b) => {
        const pa = angleOf.get(parentNode.get(a.id) || "") ?? 0;
        const pb = angleOf.get(parentNode.get(b.id) || "") ?? 0;
        if (pa !== pb) return pa - pb;
        return (a.order ?? 0) - (b.order ?? 0);
      });
      const minCircumference = list.length * 230;
      const radius = Math.max(ring * 240, minCircumference / (2 * Math.PI));
      list.forEach((n, idx) => {
        const angle = (idx / Math.max(1, list.length)) * 2 * Math.PI - Math.PI / 2;
        angleOf.set(n.id, angle);
        const sz = nodeBoxSize(n.id, centerEntityId);
        map.set(n.id, {
          ...n,
          x: +(radius * Math.cos(angle)).toFixed(1),
          y: +(radius * Math.sin(angle)).toFixed(1),
          ...sz,
          layer: ring,
        });
      });
    }
    return map;
  }

  if (layoutMode === "force") {
    const sim = allNodes.map((n, i) => {
      const sz = nodeBoxSize(n.id, centerEntityId);
      if (n.id === centerEntityId) return { ...n, x: 0, y: 0, ...sz, layer: 0 };
      const angle = (i / Math.max(1, otherNodes.length)) * 2 * Math.PI;
      const r = 240 + (i % 3) * 70;
      return { ...n, x: r * Math.cos(angle), y: r * Math.sin(angle), ...sz, layer: 1 };
    });
    const idxById = new Map(sim.map((n, i) => [n.id, i]));
    for (let iter = 0; iter < 80; iter++) {
      for (let i = 0; i < sim.length; i++) {
        for (let j = i + 1; j < sim.length; j++) {
          const a = sim[i];
          const b = sim[j];
          const dx = b.x - a.x;
          const dy = b.y - a.y;
          const dist = Math.hypot(dx, dy) || 1;
          const minDist = 245;
          if (dist < minDist) {
            const push = ((minDist - dist) / dist) * 0.38;
            const px = dx * push;
            const py = dy * push;
            if (a.id !== centerEntityId) { a.x -= px; a.y -= py; }
            if (b.id !== centerEntityId) { b.x += px; b.y += py; }
          }
        }
      }
      for (const link of links) {
        const si = idxById.get(link.source);
        const ti = idxById.get(link.target);
        if (si === undefined || ti === undefined || si === ti) continue;
        const s = sim[si];
        const t = sim[ti];
        const dx = t.x - s.x;
        const dy = t.y - s.y;
        const dist = Math.hypot(dx, dy) || 1;
        const pull = ((dist - 250) / dist) * 0.06;
        if (s.id !== centerEntityId) { s.x += dx * pull; s.y += dy * pull; }
        if (t.id !== centerEntityId) { t.x -= dx * pull; t.y -= dy * pull; }
      }
      for (const n of sim) {
        if (n.id === centerEntityId) continue;
        n.x *= 0.985;
        n.y *= 0.985;
      }
    }
    for (const n of sim) {
      map.set(n.id, { ...n, x: +n.x.toFixed(1), y: +n.y.toFixed(1) });
    }
    return map;
  }

  // === True Multi-Level Topological Hierarchy Layout ===
  const { hopDist } = computeHopTree(centerEntityId, links);
  const directedEdges: { upper: string; lower: string }[] = [];
  const downAdj = new Map<string, string[]>();
  const upAdj = new Map<string, string[]>();
  const seenDirected = new Set<string>();
  for (const link of links) {
    if (!link.source || !link.target || link.source === link.target) continue;
    if (!byId.has(link.source) || !byId.has(link.target)) continue;
    const { upper, lower } = orientHierarchyEdge(link, byId, centerEntityType, hopDist);
    const revKey = `${lower}->${upper}`;
    if (seenDirected.has(revKey)) continue;
    const key = `${upper}->${lower}`;
    if (seenDirected.has(key)) continue;
    seenDirected.add(key);
    directedEdges.push({ upper, lower });
    const dl = downAdj.get(upper) || [];
    dl.push(lower);
    downAdj.set(upper, dl);
    const ul = upAdj.get(lower) || [];
    ul.push(upper);
    upAdj.set(lower, ul);
  }

  // Assign initial relative layers anchored at centerEntityId = 0 via BFS + bounded longest-path.
  const rawLayer = new Map<string, number>([[centerEntityId, 0]]);
  const bfsOrder = [...allNodes].sort((a, b) => (hopDist.get(a.id) ?? 99) - (hopDist.get(b.id) ?? 99));
  for (let pass = 0; pass < 6; pass++) {
    let changed = false;
    for (const n of bfsOrder) {
      const curL = rawLayer.get(n.id);
      if (curL === undefined) continue;
      for (const child of downAdj.get(n.id) || []) {
        if (child === centerEntityId) continue;
        const dc = hopDist.get(child) ?? 99;
        const dn = hopDist.get(n.id) ?? 99;
        const prev = rawLayer.get(child);
        if (prev === undefined) {
          rawLayer.set(child, curL + 1);
          changed = true;
        } else if (dc > dn && prev < curL + 1 && curL + 1 <= 8) {
          rawLayer.set(child, curL + 1);
          changed = true;
        }
      }
      for (const parent of upAdj.get(n.id) || []) {
        if (parent === centerEntityId) continue;
        const dp = hopDist.get(parent) ?? 99;
        const dn = hopDist.get(n.id) ?? 99;
        const prev = rawLayer.get(parent);
        if (prev === undefined) {
          rawLayer.set(parent, curL - 1);
          changed = true;
        } else if (dp > dn && prev > curL - 1 && curL - 1 >= -8) {
          rawLayer.set(parent, curL - 1);
          changed = true;
        }
      }
    }
    if (!changed) break;
  }
  for (const n of allNodes) {
    if (!rawLayer.has(n.id)) rawLayer.set(n.id, 1);
  }

  // Compact non-empty layer numbers while keeping centerEntityId at 0.
  const distinctNeg = [...new Set([...rawLayer.values()].filter((v) => v < 0))].sort((a, b) => b - a);
  const distinctPos = [...new Set([...rawLayer.values()].filter((v) => v > 0))].sort((a, b) => a - b);
  const remap = new Map<number, number>([[0, 0]]);
  distinctNeg.forEach((v, i) => remap.set(v, -(i + 1)));
  distinctPos.forEach((v, i) => remap.set(v, i + 1));
  const layerOf = new Map<string, number>();
  for (const [id, l] of rawLayer) layerOf.set(id, remap.get(l) ?? l);

  const layers = new Map<number, string[]>();
  for (const n of allNodes) {
    const l = layerOf.get(n.id) ?? 0;
    const list = layers.get(l) || [];
    list.push(n.id);
    layers.set(l, list);
  }
  const sortedLayerNums = [...layers.keys()].sort((a, b) => a - b);

  const baseSort = (a: string, b: string) => {
    const na = byId.get(a)!;
    const nb = byId.get(b)!;
    if ((na.order ?? 0) !== (nb.order ?? 0)) return (na.order ?? 0) - (nb.order ?? 0);
    if (na.type !== nb.type) return na.type.localeCompare(nb.type);
    return na.name.localeCompare(nb.name);
  };
  for (const list of layers.values()) list.sort(baseSort);

  // Barycenter crossing minimization (4 alternating passes).
  const posIndex = new Map<string, number>();
  const syncPos = () => {
    for (const list of layers.values()) list.forEach((id, idx) => posIndex.set(id, idx));
  };
  syncPos();
  const sortOutward = (side: 1 | -1) => {
    const sideLayers = sortedLayerNums.filter((l) => (side > 0 ? l > 0 : l < 0));
    if (side < 0) sideLayers.sort((a, b) => b - a);
    for (const l of sideLayers) {
      const list = layers.get(l)!;
      const bary = new Map<string, number>();
      for (const id of list) {
        const closer = side > 0
          ? (upAdj.get(id) || []).filter((p) => (layerOf.get(p) ?? 0) < l)
          : (downAdj.get(id) || []).filter((c) => (layerOf.get(c) ?? 0) > l);
        if (closer.length > 0) {
          const avg = closer.reduce((s, nid) => s + (posIndex.get(nid) ?? 0), 0) / closer.length;
          bary.set(id, avg);
        } else {
          bary.set(id, posIndex.get(id) ?? 0);
        }
      }
      list.sort((a, b) => {
        const diff = (bary.get(a) ?? 0) - (bary.get(b) ?? 0);
        if (Math.abs(diff) > 1e-4) return diff;
        return baseSort(a, b);
      });
      list.forEach((id, idx) => posIndex.set(id, idx));
    }
  };
  sortOutward(1);
  sortOutward(-1);

  // Subtree-aware horizontal placement + automatic multi-row wrapping for wide leaf layers.
  const COL_STEP = 236;
  const LAYER_STEP = 134;
  const SUB_ROW_STEP = 80;
  const MAX_COLS_PER_GROUP = 5;

  // Primary parent in adjacent closer-to-center layer for grouping & subtree centering.
  const primaryParent = new Map<string, string>();
  const treeChildren = new Map<string, string[]>();
  for (const l of sortedLayerNums) {
    for (const id of layers.get(l)!) {
      if (l > 0) {
        const parents = (upAdj.get(id) || []).filter((p) => (layerOf.get(p) ?? 0) < l);
        parents.sort((a, b) => Math.abs((layerOf.get(a) ?? 0) - l) - Math.abs((layerOf.get(b) ?? 0) - l));
        if (parents[0]) {
          primaryParent.set(id, parents[0]);
          const ch = treeChildren.get(parents[0]) || [];
          ch.push(id);
          treeChildren.set(parents[0], ch);
        }
      } else if (l < 0) {
        const children = (downAdj.get(id) || []).filter((c) => (layerOf.get(c) ?? 0) > l);
        children.sort((a, b) => Math.abs((layerOf.get(a) ?? 0) - l) - Math.abs((layerOf.get(b) ?? 0) - l));
        if (children[0]) {
          primaryParent.set(id, children[0]);
          const ch = treeChildren.get(children[0]) || [];
          ch.push(id);
          treeChildren.set(children[0], ch);
        }
      }
    }
  }

  // Compute horizontal span (in columns) required by each subtree on positive and negative sides.
  const spanCols = new Map<string, number>();
  const computeSpan = (id: string, side: 1 | -1, visited = new Set<string>()): number => {
    if (visited.has(id)) return 1;
    visited.add(id);
    const kids = (treeChildren.get(id) || []).filter((k) => (
      side > 0 ? (layerOf.get(k) ?? 0) > (layerOf.get(id) ?? 0) : (layerOf.get(k) ?? 0) < (layerOf.get(id) ?? 0)
    ));
    if (kids.length === 0) {
      spanCols.set(`${side}:${id}`, 1);
      return 1;
    }
    // Leaf kids without grandchildren wrap at MAX_COLS_PER_GROUP columns!
    const leafKids = kids.filter((k) => (treeChildren.get(k) || []).length === 0);
    const branchKids = kids.filter((k) => (treeChildren.get(k) || []).length > 0);
    const leafCols = leafKids.length > 0 ? Math.min(MAX_COLS_PER_GROUP, leafKids.length) : 0;
    const branchCols = branchKids.reduce((s, k) => s + computeSpan(k, side, new Set(visited)), 0);
    const total = Math.max(1, leafCols + branchCols);
    spanCols.set(`${side}:${id}`, total);
    return total;
  };
  for (const id of layers.get(0) || [centerEntityId]) {
    computeSpan(id, 1);
    computeSpan(id, -1);
  }

  const xPos = new Map<string, number>();
  const subRowOffset = new Map<string, number>();
  const extraLayerRows = new Map<number, number>();

  // Place layer 0 centered at x = 0.
  const l0 = layers.get(0) || [centerEntityId];
  const l0Span = (l0.length - 1) * COL_STEP;
  l0.forEach((id, i) => {
    xPos.set(id, id === centerEntityId && l0.length === 1 ? 0 : -l0Span / 2 + i * COL_STEP);
    subRowOffset.set(id, 0);
  });

  // Place layers outward from 0 so children align directly beneath/above their parent.
  const placeSide = (side: 1 | -1) => {
    const sideLayers = sortedLayerNums.filter((l) => (side > 0 ? l > 0 : l < 0));
    if (side < 0) sideLayers.sort((a, b) => b - a); // -1, -2, -3...
    for (const l of sideLayers) {
      const list = layers.get(l)!;
      // Group nodes in this layer by primaryParent (preserving barycenter order).
      const groups: { parent: string; members: string[] }[] = [];
      const gMap = new Map<string, string[]>();
      for (const id of list) {
        const p = primaryParent.get(id) || "__none";
        let g = gMap.get(p);
        if (!g) {
          g = [];
          gMap.set(p, g);
          groups.push({ parent: p, members: g });
        }
        g.push(id);
      }
      groups.sort((a, b) => (xPos.get(a.parent) ?? 0) - (xPos.get(b.parent) ?? 0));

      // Assign desired center for each group = parent's x, with collision-free interval packing.
      let maxSubRowsInLayer = 1;
      const intervals: { center: number; width: number; place: (cx: number) => void }[] = [];
      for (const g of groups) {
        const desiredCenter = xPos.get(g.parent) ?? 0;
        const allLeaf = g.members.every((m) => (treeChildren.get(m) || []).length === 0);
        if (allLeaf && g.members.length > MAX_COLS_PER_GROUP) {
          const cols = MAX_COLS_PER_GROUP;
          const rows = Math.ceil(g.members.length / cols);
          maxSubRowsInLayer = Math.max(maxSubRowsInLayer, rows);
          const width = cols * COL_STEP;
          intervals.push({
            center: desiredCenter,
            width,
            place: (cx) => {
              g.members.forEach((m, idx) => {
                const r = Math.floor(idx / cols);
                const c = idx % cols;
                const rowCount = Math.min(cols, g.members.length - r * cols);
                const rowSpan = (rowCount - 1) * COL_STEP;
                xPos.set(m, cx - rowSpan / 2 + c * COL_STEP);
                subRowOffset.set(m, r);
              });
            },
          });
        } else {
          const memberSpans = g.members.map((m) => Math.max(1, spanCols.get(`${side}:${m}`) ?? 1) * COL_STEP);
          const totalWidth = memberSpans.reduce((s, w) => s + w, 0);
          intervals.push({
            center: desiredCenter,
            width: totalWidth,
            place: (cx) => {
              let cursor = cx - totalWidth / 2;
              g.members.forEach((m, idx) => {
                const w = memberSpans[idx];
                xPos.set(m, cursor + w / 2);
                subRowOffset.set(m, 0);
                cursor += w;
              });
            },
          });
        }
      }

      // Resolve horizontal overlaps between sibling groups while staying as close as possible to parent x.
      const centers = intervals.map((iv) => iv.center);
      for (let pass = 0; pass < 6; pass++) {
        for (let i = 1; i < intervals.length; i++) {
          const minGap = (intervals[i - 1].width + intervals[i].width) / 2;
          const overlap = minGap - (centers[i] - centers[i - 1]);
          if (overlap > 0) {
            centers[i - 1] -= overlap / 2;
            centers[i] += overlap / 2;
          }
        }
      }
      intervals.forEach((iv, i) => iv.place(centers[i]));
      extraLayerRows.set(l, maxSubRowsInLayer - 1);
    }
  };

  placeSide(1);
  placeSide(-1);

  // Compute cumulative vertical y for each layer (accounting for wrapped sub-rows).
  const layerBaseY = new Map<number, number>([[0, 0]]);
  let posCursorY = 0;
  for (const l of sortedLayerNums.filter((n) => n > 0)) {
    const prevExtra = extraLayerRows.get(l - 1) || 0;
    posCursorY += LAYER_STEP + prevExtra * SUB_ROW_STEP;
    layerBaseY.set(l, posCursorY);
  }
  let negCursorY = 0;
  for (const l of sortedLayerNums.filter((n) => n < 0).sort((a, b) => b - a)) {
    const curExtra = extraLayerRows.get(l) || 0;
    const prevExtra = l + 1 < 0 ? 0 : 0;
    negCursorY -= LAYER_STEP + (curExtra + prevExtra) * SUB_ROW_STEP;
    layerBaseY.set(l, negCursorY);
  }

  for (const n of allNodes) {
    const l = layerOf.get(n.id) ?? 0;
    const baseY = layerBaseY.get(l) ?? 0;
    const subRow = subRowOffset.get(n.id) ?? 0;
    const y = baseY + subRow * SUB_ROW_STEP;
    const x = xPos.get(n.id) ?? 0;
    const sz = nodeBoxSize(n.id, centerEntityId);
    map.set(n.id, {
      ...n,
      x: +x.toFixed(1),
      y: +y.toFixed(1),
      ...sz,
      layer: l,
    });
  }

  return map;
}
