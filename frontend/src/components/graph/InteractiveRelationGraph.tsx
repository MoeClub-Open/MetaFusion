"use client";

import React, { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "@/i18n/I18nProvider";
import { useTheme } from "@/lib/themeContext";
import { GraphNode, GraphLink, catalogEntityHref } from "@/lib/api";
import { useDefinitions, getKindName } from "@/lib/definitions";
import { RelationFilterBar, useRelationFilter } from "@/components/entity/RelationFilterBar";
import Link from "next/link";
import {
  ZoomIn,
  ZoomOut,
  Maximize2,
  Minimize2,
  Sparkles,
  ExternalLink,
  Info,
  Network,
  Layers,
  Compass,
  ArrowRight,
  ArrowLeft,
  Tag,
  Globe,
  Disc,
  User,
  Film,
  X,
  Scan,
} from "lucide-react";

export interface InteractiveRelationGraphProps {
  centerEntityId: string;
  centerEntityType: string;
  nodes: GraphNode[];
  links: GraphLink[];
  height?: number | string;
  className?: string;
  onNodeClick?: (node: GraphNode) => void;
  onEdgeClick?: (link: GraphLink) => void;
  /** 中心实体的 kind：层级布局据此把 collection/work 归上、release 归下、agent 归左。 */
  orientation?: "work" | "release" | "agent";
  showInspector?: boolean;
  title?: string;
  headerRightExtra?: React.ReactNode;
}

interface LayoutNode extends GraphNode {
  x: number;
  y: number;
  vx: number;
  vy: number;
  radius: number;
}

// 实体类型视觉主题配置与本地化辅助（固定八实体骨架，支持明暗双模式高保真渲染）
// 展示名由调用方解析（服务端 definitions.kinds 优先、前端字典兜底）：本函数只按 kind 码
// 决定配色与图标，名称不再从字典直取，避免后台改了骨架名前端不跟随。
const getEntityTypeTheme = (
  type: string,
  labelOf: (kind: string) => string,
  isDark = false,
) => {
  switch (type) {
    case "work":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#38bdf8" : "#0284c7",
        bgFill: isDark ? "#0c4a6e" : "#e0f2fe",
        textFill: isDark ? "#7dd3fc" : "#0369a1",
        stroke: isDark ? "#0284c7" : "#38bdf8",
        badgeBgClass: "bg-sky-500/15 text-sky-700 dark:text-info-soft border-sky-500/30",
        icon: Film,
      };
    case "agent":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#34d399" : "#059669",
        bgFill: isDark ? "#064e3b" : "#dcfce7",
        textFill: isDark ? "#86efac" : "#15803d",
        stroke: isDark ? "#059669" : "#34d399",
        badgeBgClass: "bg-emerald-500/15 text-emerald-700 dark:text-success-soft border-emerald-500/30",
        icon: User,
      };
    case "collection":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#818cf8" : "#4f46e5",
        bgFill: isDark ? "#312e81" : "#e0e7ff",
        textFill: isDark ? "#a5b4fc" : "#4338ca",
        stroke: isDark ? "#4f46e5" : "#818cf8",
        badgeBgClass: "bg-indigo-500/15 text-indigo-700 dark:text-alt-soft border-indigo-500/30",
        icon: Layers,
      };
    case "content_unit":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#2dd4bf" : "#0d9488",
        bgFill: isDark ? "#134e4a" : "#ccfbf1",
        textFill: isDark ? "#5eead4" : "#0f766e",
        stroke: isDark ? "#0d9488" : "#2dd4bf",
        badgeBgClass: "bg-teal-500/15 text-teal-700 dark:text-success-soft border-teal-500/30",
        icon: Sparkles,
      };
    case "expression":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#f472b6" : "#db2777",
        bgFill: isDark ? "#831843" : "#fce7f3",
        textFill: isDark ? "#f9a8d4" : "#be185d",
        stroke: isDark ? "#db2777" : "#f472b6",
        badgeBgClass: "bg-pink-500/15 text-pink-700 dark:text-danger-soft border-pink-500/30",
        icon: Sparkles,
      };
    case "release":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#fbbf24" : "#d97706",
        bgFill: isDark ? "#78350f" : "#ffedd5",
        textFill: isDark ? "#fed7aa" : "#c2410c",
        stroke: isDark ? "#d97706" : "#fbbf24",
        badgeBgClass: "bg-amber-500/15 text-amber-700 dark:text-warn-soft border-amber-500/30",
        icon: Disc,
      };
    case "medium":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#c084fc" : "#9333ea",
        bgFill: isDark ? "#581c87" : "#f3e8ff",
        textFill: isDark ? "#d8b4fe" : "#7e22ce",
        stroke: isDark ? "#9333ea" : "#c084fc",
        badgeBgClass: "bg-purple-500/15 text-purple-700 dark:text-alt-soft border-purple-500/30",
        icon: Disc,
      };
    case "track":
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#fb923c" : "#ea580c",
        bgFill: isDark ? "#7c2d12" : "#ffedd5",
        textFill: isDark ? "#fdba74" : "#c2410c",
        stroke: isDark ? "#ea580c" : "#fb923c",
        badgeBgClass: "bg-orange-500/15 text-orange-700 dark:text-warn-soft border-orange-500/30",
        icon: Tag,
      };
    default:
      return {
        label: labelOf(type),
        primaryColor: isDark ? "#94a3b8" : "#64748b",
        bgFill: isDark ? "#27272a" : "#f1f5f9",
        textFill: isDark ? "#e4e4e7" : "#475569",
        stroke: isDark ? "#52525b" : "#94a3b8",
        badgeBgClass: "bg-zinc-500/15 text-zinc-700 dark:text-zinc-300 border-zinc-500/30",
        icon: Tag,
      };
  }
};

const getLinkColorHex = (color?: string) => {
  switch (color) {
    case "emerald":
      return "#10b981";
    case "sky":
    case "blue":
      return "#0284c7";
    case "purple":
      return "#a855f7";
    case "indigo":
      return "#6366f1";
    case "amber":
      return "#f59e0b";
    case "rose":
    case "red":
      return "#f43f5e";
    case "teal":
      return "#14b8a6";
    default:
      return "#64748b";
  }
};

// 连线谓词徽标智能定位与包围盒避让算法
const calculateEdgeBadgePosition = (
  src: LayoutNode,
  tgt: LayoutNode,
  pillHalfWidth = 115
): { x: number; y: number } => {
  const dx = tgt.x - src.x;
  const dy = tgt.y - src.y;
  const dist = Math.hypot(dx, dy) || 1;

  const capsuleBottomOffset = 46;

  let tMin = (src.radius + 24) / dist;
  let tMax = 1 - (tgt.radius + 24) / dist;

  if (dy > 0) {
    const capHeightDist = src.radius + capsuleBottomOffset;
    const xOffsetAtCapBottom = Math.abs((dx / dy) * capHeightDist);
    if (xOffsetAtCapBottom < pillHalfWidth + 16) {
      const tClearSrcCap = (src.radius + capsuleBottomOffset + 14) / dy;
      tMin = Math.max(tMin, Math.min(0.48, tClearSrcCap));
    }
  }

  if (dy < 0) {
    const capHeightDist = tgt.radius + capsuleBottomOffset;
    const xOffsetAtCapBottom = Math.abs((dx / Math.abs(dy)) * capHeightDist);
    if (xOffsetAtCapBottom < pillHalfWidth + 16) {
      const tClearTgtCap = 1 - (tgt.radius + capsuleBottomOffset + 14) / Math.abs(dy);
      tMax = Math.min(tMax, Math.max(0.52, tClearTgtCap));
    }
  }

  const t = tMin < tMax ? (tMin + tMax) / 2 : 0.5;

  let posX = src.x + t * dx;
  let posY = src.y + t * dy;

  if (tMin >= tMax) {
    const normalX = -dy / dist;
    const normalY = dx / dist;
    posX += normalX * 28;
    posY += normalY * 28;
  }

  return { x: posX, y: posY };
};

export const InteractiveRelationGraph: React.FC<InteractiveRelationGraphProps> = ({
  centerEntityId,
  centerEntityType,
  nodes,
  links,
  height = 580,
  className = "",
  onNodeClick,
  onEdgeClick,
  orientation = "work",
  showInspector = true,
  title,
  headerRightExtra,
}) => {
  const { t, tr, locale } = useI18n();
  const { kinds } = useDefinitions();
  const { resolvedMode } = useTheme();
  // kind 展示名：服务端 definitions.kinds 优先，字典只作兜底，字典缺键时退原始码。
  const kindLabel = useCallback(
    (kind: string) => getKindName(kinds, kind, locale, tr(`catalog.kind.${kind}`, kind)),
    [kinds, locale, tr],
  );
  const isDark = resolvedMode === "dark";

  const containerRef = useRef<HTMLDivElement>(null);
  const canvasContainerRef = useRef<HTMLDivElement>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const fullscreenButtonRef = useRef<HTMLButtonElement>(null);

  // 拖拽平移手势追踪
  const dragRef = useRef<{
    isDown: boolean;
    startX: number;
    startY: number;
    startPanX: number;
    startPanY: number;
    hasMoved: boolean;
  }>({
    isDown: false,
    startX: 0,
    startY: 0,
    startPanX: 0,
    startPanY: 0,
    hasMoved: false,
  });

  const [zoom, setZoom] = useState(1);
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const [isDragging, setIsDragging] = useState(false);
  const [isFullscreen, setIsFullscreen] = useState(false);
  // 严格默认采用层级结构 (Hierarchy) 布局
  const [layoutMode, setLayoutMode] = useState<"hierarchy" | "radial" | "force">("hierarchy");
  const [selectedNode, setSelectedNode] = useState<GraphNode | null>(null);
  const [hoveredNodeId, setHoveredNodeId] = useState<string | null>(null);
  const [hoveredLinkId, setHoveredLinkId] = useState<string | null>(null);
  // 类型聚合 hub 的展开态（手风琴：一次只展开一个类型，默认全收起）。
  // 只在层级布局下生效； radial / force 保持原样平铺。
  const [expandedHub, setExpandedHub] = useState<string | null>(null);

  // 主题配色
  const capsuleBg = isDark ? "#18181b" : "#ffffff";
  const capsuleStroke = isDark ? "#27272a" : "#e4e4e7";
  const nodeNameFill = isDark ? "#f4f4f5" : "#09090b";
  const edgeBadgeBg = isDark ? "#27272a" : "#ffffff";
  const edgeBadgeStroke = isDark ? "#3f3f46" : "#d4d4d8";
  const edgeBadgeText = isDark ? "#e4e4e7" : "#3f3f46";
  const centerFill = isDark ? "#3b82f6" : "#2563eb";
  const centerBorder = isDark ? "#0b0f17" : "#ffffff";

  // 过滤连线：筛选维度（关联对象 / 关系分类 / 关系类型）全部由连线自身与 definitions
  // 推导——分组名与类型名都取服务端声明，新增关系或改分组后图谱即刻跟随，
  // 不在前端硬编码关系码或分组码。
  const facetRows = useMemo(
    () =>
      links.map((link, index) => ({
        type: link.type,
        // 对端 kind：中心实体一侧不算，另一侧才是"关联对象"。
        kind: (link.source === centerEntityId ? link.target_type : link.source_type) || "",
        label: link.label,
        index,
      })),
    [links, centerEntityId]
  );
  const relationFilter = useRelationFilter(facetRows);
  const filteredLinks = useMemo(
    () => relationFilter.visible.map((row) => links[row.index]),
    [relationFilter.visible, links]
  );

  // 获取激活的节点集合
  const activeNodeIds = useMemo(() => {
    const set = new Set<string>([centerEntityId]);
    filteredLinks.forEach((l) => {
      set.add(l.source);
      set.add(l.target);
    });
    return set;
  }, [centerEntityId, filteredLinks]);

  const activeNodes = useMemo(() => {
    return nodes.filter((n) => activeNodeIds.has(n.id));
  }, [nodes, activeNodeIds]);

  // 按关系类型分组（触及中心的边才参与聚合；游离边直通渲染，不进 hub）。
  // 成员按首次出现排，节点有多条边时归第一组——星形拓扑下基本是一对一。
  const typeGroups = useMemo(() => {
    const order: string[] = [];
    const members = new Map<string, string[]>();
    const labels = new Map<string, string>();
    for (const link of filteredLinks) {
      const other = link.source === centerEntityId ? link.target : link.source;
      if (other === centerEntityId) continue;
      if (link.source !== centerEntityId && link.target !== centerEntityId) continue;
      if (!members.has(link.type)) {
        order.push(link.type);
        members.set(link.type, []);
        labels.set(link.type, link.label || link.type);
      }
      const list = members.get(link.type)!;
      if (!list.includes(other)) list.push(other);
    }
    return order.map((type) => ({ type, label: labels.get(type) || type, memberIds: members.get(type) || [] }));
  }, [filteredLinks, centerEntityId]);

  // 单类型时聚合没有意义（一个 hub 包全部节点，多此一举）：直接平铺。
  const grouped = layoutMode === "hierarchy" && typeGroups.length > 1;
  const activeHub = expandedHub && typeGroups.some((g) => g.type === expandedHub) ? expandedHub : null;
  const hubIdOf = (type: string) => `__hub:${type}`;

  // 聚合展示集：中心 + 类型 hub + 展开组的成员。
  const displayNodes = useMemo<GraphNode[]>(() => {
    if (!grouped) return activeNodes;
    const byId = new Map(activeNodes.map((n) => [n.id, n]));
    const out: GraphNode[] = [];
    const center = byId.get(centerEntityId);
    if (center) out.push(center);
    for (const g of typeGroups) {
      out.push({
        id: hubIdOf(g.type),
        name: g.label,
        type: "__hub",
        category: "hub",
        level: 1,
        role: String(g.memberIds.length),
      });
      if (g.type === activeHub) {
        for (const id of g.memberIds) {
          const m = byId.get(id);
          if (m) out.push(m);
        }
      }
    }
    return out;
  }, [grouped, activeNodes, typeGroups, activeHub, centerEntityId]);

  // 聚合连边：中心→hub（收起态的唯一可见边）+ hub→成员（展开组）+
  // 两端都可见的游离边。原生的中心→成员边由 hub 边代替，不再绘制。
  const displayLinks = useMemo<GraphLink[]>(() => {
    if (!grouped) return filteredLinks;
    const visible = new Set(displayNodes.map((n) => n.id));
    const out: GraphLink[] = [];
    for (const g of typeGroups) {
      out.push({
        id: `__hub-edge:${g.type}`,
        source: centerEntityId,
        target: hubIdOf(g.type),
        type: g.type,
        label: g.label,
        qualifier: "__hub",
      });
      if (g.type === activeHub) {
        for (const id of g.memberIds) {
          if (!visible.has(id)) continue;
          out.push({
            id: `__hub-link:${g.type}:${id}`,
            source: hubIdOf(g.type),
            target: id,
            type: g.type,
            label: "",
            qualifier: "__hub-member",
          });
        }
      }
    }
    for (const link of filteredLinks) {
      if (link.source === centerEntityId || link.target === centerEntityId) continue;
      if (visible.has(link.source) && visible.has(link.target)) out.push(link);
    }
    return out;
  }, [grouped, filteredLinks, displayNodes, typeGroups, activeHub, centerEntityId]);

  // 计算节点布局坐标并执行全局几何中心对齐校准 (1000 x 680 基准坐标系)
  const layoutNodes = useMemo<Map<string, LayoutNode>>(() => {
    const map = new Map<string, LayoutNode>();
    const baseWidth = 1000;
    const baseHeight = 680;
    const centerX = baseWidth / 2;
    const centerY = baseHeight / 2;

    const otherNodes = activeNodes.filter((n) => n.id !== centerEntityId);
    const totalOthers = otherNodes.length;

    // 中心节点初始化
    const centerNode = activeNodes.find((n) => n.id === centerEntityId) || {
      id: centerEntityId,
      name: centerEntityId,
      type: centerEntityType,
      category: "center",
      level: 0,
    };

    map.set(centerEntityId, {
      ...centerNode,
      x: centerX,
      y: centerY,
      vx: 0,
      vy: 0,
      radius: 38,
    });

    if (layoutMode === "radial") {
      // 环形放射布局
      if (totalOthers <= 7) {
        const radius = Math.max(260, 190 + totalOthers * 16);
        otherNodes.forEach((node, i) => {
          const angle = (i / Math.max(1, totalOthers)) * 2 * Math.PI - Math.PI / 2;
          const x = centerX + radius * Math.cos(angle);
          const y = centerY + radius * Math.sin(angle);
          map.set(node.id, {
            ...node,
            x,
            y,
            vx: 0,
            vy: 0,
            radius: 30,
          });
        });
      } else {
        const innerCount = Math.min(6, Math.ceil(totalOthers / 2));
        const outerCount = totalOthers - innerCount;
        const rInner = 240;
        const rOuter = 390;

        for (let i = 0; i < innerCount; i++) {
          const node = otherNodes[i];
          const angle = (i / innerCount) * 2 * Math.PI - Math.PI / 2;
          const x = centerX + rInner * Math.cos(angle);
          const y = centerY + rInner * Math.sin(angle);
          map.set(node.id, {
            ...node,
            x,
            y,
            vx: 0,
            vy: 0,
            radius: 30,
          });
        }

        const offsetAngle = Math.PI / innerCount;
        for (let i = 0; i < outerCount; i++) {
          const node = otherNodes[innerCount + i];
          const angle = (i / outerCount) * 2 * Math.PI - Math.PI / 2 + offsetAngle;
          const x = centerX + rOuter * Math.cos(angle);
          const y = centerY + rOuter * Math.sin(angle);
          map.set(node.id, {
            ...node,
            x,
            y,
            vx: 0,
            vy: 0,
            radius: 30,
          });
        }
      }
    } else if (layoutMode === "hierarchy") {
      if (grouped) {
        // 类型聚合布局：中心 → 类型 hub → 成员（手风琴单开，默认全收起）。
        const hubById = new Map(displayNodes.map((n) => [n.id, n]));
        const hubList = typeGroups;
        const hubRadius = hubList.length <= 8 ? 300 : 350;
        hubList.forEach((g, i) => {
          const angle = (i / Math.max(1, hubList.length)) * 2 * Math.PI - Math.PI / 2;
          const hx = centerX + hubRadius * Math.cos(angle);
          const hy = centerY + hubRadius * Math.sin(angle);
          const hub = hubById.get(hubIdOf(g.type));
          if (hub) map.set(hub.id, { ...hub, x: hx, y: hy, vx: 0, vy: 0, radius: 34 });
          if (g.type === activeHub) {
            const side = hx >= centerX ? 1 : -1;
            const total = g.memberIds.length;
            g.memberIds.forEach((id, j) => {
              const m = hubById.get(id);
              if (!m) return;
              map.set(id, {
                ...m,
                x: hx + side * 180,
                y: hy + (j - (total - 1) / 2) * 78,
                vx: 0, vy: 0, radius: 26,
              });
            });
          }
        });
      } else {
      // 层级结构 (Hierarchy) 布局：精细化分类与充裕间距排布
      const topNodes: GraphNode[] = [];
      const bottomReleaseNodes: GraphNode[] = [];
      const bottomMediumNodes: GraphNode[] = [];
      const leftArtistNodes: GraphNode[] = [];
      const rightOtherNodes: GraphNode[] = [];

      otherNodes.forEach((node) => {
        if (node.id === centerEntityId) {
          rightOtherNodes.push(node);
        } else if (node.type === "agent") {
          leftArtistNodes.push(node);
        } else if (node.type === "medium" || node.type === "track") {
          bottomMediumNodes.push(node);
        } else if (node.type === "release") {
          // 以发行版为中心时，发行版本身在主区，其上层（work/collection）归上。
          (orientation === "release" ? topNodes : bottomReleaseNodes).push(node);
        } else if (node.type === "collection" || node.type === "work") {
          (orientation === "work" ? topNodes : bottomReleaseNodes).push(node);
        } else {
          rightOtherNodes.push(node);
        }
      });

      // Top (企划、原作、前作)
      topNodes.forEach((node, i) => {
        const span = Math.max(0, (topNodes.length - 1) * 250);
        const startX = centerX - span / 2 + (topNodes.length > 1 ? (span / (topNodes.length - 1)) * i : 0);
        map.set(node.id, { ...node, x: startX, y: centerY - 220, vx: 0, vy: 0, radius: 32 });
      });

      // Bottom 1 (发行版、规格)
      bottomReleaseNodes.forEach((node, i) => {
        const span = Math.max(0, (bottomReleaseNodes.length - 1) * 240);
        const startX =
          centerX - span / 2 + (bottomReleaseNodes.length > 1 ? (span / (bottomReleaseNodes.length - 1)) * i : 0);
        map.set(node.id, {
          ...node,
          x: startX,
          y: centerY + (bottomMediumNodes.length > 0 ? 180 : 220),
          vx: 0,
          vy: 0,
          radius: 30,
        });
      });

      // Bottom 2 (分碟载体 Mediums)
      bottomMediumNodes.forEach((node, i) => {
        const span = Math.max(0, (bottomMediumNodes.length - 1) * 210);
        const startX =
          centerX - span / 2 + (bottomMediumNodes.length > 1 ? (span / (bottomMediumNodes.length - 1)) * i : 0);
        map.set(node.id, { ...node, x: startX, y: centerY + 280, vx: 0, vy: 0, radius: 28 });
      });

      // Left (创作者、演职员、出版机构)
      leftArtistNodes.forEach((node, i) => {
        const total = leftArtistNodes.length;
        const startY = centerY - ((total - 1) * 125) / 2;
        map.set(node.id, { ...node, x: centerX - 360, y: startY + i * 125, vx: 0, vy: 0, radius: 30 });
      });

      // Right (跨媒介改编、原声带、衍生品等)
      rightOtherNodes.forEach((node, i) => {
        const total = rightOtherNodes.length;
        const startY = centerY - ((total - 1) * 125) / 2;
        map.set(node.id, { ...node, x: centerX + 360, y: startY + i * 125, vx: 0, vy: 0, radius: 30 });
      });
      }
    } else {
      // 自然力导向模拟排布 (Force-Directed)
      const simNodes: LayoutNode[] = [
        {
          ...centerNode,
          x: centerX,
          y: centerY,
          vx: 0,
          vy: 0,
          radius: 38,
        },
      ];

      otherNodes.forEach((node, i) => {
        const angle = (i / Math.max(1, totalOthers)) * 2 * Math.PI;
        const initDist = 250;
        simNodes.push({
          ...node,
          x: centerX + initDist * Math.cos(angle) + (Math.random() - 0.5) * 20,
          y: centerY + initDist * Math.sin(angle) + (Math.random() - 0.5) * 20,
          vx: 0,
          vy: 0,
          radius: 30,
        });
      });

      const kRep = 80000;
      const targetLen = 260;
      const kSpring = 0.04;
      const kCenter = 0.015;

      for (let iter = 0; iter < 60; iter++) {
        for (let i = 0; i < simNodes.length; i++) {
          for (let j = i + 1; j < simNodes.length; j++) {
            const n1 = simNodes[i];
            const n2 = simNodes[j];
            const dx = n2.x - n1.x;
            const dy = n2.y - n1.y;
            const dist = Math.hypot(dx, dy) || 1;
            if (dist < 220) {
              const force = (kRep / (dist * dist)) * 1.5;
              const fx = (dx / dist) * force;
              const fy = (dy / dist) * force;
              if (i !== 0) {
                n1.x -= fx;
                n1.y -= fy;
              }
              if (j !== 0) {
                n2.x += fx;
                n2.y += fy;
              }
            }
          }
        }

        filteredLinks.forEach((link) => {
          const sIdx = simNodes.findIndex((n) => n.id === link.source);
          const tIdx = simNodes.findIndex((n) => n.id === link.target);
          if (sIdx >= 0 && tIdx >= 0) {
            const sNode = simNodes[sIdx];
            const tNode = simNodes[tIdx];
            const dx = tNode.x - sNode.x;
            const dy = tNode.y - sNode.y;
            const dist = Math.hypot(dx, dy) || 1;
            const displacement = dist - targetLen;
            const fx = (dx / dist) * displacement * kSpring;
            const fy = (dy / dist) * displacement * kSpring;
            if (sIdx !== 0) {
              sNode.x += fx;
              sNode.y += fy;
            }
            if (tIdx !== 0) {
              tNode.x -= fx;
              tNode.y -= fy;
            }
          }
        });

        for (let i = 1; i < simNodes.length; i++) {
          const n = simNodes[i];
          n.x += (centerX - n.x) * kCenter;
          n.y += (centerY - n.y) * kCenter;
        }
      }

      simNodes.forEach((node) => {
        map.set(node.id, node);
      });
    }

    // 【全局几何中心校准】：无论哪种布局算法，均计算整体外接包围盒并自动平移对齐到 (500, 340)
    if (map.size > 0) {
      let bMinX = Infinity;
      let bMaxX = -Infinity;
      let bMinY = Infinity;
      let bMaxY = -Infinity;

      map.forEach((node) => {
        bMinX = Math.min(bMinX, node.x);
        bMaxX = Math.max(bMaxX, node.x);
        bMinY = Math.min(bMinY, node.y);
        bMaxY = Math.max(bMaxY, node.y);
      });

      const boundCenterX = (bMinX + bMaxX) / 2;
      const boundCenterY = (bMinY + bMaxY) / 2;
      const shiftX = centerX - boundCenterX;
      const shiftY = centerY - boundCenterY;

      // 平移校正所有节点
      map.forEach((node) => {
        node.x = +(node.x + shiftX).toFixed(2);
        node.y = +(node.y + shiftY).toFixed(2);
      });
    }

    return map;
  }, [activeNodes, centerEntityId, centerEntityType, layoutMode, filteredLinks, grouped, activeHub, typeGroups, displayNodes]);

  // 渲染用的边集：聚合态用合成边（hub 边无徽章，见 qualifier），平铺态用原边。
  const renderLinks = grouped ? displayLinks : filteredLinks;

  // 将屏幕 Client 坐标精确转换为 SVG viewBox (0..1000, 0..680) 坐标
  const getSvgPoint = useCallback((clientX: number, clientY: number): { x: number; y: number } => {
    const svg = svgRef.current;
    if (!svg) return { x: 500, y: 340 };
    try {
      const pt = svg.createSVGPoint();
      pt.x = clientX;
      pt.y = clientY;
      const ctm = svg.getScreenCTM();
      if (ctm) {
        const inverted = ctm.inverse();
        const svgPoint = pt.matrixTransform(inverted);
        if (Number.isFinite(svgPoint.x) && Number.isFinite(svgPoint.y)) {
          return { x: svgPoint.x, y: svgPoint.y };
        }
      }
    } catch {
      // 捕获不可逆矩阵异常
    }

    try {
      const rect = svg.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) {
        return {
          x: ((clientX - rect.left) / rect.width) * 1000,
          y: ((clientY - rect.top) / rect.height) * 680,
        };
      }
    } catch {
      // 容错处理
    }
    return { x: 500, y: 340 };
  }, []);

  // 自适应适配居中 (Auto-Fit & Centering)
  const fitToView = useCallback(
    (customNodes?: Map<string, LayoutNode>) => {
      const nodesMap = customNodes || layoutNodes;
      if (!nodesMap || nodesMap.size === 0) {
        setZoom(1);
        setPan({ x: 0, y: 0 });
        return;
      }

      const nodeList = Array.from(nodesMap.values());
      let minX = Infinity;
      let maxX = -Infinity;
      let minY = Infinity;
      let maxY = -Infinity;

      nodeList.forEach((n) => {
        const halfPillWidth = 115;
        const topOffset = n.radius + 15;
        const bottomOffset = n.radius + 45;

        minX = Math.min(minX, n.x - halfPillWidth);
        maxX = Math.max(maxX, n.x + halfPillWidth);
        minY = Math.min(minY, n.y - topOffset);
        maxY = Math.max(maxY, n.y + bottomOffset);
      });

      if (!Number.isFinite(minX) || !Number.isFinite(maxX) || !Number.isFinite(minY) || !Number.isFinite(maxY)) {
        setZoom(1);
        setPan({ x: 0, y: 0 });
        return;
      }

      const boxWidth = Math.max(140, maxX - minX);
      const boxHeight = Math.max(140, maxY - minY);
      const boxCenterX = (minX + maxX) / 2;
      const boxCenterY = (minY + maxY) / 2;

      const svgWidth = 1000;
      const svgHeight = 680;
      const padding = 75; // 舒适的安全留白边界

      const availableWidth = svgWidth - padding * 2;
      const availableHeight = svgHeight - padding * 2;

      const scaleX = availableWidth / boxWidth;
      const scaleY = availableHeight / boxHeight;

      // 缩放范围限制在 [0.45, 1.25]
      const rawScale = Math.min(scaleX, scaleY);
      const idealZoom = Number.isFinite(rawScale) ? Math.min(1.25, Math.max(0.45, rawScale)) : 1;
      const roundedZoom = +idealZoom.toFixed(3);

      // 将包围盒几何中心严格对准 SVG 视口中心 (500, 340)
      const targetPanX = +(svgWidth / 2 - roundedZoom * boxCenterX).toFixed(2);
      const targetPanY = +(svgHeight / 2 - roundedZoom * boxCenterY).toFixed(2);

      setZoom(Number.isFinite(roundedZoom) ? roundedZoom : 1);
      setPan({
        x: Number.isFinite(targetPanX) ? targetPanX : 0,
        y: Number.isFinite(targetPanY) ? targetPanY : 0,
      });
    },
    [layoutNodes]
  );

  // 节点数据/布局改变时自动执行自适应居中
  useEffect(() => {
    fitToView();
  }, [layoutNodes, fitToView]);

  // 网页窗口内最大化切换逻辑 (In-Browser Fullscreen / 窗口内铺满视口)
  const toggleFullscreen = useCallback(() => {
    setIsFullscreen((prev) => !prev);
  }, []);

  // 对话框挂到 body 并进入浏览器顶层，不受页面祖先的 transform / contain 裁剪。
  // 原生 dialog 约束焦点与背景交互；滚动锁只在打开期间生效，退出或卸载均恢复。
  useEffect(() => {
    if (!isFullscreen) return;
    const dialog = dialogRef.current;
    if (!dialog) return;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const prevOverflow = document.body.style.overflow;
    dialog.showModal();
    document.body.style.overflow = "hidden";
    fullscreenButtonRef.current?.focus({ preventScroll: true });
    return () => {
      dialog.close();
      document.body.style.overflow = prevOverflow;
      // Portal 切换会重新挂载按钮，不能依赖已从文档移除的旧按钮。
      const restoreFocus = fullscreenButtonRef.current ?? previousFocus;
      if (restoreFocus?.isConnected) restoreFocus.focus({ preventScroll: true });
    };
  }, [isFullscreen]);

  // 监听容器与视口尺寸变动 (ResizeObserver)，无论是最大化切换还是窗口缩放，立即执行平滑自适应居中
  useEffect(() => {
    const el = canvasContainerRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;

    let rafId: number | null = null;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        if (entry.contentRect.width > 0 && entry.contentRect.height > 0) {
          if (rafId) cancelAnimationFrame(rafId);
          rafId = requestAnimationFrame(() => {
            fitToView();
          });
        }
      }
    });

    observer.observe(el);
    return () => {
      if (rafId) cancelAnimationFrame(rafId);
      observer.disconnect();
    };
  }, [isFullscreen, fitToView]);

  // 以指定 SVG 锚点为基准的定点缩放算法 (Anchor Zooming Math)
  const handleZoomAnchor = useCallback((factor: number, anchor?: { x: number; y: number }) => {
    setZoom((prevZoom) => {
      const nextZoom = Math.min(3.5, Math.max(0.3, +(prevZoom * factor).toFixed(3)));
      if (nextZoom === prevZoom) return prevZoom;
      const scaleChange = nextZoom / prevZoom;
      const ax = anchor?.x ?? 500;
      const ay = anchor?.y ?? 340;

      setPan((prevPan) => ({
        x: +(ax - (ax - prevPan.x) * scaleChange).toFixed(2),
        y: +(ay - (ay - prevPan.y) * scaleChange).toFixed(2),
      }));

      return nextZoom;
    });
  }, []);

  // 内嵌图谱让普通滚轮滚动页面；按 Ctrl / ⌘ 或放大查看时才接管缩放。
  useEffect(() => {
    const el = canvasContainerRef.current;
    if (!el) return;

    const handleNativeWheel = (e: WheelEvent) => {
      if (!isFullscreen && !e.ctrlKey && !e.metaKey) return;
      if (e.deltaY === 0) return;
      e.preventDefault();
      e.stopPropagation();

      const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15;
      const pt = getSvgPoint(e.clientX, e.clientY);
      handleZoomAnchor(factor, pt);
    };

    el.addEventListener("wheel", handleNativeWheel, { passive: false });
    return () => {
      el.removeEventListener("wheel", handleNativeWheel);
    };
  }, [isFullscreen, getSvgPoint, handleZoomAnchor]);

  // 鼠标 / 触控指针拖拽平移事件处理
  const handlePointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    if (!isFullscreen && e.pointerType === "touch") return;

    const target = e.target as HTMLElement | SVGElement;
    if (target.closest("[data-node-interactive='true']")) {
      return;
    }

    const svgPt = getSvgPoint(e.clientX, e.clientY);
    dragRef.current = {
      isDown: true,
      startX: svgPt.x,
      startY: svgPt.y,
      startPanX: pan.x,
      startPanY: pan.y,
      hasMoved: false,
    };
    setIsDragging(true);
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const handlePointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragRef.current.isDown) return;

    const svgPt = getSvgPoint(e.clientX, e.clientY);
    const dx = svgPt.x - dragRef.current.startX;
    const dy = svgPt.y - dragRef.current.startY;

    if (Math.abs(dx) > 1 || Math.abs(dy) > 1) {
      dragRef.current.hasMoved = true;
    }

    setPan({
      x: +(dragRef.current.startPanX + dx).toFixed(2),
      y: +(dragRef.current.startPanY + dy).toFixed(2),
    });
  };

  const handlePointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (dragRef.current.isDown) {
      dragRef.current.isDown = false;
      setIsDragging(false);
      try {
        e.currentTarget.releasePointerCapture(e.pointerId);
      } catch {
        // 忽略 releasePointerCapture 异常
      }
    }
  };

  // 选中的节点所连接的所有图谱关系 (用于 Inspector 详细卡片)
  const selectedNodeRelations = useMemo(() => {
    if (!selectedNode) return [];
    const list: {
      link: GraphLink;
      otherNode: LayoutNode | GraphNode | undefined;
      isOutgoing: boolean;
    }[] = [];

    filteredLinks.forEach((link) => {
      if (link.source === selectedNode.id) {
        const otherNode = layoutNodes.get(link.target) || nodes.find((n) => n.id === link.target);
        list.push({ link, otherNode, isOutgoing: true });
      } else if (link.target === selectedNode.id) {
        const otherNode = layoutNodes.get(link.source) || nodes.find((n) => n.id === link.source);
        list.push({ link, otherNode, isOutgoing: false });
      }
    });

    return list;
  }, [selectedNode, filteredLinks, layoutNodes, nodes]);

  const graph = (
    <div
      ref={containerRef}
      className={`relative min-h-0 flex flex-col overflow-hidden select-none ${
        isFullscreen
          ? "h-full w-full bg-background text-foreground"
          : `rounded-card border border-border/80 bg-card/60 backdrop-blur-md shadow-sm ${className}`
      }`}
      style={isFullscreen ? undefined : { height }}
    >
      {/* 顶部工具栏与控制面板 */}
      <div className="shrink-0 flex flex-wrap items-center justify-between gap-2 px-4 py-2.5 bg-background/85 border-b border-border/60 backdrop-blur-md z-10">
        <div className="flex items-center gap-2 min-w-0">
          <div className="p-1.5 rounded-lg bg-primary/10 text-primary shrink-0">
            <Network className="w-4 h-4" />
          </div>
          <div className="flex items-center gap-2 min-w-0">
            <h3 className="text-xs font-semibold text-foreground truncate">
              {title || t("graph.title")}
            </h3>
            <span className="text-[10px] font-mono font-medium text-muted-foreground px-2 py-0.5 rounded-chip bg-secondary border border-border/40 shrink-0">
              {t("graph.connectedNodes", { count: activeNodes.length })}
            </span>
          </div>
        </div>

        {/* 布局、过滤器与控制器 */}
        <div className="flex flex-wrap items-center gap-1.5">
          {headerRightExtra && (
            <div className="mr-1 border-r border-border/60 pr-2">{headerRightExtra}</div>
          )}

          {/* 拓扑排布模式切换 (层级结构 / 环形放射 / 力导向) */}
          <div className="hidden sm:flex items-center bg-secondary/80 rounded-lg p-0.5 border border-border/50 text-[11px]">
            <button
              type="button"
              onClick={() => setLayoutMode("hierarchy")}
              title={t("graph.layoutHierarchy")}
              className={`px-2.5 py-1 rounded-md font-medium transition-all ${
                layoutMode === "hierarchy"
                  ? "bg-background text-foreground shadow-xs"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              <Layers className="w-3.5 h-3.5 inline mr-1" />
              {t("graph.layoutHierarchy")}
            </button>
            <button
              type="button"
              onClick={() => setLayoutMode("radial")}
              title={t("graph.layoutRadial")}
              className={`px-2.5 py-1 rounded-md font-medium transition-all ${
                layoutMode === "radial"
                  ? "bg-background text-foreground shadow-xs"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              <Compass className="w-3.5 h-3.5 inline mr-1" />
              {t("graph.layoutRadial")}
            </button>
            <button
              type="button"
              onClick={() => setLayoutMode("force")}
              title={t("graph.layoutForce")}
              className={`px-2.5 py-1 rounded-md font-medium transition-all ${
                layoutMode === "force"
                  ? "bg-background text-foreground shadow-xs"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              <Sparkles className="w-3.5 h-3.5 inline mr-1" />
              {t("graph.layoutForce")}
            </button>
          </div>

          {/* 缩放、自适应与全屏控制区 */}
          <div className="flex items-center gap-1 border-l border-border/60 pl-1.5">
            <button
              type="button"
              onClick={() => handleZoomAnchor(1.25)}
              title={t("graph.zoomIn")}
              className="p-1.5 rounded-lg hover:bg-secondary text-muted-foreground hover:text-foreground transition-colors duration-fast ease-soft"
            >
              <ZoomIn className="w-4 h-4" />
            </button>
            <span className="text-[11px] font-mono text-muted-foreground/80 px-1 select-none min-w-[38px] text-center">
              {Math.round(zoom * 100)}%
            </span>
            <button
              type="button"
              onClick={() => handleZoomAnchor(1 / 1.25)}
              title={t("graph.zoomOut")}
              className="p-1.5 rounded-lg hover:bg-secondary text-muted-foreground hover:text-foreground transition-colors duration-fast ease-soft"
            >
              <ZoomOut className="w-4 h-4" />
            </button>
            <button
              type="button"
              onClick={() => fitToView()}
              title={t("graph.fitView")}
              className="p-1.5 rounded-lg hover:bg-secondary text-muted-foreground hover:text-foreground transition-colors duration-fast ease-soft"
            >
              <Scan className="w-4 h-4" />
            </button>

            {/* 窗口内全屏/最大化切换按钮 */}
            <button
              ref={fullscreenButtonRef}
              type="button"
              onClick={toggleFullscreen}
              aria-label={isFullscreen ? t("graph.exitFullscreen") : t("graph.fullscreen")}
              title={isFullscreen ? t("graph.exitFullscreen") : t("graph.fullscreen")}
              className={`p-1.5 rounded-lg transition-all font-medium inline-flex items-center gap-1.5 ${
                isFullscreen
                  ? "bg-primary text-primary-foreground hover:bg-primary/90 shadow-sm"
                  : "hover:bg-primary/10 hover:text-primary text-muted-foreground"
              }`}
            >
              {isFullscreen ? (
                <>
                  <Minimize2 className="w-4 h-4" />
                  <span className="hidden md:inline text-xs font-sans">{t("graph.exitFullscreen")}</span>
                  <kbd className="hidden sm:inline-block text-[10px] font-mono bg-black/20 text-primary-foreground/90 px-1.5 py-0.5 rounded leading-none">
                    ESC
                  </kbd>
                </>
              ) : (
                <>
                  <Maximize2 className="w-4 h-4" />
                  <span className="hidden md:inline text-xs font-sans">{t("graph.fullscreen")}</span>
                </>
              )}
            </button>
          </div>
        </div>
      </div>

      {/* 关系筛选：维度与选项由连线数据与 definitions 动态生成，数据里没有的维度不出现 */}
      {relationFilter.filterable && (
        <div className="shrink-0 max-h-[30dvh] overflow-y-auto px-4 pt-2 pb-1 border-b border-border/60 bg-background/60">
          <RelationFilterBar
            facets={relationFilter.facets}
            selection={relationFilter.selection}
            onToggle={relationFilter.toggle}
          />
        </div>
      )}

      {/* 图谱主体 SVG 交互画布 */}
      <div
        ref={canvasContainerRef}
        className={`min-h-0 flex-1 w-full relative cursor-grab active:cursor-grabbing overflow-hidden select-none ${isFullscreen ? "touch-none" : "touch-pan-y touch-pinch-zoom"}`}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
      >
        <svg
          ref={svgRef}
          className="w-full h-full block"
          viewBox="0 0 1000 680"
          preserveAspectRatio="xMidYMid meet"
        >
          <defs>
            {/* 连线与标签投影滤镜 */}
            <filter id="badge-drop-shadow" x="-20%" y="-20%" width="140%" height="140%">
              <feDropShadow
                dx="0"
                dy="1.5"
                stdDeviation="2.5"
                floodColor="#000000"
                floodOpacity={isDark ? "0.35" : "0.08"}
              />
            </filter>
            <filter id="node-drop-shadow" x="-30%" y="-30%" width="160%" height="160%">
              <feDropShadow
                dx="0"
                dy="3"
                stdDeviation="4"
                floodColor="#000000"
                floodOpacity={isDark ? "0.45" : "0.15"}
              />
            </filter>

            {/* 中心节点柔和环境光晕与聚焦光环渐变 */}
            <radialGradient id="grad-center-halo" cx="50%" cy="50%" r="50%">
              <stop offset="50%" stopColor={isDark ? "#38bdf8" : "#0284c7"} stopOpacity="0.28" />
              <stop offset="100%" stopColor={isDark ? "#38bdf8" : "#0284c7"} stopOpacity="0" />
            </radialGradient>
            <linearGradient id="grad-center-ring" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor={isDark ? "#38bdf8" : "#0284c7"} />
              <stop offset="100%" stopColor={isDark ? "#818cf8" : "#4f46e5"} />
            </linearGradient>

            {/* 节点通用渐变定义（固定八实体骨架；主题色见 getEntityTypeTheme） */}
            <linearGradient id="grad-agent" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#059669" />
              <stop offset="100%" stopColor="#047857" />
            </linearGradient>
            <linearGradient id="grad-collection" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#4f46e5" />
              <stop offset="100%" stopColor="#4338ca" />
            </linearGradient>
            <linearGradient id="grad-work" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#0284c7" />
              <stop offset="100%" stopColor="#0369a1" />
            </linearGradient>
            <linearGradient id="grad-content_unit" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#0d9488" />
              <stop offset="100%" stopColor="#0f766e" />
            </linearGradient>
            <linearGradient id="grad-expression" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#db2777" />
              <stop offset="100%" stopColor="#be185d" />
            </linearGradient>
            <linearGradient id="grad-release" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#d97706" />
              <stop offset="100%" stopColor="#b45309" />
            </linearGradient>
            <linearGradient id="grad-medium" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#9333ea" />
              <stop offset="100%" stopColor="#7e22ce" />
            </linearGradient>
            <linearGradient id="grad-track" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#ea580c" />
              <stop offset="100%" stopColor="#c2410c" />
            </linearGradient>
            <linearGradient id="grad-default" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#64748b" />
              <stop offset="100%" stopColor="#475569" />
            </linearGradient>

            {/* 连线箭头定义 */}
            <marker
              id="arrow-default"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill={isDark ? "#94a3b8" : "#64748b"} opacity="0.9" />
            </marker>
            <marker
              id="arrow-sky"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="#0284c7" />
            </marker>
            <marker
              id="arrow-emerald"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="#10b981" />
            </marker>
            <marker
              id="arrow-indigo"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="#6366f1" />
            </marker>
            <marker
              id="arrow-purple"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="#a855f7" />
            </marker>
            <marker
              id="arrow-amber"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="#f59e0b" />
            </marker>
            <marker
              id="arrow-rose"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="#f43f5e" />
            </marker>
            <marker
              id="arrow-teal"
              viewBox="0 0 10 10"
              refX="26"
              refY="5"
              markerWidth="6"
              markerHeight="6"
              orient="auto-start-reverse"
            >
              <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill="#14b8a6" />
            </marker>
          </defs>

          {/* 根视口变换容器 (平移与缩放矩阵) */}
          <g
            transform={`translate(${pan.x}, ${pan.y}) scale(${zoom})`}
            style={{
              transition: isDragging ? "none" : "transform 200ms cubic-bezier(0.16, 1, 0.3, 1)",
            }}
          >
            {/* 1. 渲染拓扑连线底层 (Edges Lines Layer) */}
            <g className="edges-lines-layer">
              {renderLinks.map((link, idx) => {
                const src = layoutNodes.get(link.source);
                const tgt = layoutNodes.get(link.target);
                if (!src || !tgt) return null;

                const isHovered = hoveredLinkId === (link.id || `${link.source}-${link.target}`);
                const colorHex = getLinkColorHex(link.color);
                const strokeWidth = isHovered ? 2.5 : 1.8;

                return (
                  <g
                    key={`line-${link.id || idx}`}
                    data-node-interactive="true"
                    className="transition-opacity duration-200 cursor-pointer"
                    onMouseEnter={() => setHoveredLinkId(link.id || `${link.source}-${link.target}`)}
                    onMouseLeave={() => setHoveredLinkId(null)}
                    onClick={() => onEdgeClick?.(link)}
                  >
                    {/* 背景透明粗线扩大鼠标交互区域 */}
                    <line
                      x1={src.x}
                      y1={src.y}
                      x2={tgt.x}
                      y2={tgt.y}
                      stroke="transparent"
                      strokeWidth={18}
                    />

                    {/* 实体连线 */}
                    <line
                      x1={src.x}
                      y1={src.y}
                      x2={tgt.x}
                      y2={tgt.y}
                      stroke={colorHex}
                      strokeWidth={strokeWidth}
                      strokeDasharray={link.is_hierarchical ? "none" : "5,3"}
                      opacity={isHovered ? 1 : 0.75}
                      markerEnd={`url(#arrow-${link.color || "default"})`}
                    />
                  </g>
                );
              })}
            </g>

            {/* 2. 渲染拓扑节点层 (Nodes Layer: 主圆、呼吸光环、封面、标题胶囊) */}
            <g className="nodes-layer">
              {Array.from(layoutNodes.values()).map((node) => {
                const isCenter = node.id === centerEntityId;
                const isHub = !isCenter && node.id.startsWith("__hub:");
                const hubType = isHub ? node.id.slice("__hub:".length) : "";
                const hubOpen = isHub && activeHub === hubType;
                const isSelected = selectedNode?.id === node.id;
                const isHovered = hoveredNodeId === node.id;
                const r = node.radius;
                const theme = getEntityTypeTheme(node.type, kindLabel, isDark);

                const typeLabel = theme.label;
                // hub 药丸只显示一行「类型名 (数量)」：类型徽章省了，名字就是标签。
                const hubName = isHub ? `${node.name} (${node.role ?? ""})` : "";
                const typeBadgeWidth = isHub ? 0 : Math.max(34, typeLabel.length * 11 + 10);
                const maxNameChars = 16;
                const truncatedName = isHub
                  ? hubName
                  : node.name.length > maxNameChars
                    ? `${node.name.slice(0, maxNameChars - 1)}…`
                    : node.name;
                const nameWidth = truncatedName.length * 12.5;
                const pillWidth = Math.min(240, Math.max(110, typeBadgeWidth + nameWidth + 24));
                const pillHeight = 28;

                return (
                  <g
                    key={node.id}
                    data-node-interactive="true"
                    transform={`translate(${node.x}, ${node.y})`}
                    className="cursor-pointer transition-transform duration-150 group"
                    onMouseEnter={() => setHoveredNodeId(node.id)}
                    onMouseLeave={() => setHoveredNodeId(null)}
                    onClick={(e) => {
                      e.stopPropagation();
                      // hub 点了只展开/收起（手风琴），不进检查器、不导航——它不是实体。
                      if (isHub) {
                        setExpandedHub(hubOpen ? null : hubType);
                        return;
                      }
                      setSelectedNode(node);
                      onNodeClick?.(node);
                    }}
                  >
                    <title>{`${node.name} (${typeLabel})`}</title>

                    {/* 中心节点呼吸光环与聚焦环 (Focus Ring) */}
                    {isCenter && (
                      <>
                        <circle
                          r={r + 14}
                          fill="url(#grad-center-halo)"
                          className="animate-pulse pointer-events-none"
                        />
                        <circle
                          r={r + 6}
                          fill="none"
                          stroke="url(#grad-center-ring)"
                          strokeWidth={1.8}
                          strokeDasharray="4 3"
                          opacity={0.85}
                          className="pointer-events-none"
                        />
                      </>
                    )}

                    {/* 选中/悬浮状态的高亮光环 (非中心节点) */}
                    {!isCenter && (isSelected || isHovered) && (
                      <circle
                        r={r + 6}
                        fill="none"
                        stroke={theme.primaryColor}
                        strokeWidth={2}
                        strokeOpacity={0.65}
                        className="pointer-events-none animate-pulse"
                      />
                    )}

                    {/* 节点主圆底色（hub 没有渐变定义，走主题纯色） */}
                    <circle
                      r={r}
                      fill={isCenter ? centerFill : isHub ? theme.bgFill : `url(#grad-${node.type})`}
                      stroke={isCenter ? centerBorder : theme.stroke}
                      strokeWidth={isCenter ? 3.5 : 2.5}
                      filter="url(#node-drop-shadow)"
                    />

                    {/* 封面图片剪裁区 */}
                    {node.cover_image_url ? (
                      <>
                        <clipPath id={`clip-${node.id}`}>
                          <circle r={r - 3} />
                        </clipPath>
                        <image
                          href={node.cover_image_url}
                          x={-(r - 3)}
                          y={-(r - 3)}
                          width={(r - 3) * 2}
                          height={(r - 3) * 2}
                          preserveAspectRatio="xMidYMid slice"
                          clipPath={`url(#clip-${node.id})`}
                        />
                      </>
                    ) : (
                      <text
                        textAnchor="middle"
                        y={isCenter ? 5 : 4}
                        fontSize={isCenter ? "14" : "12"}
                        fontWeight="bold"
                        fill="#ffffff"
                        className="pointer-events-none font-mono select-none"
                      >
                        {isHub ? (hubOpen ? "−" : "+") : node.type.slice(0, 2).toUpperCase()}
                      </text>
                    )}

                    {/* 节点下方名称与类型双层清晰药丸 (Node Title & Type Badge) */}
                    <g transform={`translate(0, ${r + 20})`}>
                      <rect
                        x={-pillWidth / 2}
                        y={-pillHeight / 2}
                        width={pillWidth}
                        height={pillHeight}
                        rx={pillHeight / 2}
                        fill={capsuleBg}
                        stroke={isSelected || isHovered ? theme.primaryColor : capsuleStroke}
                        strokeWidth={isSelected || isHovered ? 1.5 : 1}
                        filter="url(#badge-drop-shadow)"
                      />

                      {!isHub && (
                        <>
                          <rect
                            x={-pillWidth / 2 + 4}
                            y={-9}
                            width={typeBadgeWidth}
                            height={18}
                            rx={9}
                            fill={theme.bgFill}
                          />
                          <text
                            x={-pillWidth / 2 + 4 + typeBadgeWidth / 2}
                            y={3.5}
                            textAnchor="middle"
                            fontSize="10"
                            fontWeight="700"
                            fill={theme.textFill}
                            className="pointer-events-none select-none font-sans"
                          >
                            {typeLabel}
                          </text>
                        </>
                      )}

                      <text
                        x={isHub ? 0 : (-pillWidth / 2 + 4 + typeBadgeWidth + (pillWidth / 2 - 6)) / 2}
                        y={4.5}
                        textAnchor="middle"
                        fontSize="13"
                        fontWeight="600"
                        fill={nodeNameFill}
                        className="pointer-events-none select-none font-sans"
                      >
                        {truncatedName}
                      </text>
                    </g>
                  </g>
                );
              })}
            </g>

            {/* 3. 渲染连线谓词徽章层 (Edge Badges Top Layer) */}
            <g className="edges-badges-layer">
              {renderLinks.map((link, idx) => {
                const src = layoutNodes.get(link.source);
                const tgt = layoutNodes.get(link.target);
                if (!src || !tgt) return null;
                // 合成 hub 边不画谓词徽章：类型名已在 hub 药丸上，画了就是满屏重复。
                if (link.qualifier === "__hub" || link.qualifier === "__hub-member") return null;

                const isHovered = hoveredLinkId === (link.id || `${link.source}-${link.target}`);
                const colorHex = getLinkColorHex(link.color);

                const badgePos = calculateEdgeBadgePosition(src, tgt);

                const badgeLabel = link.label || link.type;
                const badgeWidth = Math.max(68, badgeLabel.length * 12 + 24);
                const badgeHeight = 24;

                return (
                  <g
                    key={`badge-${link.id || idx}`}
                    data-node-interactive="true"
                    transform={`translate(${badgePos.x}, ${badgePos.y})`}
                    className="cursor-pointer transition-all duration-150"
                    onMouseEnter={() => setHoveredLinkId(link.id || `${link.source}-${link.target}`)}
                    onMouseLeave={() => setHoveredLinkId(null)}
                    onClick={() => onEdgeClick?.(link)}
                  >
                    <rect
                      x={-badgeWidth / 2}
                      y={-badgeHeight / 2}
                      width={badgeWidth}
                      height={badgeHeight}
                      rx={6}
                      fill={edgeBadgeBg}
                      stroke={isHovered ? colorHex : edgeBadgeStroke}
                      strokeWidth={isHovered ? 1.5 : 1}
                      filter="url(#badge-drop-shadow)"
                      className="transition-all duration-base ease-soft"
                    />
                    <circle cx={-badgeWidth / 2 + 10} cy={0} r={3} fill={colorHex} />
                    <text
                      x={4}
                      y={3.8}
                      textAnchor="middle"
                      fontSize="11"
                      fontWeight="600"
                      fill={edgeBadgeText}
                      className="pointer-events-none font-sans select-none"
                    >
                      {badgeLabel}
                    </text>
                  </g>
                );
              })}
            </g>
          </g>
        </svg>

        {/* 底部交互操作提示浮层 */}
        <div className="absolute bottom-3 left-4 pointer-events-none text-[11px] text-muted-foreground/90 flex items-center gap-1.5 bg-background/85 backdrop-blur-md px-3 py-1.5 rounded-panel border border-border/50 shadow-xs">
          <Info className="w-3.5 h-3.5 text-primary shrink-0" />
          <span>{t("graph.interactiveHint")}</span>
        </div>
      </div>

      {/* 侧边节点详细信息检查器 (Property Inspector Popover) */}
      {showInspector && selectedNode && (
        <div className="absolute top-14 right-4 w-80 max-w-[calc(100vw-2rem)] max-h-[calc(100%-4.5rem)] overflow-y-auto rounded-panel border border-border bg-card/98 backdrop-blur-xl p-4 shadow-2xl z-20 space-y-3.5 animate-in duration-200">
          <div className="flex items-start justify-between gap-2 pb-2.5 border-b border-border/60">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5 mb-1">
                <span
                  className={`text-[10px] font-semibold px-2 py-0.5 rounded-chip border ${
                    getEntityTypeTheme(selectedNode.type, kindLabel, isDark).badgeBgClass
                  }`}
                >
                  {getEntityTypeTheme(selectedNode.type, kindLabel, isDark).label}
                </span>
                {selectedNode.id === centerEntityId && (
                  <span className="text-[10px] font-medium px-2 py-0.5 rounded-chip bg-primary/15 text-primary border border-primary/25">
                    {t("graph.centerNode")}
                  </span>
                )}
              </div>
              <h4 className="text-sm font-bold text-foreground truncate" title={selectedNode.name}>
                {selectedNode.name}
              </h4>
              {selectedNode.original_name && (
                <p className="text-xs text-muted-foreground truncate" title={selectedNode.original_name}>
                  {selectedNode.original_name}
                </p>
              )}
            </div>
            <button
              type="button"
              onClick={() => setSelectedNode(null)}
              className="text-muted-foreground hover:text-foreground text-xs p-1.5 rounded-lg hover:bg-secondary transition-colors duration-fast ease-soft"
            >
              <X className="w-4 h-4" />
            </button>
          </div>

          {/* 封面海报大图预览 */}
          {selectedNode.cover_image_url && (
            <div className="relative w-full aspect-[3/4] rounded-xl overflow-hidden border border-border/60 bg-muted shadow-xs group">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={selectedNode.cover_image_url}
                alt={selectedNode.name}
                className="w-full h-full object-cover transition-transform duration-300 group-hover:scale-105"
              />
            </div>
          )}

          {/* 实体基础属性网格 */}
          <div className="space-y-1.5 text-xs text-muted-foreground bg-secondary/40 p-2.5 rounded-xl border border-border/40">
            {selectedNode.country && (
              <div className="flex justify-between items-center">
                <span className="flex items-center gap-1">
                  <Globe className="w-3 h-3 text-muted-foreground" />
                  {t("graph.country")}:
                </span>
                <span className="font-mono text-foreground font-medium">{selectedNode.country}</span>
              </div>
            )}

            {selectedNode.disambiguation && (
              <div className="pt-1 text-[11px] text-muted-foreground border-t border-border/40">
                <span className="font-semibold text-foreground/80">{t("graph.disambiguation")}: </span>
                {selectedNode.disambiguation}
              </div>
            )}
          </div>

          {/* 图谱中与此节点的关联关系列表 */}
          <div className="space-y-2">
            <h5 className="text-xs font-semibold text-foreground flex items-center justify-between">
              <span>{t("graph.connectedEdges")}</span>
              <span className="text-[10px] font-mono text-muted-foreground px-1.5 py-0.2 rounded bg-secondary">
                {selectedNodeRelations.length}
              </span>
            </h5>

            <div className="max-h-36 overflow-y-auto space-y-1.5 pr-1 text-xs">
              {selectedNodeRelations.length > 0 ? (
                selectedNodeRelations.map((rel, i) => {
                  const relColor = getLinkColorHex(rel.link.color);
                  return (
                    <div
                      key={rel.link.id || i}
                      className="flex items-center justify-between gap-1.5 p-1.5 rounded-lg bg-background border border-border/50 text-[11px]"
                    >
                      <div className="flex items-center gap-1 min-w-0">
                        {rel.isOutgoing ? (
                          <ArrowRight className="w-3 h-3 text-primary shrink-0" />
                        ) : (
                          <ArrowLeft className="w-3 h-3 text-emerald-500 shrink-0" />
                        )}
                        <span
                          className="font-medium px-1.5 py-0.5 rounded text-[10px]"
                          style={{
                            backgroundColor: `${relColor}15`,
                            color: relColor,
                          }}
                        >
                          {rel.link.label}
                        </span>
                      </div>
                      <span className="font-medium text-foreground truncate max-w-[110px]" title={rel.otherNode?.name}>
                        {rel.otherNode?.name || "Target"}
                      </span>
                    </div>
                  );
                })
              ) : (
                <div className="text-[11px] text-muted-foreground text-center py-2">
                  {t("graph.noConnectedEdges")}
                </div>
              )}
            </div>
          </div>

          {/* 底部操作区：进入详情档案 */}
          <div className="pt-2.5 border-t border-border/60 flex items-center justify-end gap-2">
            <Link
              href={catalogEntityHref(selectedNode.id)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-primary text-primary-foreground text-xs font-semibold hover:bg-primary/90 transition-colors duration-fast ease-soft shadow-xs"
            >
              <span>{t("graph.inspectEntity")}</span>
              <ExternalLink className="w-3.5 h-3.5" />
            </Link>
          </div>
        </div>
      )}
    </div>
  );

  if (!isFullscreen) return graph;

  return (
    <>
      {/* 保留内嵌位置的高度，关闭放大视图后页面仍处于原来的滚动位置。 */}
      <div aria-hidden="true" className={className} style={{ height }} />
      {createPortal(
        <dialog
          ref={dialogRef}
          aria-label={title || t("graph.title")}
          aria-modal="true"
          onCancel={(event) => {
            event.preventDefault();
            setIsFullscreen(false);
          }}
          className="fixed inset-0 m-0 h-dvh w-screen max-h-none max-w-none overflow-hidden border-0 bg-background p-0 text-foreground backdrop:bg-black/50"
        >
          {graph}
        </dialog>,
        document.body,
      )}
    </>
  );
};
