"use client";

import React, { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { createPortal } from "react-dom";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { useTheme } from "@/lib/themeContext";
import { GraphNode, GraphLink, catalogEntityHref } from "@/lib/api";
import { useDefinitions, getKindName } from "@/lib/definitions";
import { RelationFilterBar, useRelationFilter } from "@/components/entity/RelationFilterBar";
import {
  buildDisplayGraph,
  computeGraphLayout,
  GraphLayoutMode,
  hubKeyFromNodeId,
  isHubNodeId,
  PositionedGraphNode,
  withAncestorBridgeLinks,
} from "@/lib/relationGraphLayout";
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
  RotateCcw,
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
  orientation?: "work" | "release" | "agent";
  showInspector?: boolean;
  title?: string;
  headerRightExtra?: React.ReactNode;
}

type GraphScopeMode = "all" | "structure" | "relations";

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
        badgeBgClass: "bg-surfaceSubtle text-text-body border-line",
        icon: Tag,
      };
  }
};

const getLinkColorHex = (color?: string, isHierarchical?: boolean, isDark = false) => {
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
      if (isHierarchical) return isDark ? "#94a3b8" : "#475569";
      return isDark ? "#71717a" : "#64748b";
  }
};

function visualUnits(text: string): number {
  let units = 0;
  for (const ch of text) {
    units += ch.charCodeAt(0) > 0xff ? 1.85 : 1;
  }
  return units;
}

function truncateByUnits(text: string, maxUnits: number): string {
  if (!text) return "";
  let acc = 0;
  let out = "";
  for (const ch of text) {
    const w = ch.charCodeAt(0) > 0xff ? 1.85 : 1;
    if (acc + w > maxUnits) return out + "…";
    acc += w;
    out += ch;
  }
  return out;
}

function buildEdgeCurve(
  src: PositionedGraphNode,
  tgt: PositionedGraphNode,
): { d: string; midX: number; midY: number } {
  const dx = tgt.x - src.x;
  const dy = tgt.y - src.y;

  if (Math.abs(dy) >= 42) {
    const dirY = dy >= 0 ? 1 : -1;
    const x1 = src.x;
    const y1 = src.y + dirY * (src.height / 2 + 1);
    const x2 = tgt.x;
    const y2 = tgt.y - dirY * (tgt.height / 2 + 4);
    const spanY = Math.abs(y2 - y1);
    const ctrl = Math.max(28, spanY * 0.46);
    const cy1 = y1 + dirY * ctrl;
    const cy2 = y2 - dirY * ctrl;
    const midX = 0.5 * (x1 + x2);
    const midY = 0.125 * y1 + 0.375 * cy1 + 0.375 * cy2 + 0.125 * y2;
    return {
      d: "M " + x1 + " " + y1 + " C " + x1 + " " + cy1 + ", " + x2 + " " + cy2 + ", " + x2 + " " + y2,
      midX,
      midY,
    };
  }

  const dirX = dx >= 0 ? 1 : -1;
  const x1 = src.x + dirX * (src.width / 2 + 1);
  const y1 = src.y;
  const x2 = tgt.x - dirX * (tgt.width / 2 + 4);
  const y2 = tgt.y;
  const spanX = Math.abs(x2 - x1);
  const lift = spanX > 260 ? -38 : 0;
  const cx1 = x1 + dirX * Math.max(24, spanX * 0.35);
  const cx2 = x2 - dirX * Math.max(24, spanX * 0.35);
  const cy1 = y1 + lift;
  const cy2 = y2 + lift;
  return {
    d: "M " + x1 + " " + y1 + " C " + cx1 + " " + cy1 + ", " + cx2 + " " + cy2 + ", " + x2 + " " + y2,
    midX: (x1 + x2) / 2,
    midY: (y1 + y2) / 2 + lift * 0.75,
  };
}

export const InteractiveRelationGraph: React.FC<InteractiveRelationGraphProps> = ({
  centerEntityId,
  centerEntityType,
  nodes,
  links,
  height = 580,
  className = "",
  onNodeClick,
  onEdgeClick,
  showInspector = true,
  title,
  headerRightExtra,
}) => {
  const { t, tr, locale } = useI18n();
  const { kinds } = useDefinitions();
  const { resolvedMode } = useTheme();
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

  const [viewport, setViewport] = useState<{ w: number; h: number }>({ w: 1000, h: 580 });
  const viewportRef = useRef(viewport);
  viewportRef.current = viewport;

  const panDragRef = useRef<{
    isDown: boolean;
    startX: number;
    startY: number;
    startPanX: number;
    startPanY: number;
  }>({
    isDown: false,
    startX: 0,
    startY: 0,
    startPanX: 0,
    startPanY: 0,
  });

  const nodeDragRef = useRef<{
    nodeId: string;
    pointerId: number;
    startClientX: number;
    startClientY: number;
    startSvgX: number;
    startSvgY: number;
    origX: number;
    origY: number;
    moved: boolean;
  } | null>(null);

  const [zoom, setZoom] = useState(1);
  const zoomRef = useRef(zoom);
  zoomRef.current = zoom;
  const [pan, setPan] = useState({ x: 500, y: 290 });
  const [isPanning, setIsPanning] = useState(false);
  const [draggingNodeId, setDraggingNodeId] = useState<string | null>(null);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [layoutMode, setLayoutMode] = useState<GraphLayoutMode>("hierarchy");
  const [scopeMode, setScopeMode] = useState<GraphScopeMode>("all");
  const [selectedNode, setSelectedNode] = useState<GraphNode | null>(null);
  const [hoveredNodeId, setHoveredNodeId] = useState<string | null>(null);
  const [hoveredLinkId, setHoveredLinkId] = useState<string | null>(null);
  const [groupOverride, setGroupOverride] = useState<boolean | null>(null);
  const [expandedHubs, setExpandedHubs] = useState<Set<string>>(() => new Set());
  const [manualPositions, setManualPositions] = useState<Record<string, { x: number; y: number }>>({});

  useEffect(() => {
    setManualPositions({});
    setExpandedHubs(new Set());
    setSelectedNode(null);
  }, [centerEntityId]);

  const themePaint = (name: string, fallback: string) => {
    if (typeof window === "undefined") return fallback;
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
  };
  const cardBg = themePaint("--surface-color", isDark ? "#232a2e" : "#ffffff");
  const cardStroke = themePaint("--line-color", isDark ? "rgba(211,198,170,0.18)" : "rgba(92,106,114,0.2)");
  const titleFill = themePaint("--text-strong-color", isDark ? "#e6e1cf" : "#1f2937");
  const subFill = themePaint("--text-muted-color", isDark ? "#9da9a0" : "#64748b");
  const edgeBadgeBg = cardBg;
  const edgeBadgeStroke = themePaint("--line-strong-color", cardStroke);
  const edgeBadgeText = themePaint("--text-body-color", titleFill);
  const centerAccent = themePaint("--primary-color", isDark ? "#a7c080" : "#0284c7");

  const hasStructureLinks = useMemo(() => links.some((l) => Boolean(l.is_hierarchical)), [links]);
  const hasRelationLinks = useMemo(() => links.some((l) => !l.is_hierarchical), [links]);
  const effectiveScope: GraphScopeMode =
    hasStructureLinks && hasRelationLinks ? scopeMode : "all";

  const scopeLinks = useMemo(() => {
    if (effectiveScope === "structure") return links.filter((l) => Boolean(l.is_hierarchical));
    if (effectiveScope === "relations") {
      const relOnly = links.filter((l) => !l.is_hierarchical);
      return withAncestorBridgeLinks(centerEntityId, links, relOnly);
    }
    return links;
  }, [links, effectiveScope, centerEntityId]);

  const nonHierarchicalScopeLinks = useMemo(
    () => scopeLinks.filter((l) => !l.is_hierarchical),
    [scopeLinks],
  );

  const facetRows = useMemo(
    () =>
      nonHierarchicalScopeLinks.map((link, index) => ({
        type: link.type,
        kind:
          (link.source === centerEntityId
            ? link.target_type
            : link.target === centerEntityId
              ? link.source_type
              : link.target_type || link.source_type) || "",
        label: link.label,
        index,
      })),
    [nonHierarchicalScopeLinks, centerEntityId],
  );
  const relationFilter = useRelationFilter(facetRows);

  const filteredLinks = useMemo(() => {
    if (nonHierarchicalScopeLinks.length === 0) return scopeLinks;
    if (relationFilter.visible.length === nonHierarchicalScopeLinks.length) return scopeLinks;
    const matched = relationFilter.visible.map((row) => nonHierarchicalScopeLinks[row.index]);
    return withAncestorBridgeLinks(centerEntityId, scopeLinks, matched);
  }, [scopeLinks, nonHierarchicalScopeLinks, relationFilter.visible, centerEntityId]);

  const activeNodeIds = useMemo(() => {
    const set = new Set<string>([centerEntityId]);
    for (const l of filteredLinks) {
      if (l.source) set.add(l.source);
      if (l.target) set.add(l.target);
    }
    return set;
  }, [centerEntityId, filteredLinks]);

  const activeNodes = useMemo(() => {
    const list = nodes.filter((n) => activeNodeIds.has(n.id));
    if (!list.some((n) => n.id === centerEntityId)) {
      list.unshift({
        id: centerEntityId,
        name: centerEntityId,
        type: centerEntityType,
        category: "center",
        level: 0,
      });
    }
    return list;
  }, [nodes, activeNodeIds, centerEntityId, centerEntityType]);

  const wantGrouping = layoutMode === "hierarchy" && (groupOverride ?? activeNodes.length > 26);
  const displayGraph = useMemo(
    () => buildDisplayGraph(centerEntityId, activeNodes, filteredLinks, wantGrouping, expandedHubs, 6),
    [centerEntityId, activeNodes, filteredLinks, wantGrouping, expandedHubs],
  );
  const canGroup = layoutMode === "hierarchy" && displayGraph.canGroup;
  const grouped = canGroup && wantGrouping;

  const baseLayoutNodes = useMemo(
    () =>
      computeGraphLayout(
        centerEntityId,
        centerEntityType,
        displayGraph.nodes,
        displayGraph.links,
        layoutMode,
      ),
    [centerEntityId, centerEntityType, displayGraph.nodes, displayGraph.links, layoutMode],
  );

  const positionedNodes = useMemo(() => {
    if (Object.keys(manualPositions).length === 0) return baseLayoutNodes;
    const merged = new Map<string, PositionedGraphNode>();
    for (const [id, node] of baseLayoutNodes) {
      const override = manualPositions[id];
      merged.set(id, override ? { ...node, x: override.x, y: override.y } : node);
    }
    return merged;
  }, [baseLayoutNodes, manualPositions]);

  const renderLinks = displayGraph.links;
  const hasManualPositions = Object.keys(manualPositions).length > 0;

  const getSvgPoint = useCallback((clientX: number, clientY: number): { x: number; y: number } => {
    const svg = svgRef.current;
    if (!svg) return { x: viewportRef.current.w / 2, y: viewportRef.current.h / 2 };
    try {
      const pt = svg.createSVGPoint();
      pt.x = clientX;
      pt.y = clientY;
      const ctm = svg.getScreenCTM();
      if (ctm) {
        const svgPt = pt.matrixTransform(ctm.inverse());
        if (Number.isFinite(svgPt.x) && Number.isFinite(svgPt.y)) {
          return { x: svgPt.x, y: svgPt.y };
        }
      }
    } catch {
      // ignore CTM inverse failure
    }
    const rect = svg.getBoundingClientRect();
    if (rect.width > 0 && rect.height > 0) {
      return {
        x: ((clientX - rect.left) / rect.width) * viewportRef.current.w,
        y: ((clientY - rect.top) / rect.height) * viewportRef.current.h,
      };
    }
    return { x: viewportRef.current.w / 2, y: viewportRef.current.h / 2 };
  }, []);

  const fitToView = useCallback(
    (targetMap?: Map<string, PositionedGraphNode>) => {
      const nodesMap = targetMap || baseLayoutNodes;
      const vw = viewportRef.current.w || 1000;
      const vh = viewportRef.current.h || 580;
      if (!nodesMap || nodesMap.size === 0) {
        setZoom(1);
        setPan({ x: vw / 2, y: vh / 2 });
        return;
      }
      let minX = Infinity;
      let maxX = -Infinity;
      let minY = Infinity;
      let maxY = -Infinity;
      for (const n of nodesMap.values()) {
        minX = Math.min(minX, n.x - n.width / 2);
        maxX = Math.max(maxX, n.x + n.width / 2);
        minY = Math.min(minY, n.y - n.height / 2);
        maxY = Math.max(maxY, n.y + n.height / 2);
      }
      if (!Number.isFinite(minX) || !Number.isFinite(maxX)) {
        setZoom(1);
        setPan({ x: vw / 2, y: vh / 2 });
        return;
      }
      const padX = 56;
      const padY = 52;
      const boxW = Math.max(220, maxX - minX);
      const boxH = Math.max(120, maxY - minY);
      const cx = (minX + maxX) / 2;
      const cy = (minY + maxY) / 2;
      const scale = Math.min((vw - padX * 2) / boxW, (vh - padY * 2) / boxH);
      const clamped = Number.isFinite(scale) ? Math.min(1.15, Math.max(0.22, scale)) : 1;
      const rounded = +clamped.toFixed(3);
      setZoom(rounded);
      setPan({
        x: +(vw / 2 - rounded * cx).toFixed(1),
        y: +(vh / 2 - rounded * cy).toFixed(1),
      });
    },
    [baseLayoutNodes],
  );

  useEffect(() => {
    const el = canvasContainerRef.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    let initialized = false;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const w = Math.round(entry.contentRect.width);
        const h = Math.round(entry.contentRect.height);
        if (w > 40 && h > 40) {
          setViewport((prev) => (prev.w === w && prev.h === h ? prev : { w, h }));
          viewportRef.current = { w, h };
          if (!initialized) {
            initialized = true;
            requestAnimationFrame(() => fitToView());
          }
        }
      }
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [isFullscreen, fitToView]);

  useEffect(() => {
    fitToView(baseLayoutNodes);
  }, [baseLayoutNodes, isFullscreen, fitToView]);

  const resetPositions = useCallback(() => {
    setManualPositions({});
    fitToView(baseLayoutNodes);
  }, [baseLayoutNodes, fitToView]);

  const toggleFullscreen = useCallback(() => {
    setIsFullscreen((prev) => !prev);
  }, []);

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
      const restoreFocus = fullscreenButtonRef.current ?? previousFocus;
      if (restoreFocus?.isConnected) restoreFocus.focus({ preventScroll: true });
    };
  }, [isFullscreen]);

  const handleZoomAnchor = useCallback((factor: number, anchor?: { x: number; y: number }) => {
    setZoom((prevZoom) => {
      const nextZoom = Math.min(3.0, Math.max(0.15, +(prevZoom * factor).toFixed(3)));
      if (nextZoom === prevZoom) return prevZoom;
      const scaleChange = nextZoom / prevZoom;
      const ax = anchor?.x ?? viewportRef.current.w / 2;
      const ay = anchor?.y ?? viewportRef.current.h / 2;
      setPan((prevPan) => ({
        x: +(ax - (ax - prevPan.x) * scaleChange).toFixed(1),
        y: +(ay - (ay - prevPan.y) * scaleChange).toFixed(1),
      }));
      return nextZoom;
    });
  }, []);

  useEffect(() => {
    const el = canvasContainerRef.current;
    if (!el) return;
    const handleNativeWheel = (e: WheelEvent) => {
      if (!isFullscreen && !e.ctrlKey && !e.metaKey) return;
      if (e.deltaY === 0) return;
      e.preventDefault();
      e.stopPropagation();
      const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15;
      handleZoomAnchor(factor, getSvgPoint(e.clientX, e.clientY));
    };
    el.addEventListener("wheel", handleNativeWheel, { passive: false });
    return () => el.removeEventListener("wheel", handleNativeWheel);
  }, [isFullscreen, getSvgPoint, handleZoomAnchor]);

  const handleCanvasPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    if (!isFullscreen && e.pointerType === "touch") return;
    const target = e.target as HTMLElement | SVGElement;
    if (target.closest("[data-node-interactive='true']")) return;

    const svgPt = getSvgPoint(e.clientX, e.clientY);
    panDragRef.current = {
      isDown: true,
      startX: svgPt.x,
      startY: svgPt.y,
      startPanX: pan.x,
      startPanY: pan.y,
    };
    setIsPanning(true);
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const handleCanvasPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!panDragRef.current.isDown) return;
    const svgPt = getSvgPoint(e.clientX, e.clientY);
    setPan({
      x: +(panDragRef.current.startPanX + (svgPt.x - panDragRef.current.startX)).toFixed(1),
      y: +(panDragRef.current.startPanY + (svgPt.y - panDragRef.current.startY)).toFixed(1),
    });
  };

  const handleCanvasPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (panDragRef.current.isDown) {
      panDragRef.current.isDown = false;
      setIsPanning(false);
      try {
        e.currentTarget.releasePointerCapture(e.pointerId);
      } catch {
        // ignore
      }
    }
  };

  const handleNodePointerDown = (e: React.PointerEvent<SVGGElement>, node: PositionedGraphNode) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    const svgPt = getSvgPoint(e.clientX, e.clientY);
    nodeDragRef.current = {
      nodeId: node.id,
      pointerId: e.pointerId,
      startClientX: e.clientX,
      startClientY: e.clientY,
      startSvgX: svgPt.x,
      startSvgY: svgPt.y,
      origX: node.x,
      origY: node.y,
      moved: false,
    };
    try {
      e.currentTarget.setPointerCapture(e.pointerId);
    } catch {
      // ignore
    }
  };

  const handleNodePointerMove = (e: React.PointerEvent<SVGGElement>, node: PositionedGraphNode) => {
    const st = nodeDragRef.current;
    if (!st || st.nodeId !== node.id) return;
    e.stopPropagation();
    const pxDist = Math.hypot(e.clientX - st.startClientX, e.clientY - st.startClientY);
    if (!st.moved && pxDist >= 5) {
      st.moved = true;
      setDraggingNodeId(node.id);
    }
    if (!st.moved) return;
    const svgPt = getSvgPoint(e.clientX, e.clientY);
    const z = zoomRef.current || 1;
    const nextX = +(st.origX + (svgPt.x - st.startSvgX) / z).toFixed(1);
    const nextY = +(st.origY + (svgPt.y - st.startSvgY) / z).toFixed(1);
    setManualPositions((prev) => ({
      ...prev,
      [node.id]: { x: nextX, y: nextY },
    }));
  };

  const handleNodePointerUp = (e: React.PointerEvent<SVGGElement>, node: PositionedGraphNode) => {
    const st = nodeDragRef.current;
    if (!st || st.nodeId !== node.id) return;
    e.stopPropagation();
    nodeDragRef.current = null;
    setDraggingNodeId(null);
    try {
      e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // ignore
    }
    if (st.moved) return;

    if (isHubNodeId(node.id)) {
      const hubKey = hubKeyFromNodeId(node.id);
      setExpandedHubs((prev) => {
        const next = new Set(prev);
        if (next.has(hubKey)) next.delete(hubKey);
        else next.add(hubKey);
        return next;
      });
      return;
    }
    setSelectedNode((prev) => (prev?.id === node.id ? null : node));
    onNodeClick?.(node);
  };

  const focusId = hoveredNodeId || selectedNode?.id || null;
  const highlightedIds = useMemo(() => {
    if (!focusId && !hoveredLinkId) return null;
    const set = new Set<string>();
    if (focusId) {
      set.add(focusId);
      for (const l of renderLinks) {
        if (l.source === focusId) set.add(l.target);
        if (l.target === focusId) set.add(l.source);
      }
    }
    if (hoveredLinkId) {
      for (const l of renderLinks) {
        const lid = l.id || l.source + "-" + l.target + "-" + l.type;
        if (lid === hoveredLinkId) {
          set.add(l.source);
          set.add(l.target);
        }
      }
    }
    return set;
  }, [focusId, hoveredLinkId, renderLinks]);

  const passiveBadgeAllowed = useMemo(() => {
    const counts = new Map<string, number>();
    for (const l of renderLinks) {
      if (l.is_hierarchical || l.qualifier === "__hub" || l.qualifier === "__hub-member") continue;
      const k = l.source + "::" + l.type;
      counts.set(k, (counts.get(k) || 0) + 1);
      const rk = l.target + "::" + l.type;
      counts.set(rk, (counts.get(rk) || 0) + 1);
    }
    const allow = new Set<string>();
    for (const l of renderLinks) {
      if (l.is_hierarchical || l.qualifier === "__hub" || l.qualifier === "__hub-member") continue;
      const lid = l.id || l.source + "-" + l.target + "-" + l.type;
      const c1 = counts.get(l.source + "::" + l.type) || 0;
      const c2 = counts.get(l.target + "::" + l.type) || 0;
      if (Math.max(c1, c2) <= 2) allow.add(lid);
    }
    return allow;
  }, [renderLinks]);

  const selectedNodeRelations = useMemo(() => {
    if (!selectedNode) return [];
    const list: {
      link: GraphLink;
      otherNode: GraphNode | undefined;
      isOutgoing: boolean;
    }[] = [];
    for (const link of filteredLinks) {
      if (link.source === selectedNode.id) {
        const otherNode = positionedNodes.get(link.target) || nodes.find((n) => n.id === link.target);
        list.push({ link, otherNode, isOutgoing: true });
      } else if (link.target === selectedNode.id) {
        const otherNode = positionedNodes.get(link.source) || nodes.find((n) => n.id === link.source);
        list.push({ link, otherNode, isOutgoing: false });
      }
    }
    return list;
  }, [selectedNode, filteredLinks, positionedNodes, nodes]);

  const containerClass = isFullscreen
    ? "relative min-h-0 flex flex-col overflow-hidden select-none h-full w-full bg-background text-foreground"
    : "relative min-h-0 flex flex-col overflow-hidden select-none rounded-card border border-border/80 bg-card/60 backdrop-blur-md shadow-sm " + className;

  const canvasClass =
    "min-h-0 flex-1 w-full relative overflow-hidden select-none " +
    (isPanning || draggingNodeId ? "cursor-grabbing " : "cursor-grab ") +
    (isFullscreen ? "touch-none" : "touch-pan-y touch-pinch-zoom");

  const graph = (
    <div
      ref={containerRef}
      className={containerClass}
      style={isFullscreen ? undefined : { height }}
    >
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

        <div className="flex flex-wrap items-center gap-1.5">
          {headerRightExtra && (
            <div className="mr-1 border-r border-border/60 pr-2">{headerRightExtra}</div>
          )}

          {hasStructureLinks && hasRelationLinks && (
            <div className="flex items-center bg-secondary/80 rounded-lg p-0.5 border border-border/50 text-[11px]">
              {(
                [
                  ["all", t("graph.scopeAll")],
                  ["structure", t("graph.scopeStructure")],
                  ["relations", t("graph.scopeRelations")],
                ] as const
              ).map(([mode, label]) => (
                <button
                  key={mode}
                  type="button"
                  onClick={() => setScopeMode(mode)}
                  className={
                    effectiveScope === mode
                      ? "px-2 py-1 rounded-md font-medium transition-all bg-background text-foreground shadow-xs"
                      : "px-2 py-1 rounded-md font-medium transition-all text-muted-foreground hover:text-foreground"
                  }
                >
                  {label}
                </button>
              ))}
            </div>
          )}

          {canGroup && (
            <button
              type="button"
              onClick={() => {
                if (grouped) {
                  setGroupOverride(false);
                } else {
                  setExpandedHubs(new Set());
                  setGroupOverride(true);
                }
              }}
              className="px-2.5 py-1 rounded-lg border border-border/60 bg-secondary/70 hover:bg-secondary text-[11px] font-medium text-foreground transition-colors"
            >
              {grouped ? t("graph.expandAll") : t("graph.groupByType")}
            </button>
          )}

          {hasManualPositions && (
            <button
              type="button"
              onClick={resetPositions}
              title={t("graph.resetLayout")}
              className="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg border border-primary/30 bg-primary/10 hover:bg-primary/15 text-[11px] font-medium text-primary transition-colors"
            >
              <RotateCcw className="w-3 h-3" />
              <span>{t("graph.resetLayout")}</span>
            </button>
          )}

          <div className="hidden sm:flex items-center bg-secondary/80 rounded-lg p-0.5 border border-border/50 text-[11px]">
            <button
              type="button"
              onClick={() => {
                setManualPositions({});
                setLayoutMode("hierarchy");
              }}
              title={t("graph.layoutHierarchy")}
              className={
                layoutMode === "hierarchy"
                  ? "px-2.5 py-1 rounded-md font-medium transition-all bg-background text-foreground shadow-xs"
                  : "px-2.5 py-1 rounded-md font-medium transition-all text-muted-foreground hover:text-foreground"
              }
            >
              <Layers className="w-3.5 h-3.5 inline mr-1" />
              {t("graph.layoutHierarchy")}
            </button>
            <button
              type="button"
              onClick={() => {
                setManualPositions({});
                setLayoutMode("radial");
              }}
              title={t("graph.layoutRadial")}
              className={
                layoutMode === "radial"
                  ? "px-2.5 py-1 rounded-md font-medium transition-all bg-background text-foreground shadow-xs"
                  : "px-2.5 py-1 rounded-md font-medium transition-all text-muted-foreground hover:text-foreground"
              }
            >
              <Compass className="w-3.5 h-3.5 inline mr-1" />
              {t("graph.layoutRadial")}
            </button>
            <button
              type="button"
              onClick={() => {
                setManualPositions({});
                setLayoutMode("force");
              }}
              title={t("graph.layoutForce")}
              className={
                layoutMode === "force"
                  ? "px-2.5 py-1 rounded-md font-medium transition-all bg-background text-foreground shadow-xs"
                  : "px-2.5 py-1 rounded-md font-medium transition-all text-muted-foreground hover:text-foreground"
              }
            >
              <Sparkles className="w-3.5 h-3.5 inline mr-1" />
              {t("graph.layoutForce")}
            </button>
          </div>

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
              onClick={() => fitToView(positionedNodes)}
              title={t("graph.fitView")}
              className="p-1.5 rounded-lg hover:bg-secondary text-muted-foreground hover:text-foreground transition-colors duration-fast ease-soft"
            >
              <Scan className="w-4 h-4" />
            </button>

            <button
              ref={fullscreenButtonRef}
              type="button"
              onClick={toggleFullscreen}
              aria-label={isFullscreen ? t("graph.exitFullscreen") : t("graph.fullscreen")}
              title={isFullscreen ? t("graph.exitFullscreen") : t("graph.fullscreen")}
              className={
                isFullscreen
                  ? "p-1.5 rounded-lg transition-all font-medium inline-flex items-center gap-1.5 bg-primary text-primary-foreground hover:bg-primary/90 shadow-sm"
                  : "p-1.5 rounded-lg transition-all font-medium inline-flex items-center gap-1.5 hover:bg-primary/10 hover:text-primary text-muted-foreground"
              }
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

      {effectiveScope !== "structure" && relationFilter.filterable && (
        <div className="shrink-0 max-h-[26dvh] overflow-y-auto px-4 pt-2 pb-1.5 border-b border-border/60 bg-background/60">
          <RelationFilterBar
            facets={relationFilter.facets}
            selection={relationFilter.selection}
            onToggle={relationFilter.toggle}
          />
        </div>
      )}

      <div
        ref={canvasContainerRef}
        className={canvasClass}
        onPointerDown={handleCanvasPointerDown}
        onPointerMove={handleCanvasPointerMove}
        onPointerUp={handleCanvasPointerUp}
        onPointerCancel={handleCanvasPointerUp}
      >
        <svg
          ref={svgRef}
          className="w-full h-full block"
          viewBox={"0 0 " + viewport.w + " " + viewport.h}
        >
          <defs>
            <filter id="mf-card-shadow" x="-15%" y="-15%" width="130%" height="135%">
              <feDropShadow
                dx="0"
                dy="2"
                stdDeviation="3"
                floodColor="#000000"
                floodOpacity={isDark ? "0.38" : "0.08"}
              />
            </filter>
            {(["default", "structure", "emerald", "sky", "blue", "purple", "indigo", "amber", "rose", "red", "teal"] as const).map(
              (c) => {
                const hex =
                  c === "structure"
                    ? getLinkColorHex(undefined, true, isDark)
                    : getLinkColorHex(c === "default" ? undefined : c, false, isDark);
                return (
                  <marker
                    key={c}
                    id={"mf-arrow-" + c}
                    viewBox="0 0 10 10"
                    refX="8"
                    refY="5"
                    markerWidth="5.5"
                    markerHeight="5.5"
                    orient="auto-start-reverse"
                  >
                    <path d="M 0 1.5 L 9 5 L 0 8.5 z" fill={hex} />
                  </marker>
                );
              },
            )}
          </defs>

          <g transform={"translate(" + pan.x + ", " + pan.y + ") scale(" + zoom + ")"}>
            <g className="edges-layer">
              {renderLinks.map((link, idx) => {
                const src = positionedNodes.get(link.source);
                const tgt = positionedNodes.get(link.target);
                if (!src || !tgt) return null;
                const lid = link.id || link.source + "-" + link.target + "-" + link.type;
                const isHovered = hoveredLinkId === lid;
                const touchesFocus =
                  Boolean(focusId) && (link.source === focusId || link.target === focusId);
                const active = isHovered || touchesFocus;
                const dimmed = Boolean(highlightedIds) && !active;
                const colorHex = getLinkColorHex(link.color, link.is_hierarchical, isDark);
                const markerKey = link.color || (link.is_hierarchical ? "structure" : "default");
                const { d } = buildEdgeCurve(src, tgt);

                return (
                  <g
                    key={"edge-" + lid + "-" + idx}
                    data-node-interactive="true"
                    className="cursor-pointer"
                    onMouseEnter={() => setHoveredLinkId(lid)}
                    onMouseLeave={() => setHoveredLinkId(null)}
                    onClick={(e) => {
                      e.stopPropagation();
                      onEdgeClick?.(link);
                    }}
                  >
                    <path d={d} fill="none" stroke="transparent" strokeWidth={14} />
                    <path
                      d={d}
                      fill="none"
                      stroke={colorHex}
                      strokeWidth={active ? 2.4 : link.is_hierarchical ? 1.75 : 1.45}
                      strokeDasharray={link.is_hierarchical ? "none" : "5,3"}
                      opacity={dimmed ? 0.18 : active ? 0.98 : link.is_hierarchical ? 0.72 : 0.58}
                      markerEnd={"url(#mf-arrow-" + markerKey + ")"}
                    />
                  </g>
                );
              })}
            </g>

            <g className="nodes-layer">
              {Array.from(positionedNodes.values()).map((node) => {
                const isCenter = node.id === centerEntityId;
                const isHub = isHubNodeId(node.id);
                const hubKey = isHub ? hubKeyFromNodeId(node.id) : "";
                const hubOpen = isHub && expandedHubs.has(hubKey);
                const isSelected = selectedNode?.id === node.id;
                const isHovered = hoveredNodeId === node.id;
                const isBeingDragged = draggingNodeId === node.id;
                const dimmed = Boolean(highlightedIds) && !highlightedIds!.has(node.id);
                const effectiveKind = isHub ? node.category || "related" : node.type;
                const theme = getEntityTypeTheme(effectiveKind, kindLabel, isDark);

                const w = node.width;
                const h = node.height;
                const halfW = w / 2;
                const halfH = h / 2;

                const strokeColor = isCenter
                  ? centerAccent
                  : isSelected || isHovered || isBeingDragged
                    ? theme.primaryColor
                    : cardStroke;
                const strokeWidth = isCenter ? 2.4 : isSelected || isHovered || isBeingDragged ? 2 : 1.15;

                const thumbSize = isHub ? 30 : 38;
                const thumbX = -halfW + 9;
                const thumbY = -thumbSize / 2;
                const textLeft = thumbX + thumbSize + 9;
                const textMaxUnits = isHub ? 17 : 19;
                const displayName = truncateByUnits(node.name, textMaxUnits);
                const badgeLabel = isHub ? kindLabel(effectiveKind) : theme.label;
                const badgeW = Math.min(84, Math.max(32, visualUnits(badgeLabel) * 5.8 + 12));
                const subNote = isHub ? "×" + (node.role || "") : node.role ? truncateByUnits(node.role, 9) : "";
                const clipId = "mf-clip-" + node.id.replace(/[^a-zA-Z0-9_-]/g, "_");

                return (
                  <g
                    key={node.id}
                    data-node-interactive="true"
                    transform={"translate(" + node.x + ", " + node.y + ")"}
                    opacity={dimmed ? 0.28 : 1}
                    className={isBeingDragged ? "cursor-grabbing" : "cursor-grab"}
                    onPointerDown={(e) => handleNodePointerDown(e, node)}
                    onPointerMove={(e) => handleNodePointerMove(e, node)}
                    onPointerUp={(e) => handleNodePointerUp(e, node)}
                    onPointerCancel={(e) => handleNodePointerUp(e, node)}
                    onMouseEnter={() => setHoveredNodeId(node.id)}
                    onMouseLeave={() => setHoveredNodeId(null)}
                  >
                    <title>{isHub ? node.name + " (" + node.role + ")" : node.name + " (" + theme.label + ")"}</title>

                    {isCenter && (
                      <rect
                        x={-halfW - 4}
                        y={-halfH - 4}
                        width={w + 8}
                        height={h + 8}
                        rx={13}
                        fill="none"
                        stroke={centerAccent}
                        strokeWidth={1.4}
                        strokeDasharray="4 3"
                        opacity={0.65}
                        className="pointer-events-none"
                      />
                    )}

                    <rect
                      x={-halfW}
                      y={-halfH}
                      width={w}
                      height={h}
                      rx={10}
                      fill={cardBg}
                      stroke={strokeColor}
                      strokeWidth={strokeWidth}
                      filter="url(#mf-card-shadow)"
                    />

                    <rect
                      x={-halfW}
                      y={-halfH + 8}
                      width={3}
                      height={h - 16}
                      rx={1.5}
                      fill={isCenter ? centerAccent : theme.primaryColor}
                    />

                    {node.cover_image_url && !isHub ? (
                      <>
                        <clipPath id={clipId}>
                          <rect x={thumbX} y={thumbY} width={thumbSize} height={thumbSize} rx={7} />
                        </clipPath>
                        <rect
                          x={thumbX}
                          y={thumbY}
                          width={thumbSize}
                          height={thumbSize}
                          rx={7}
                          fill={theme.bgFill}
                        />
                        <image
                          href={node.cover_image_url}
                          x={thumbX}
                          y={thumbY}
                          width={thumbSize}
                          height={thumbSize}
                          preserveAspectRatio="xMidYMid slice"
                          clipPath={"url(#" + clipId + ")"}
                        />
                      </>
                    ) : (
                      <>
                        <rect
                          x={thumbX}
                          y={thumbY}
                          width={thumbSize}
                          height={thumbSize}
                          rx={7}
                          fill={theme.bgFill}
                        />
                        <text
                          x={thumbX + thumbSize / 2}
                          y={4.5}
                          textAnchor="middle"
                          fontSize={isHub ? "15" : "11"}
                          fontWeight="700"
                          fill={theme.textFill}
                          className="pointer-events-none font-mono select-none"
                        >
                          {isHub ? (hubOpen ? "−" : "+") : effectiveKind.slice(0, 2).toUpperCase()}
                        </text>
                      </>
                    )}

                    <rect
                      x={textLeft}
                      y={-halfH + 8}
                      width={badgeW}
                      height={15}
                      rx={4}
                      fill={theme.bgFill}
                    />
                    <text
                      x={textLeft + badgeW / 2}
                      y={-halfH + 18.5}
                      textAnchor="middle"
                      fontSize="9.5"
                      fontWeight="700"
                      fill={theme.textFill}
                      className="pointer-events-none font-sans select-none"
                    >
                      {truncateByUnits(badgeLabel, 12)}
                    </text>

                    {subNote && (
                      <text
                        x={halfW - 9}
                        y={-halfH + 18.5}
                        textAnchor="end"
                        fontSize="10"
                        fontWeight="600"
                        fill={subFill}
                        className="pointer-events-none font-mono select-none"
                      >
                        {subNote}
                      </text>
                    )}

                    <text
                      x={textLeft}
                      y={halfH - 11}
                      textAnchor="start"
                      fontSize="12"
                      fontWeight="600"
                      fill={titleFill}
                      className="pointer-events-none font-sans select-none"
                    >
                      {displayName}
                    </text>
                  </g>
                );
              })}
            </g>

            <g className="edge-badges-layer">
              {renderLinks.map((link, idx) => {
                if (link.qualifier === "__hub" || link.qualifier === "__hub-member") return null;
                const src = positionedNodes.get(link.source);
                const tgt = positionedNodes.get(link.target);
                if (!src || !tgt) return null;
                const lid = link.id || link.source + "-" + link.target + "-" + link.type;
                const isHovered = hoveredLinkId === lid;
                const touchesFocus =
                  Boolean(focusId) && (link.source === focusId || link.target === focusId);
                const showBadge = isHovered || touchesFocus || (!highlightedIds && passiveBadgeAllowed.has(lid));
                if (!showBadge) return null;

                const labelText = truncateByUnits(link.label || link.type, 16);
                if (!labelText) return null;
                const { midX, midY } = buildEdgeCurve(src, tgt);
                const colorHex = getLinkColorHex(link.color, link.is_hierarchical, isDark);
                const badgeW = Math.max(52, visualUnits(labelText) * 6.2 + 22);
                const badgeH = 20;

                return (
                  <g
                    key={"badge-" + lid + "-" + idx}
                    data-node-interactive="true"
                    transform={"translate(" + midX + ", " + midY + ")"}
                    className="cursor-pointer"
                    onMouseEnter={() => setHoveredLinkId(lid)}
                    onMouseLeave={() => setHoveredLinkId(null)}
                    onClick={(e) => {
                      e.stopPropagation();
                      onEdgeClick?.(link);
                    }}
                  >
                    <rect
                      x={-badgeW / 2}
                      y={-badgeH / 2}
                      width={badgeW}
                      height={badgeH}
                      rx={5}
                      fill={edgeBadgeBg}
                      stroke={isHovered || touchesFocus ? colorHex : edgeBadgeStroke}
                      strokeWidth={isHovered || touchesFocus ? 1.4 : 1}
                    />
                    <circle cx={-badgeW / 2 + 8} cy={0} r={2.6} fill={colorHex} />
                    <text
                      x={3}
                      y={3.5}
                      textAnchor="middle"
                      fontSize="10"
                      fontWeight="600"
                      fill={edgeBadgeText}
                      className="pointer-events-none font-sans select-none"
                    >
                      {labelText}
                    </text>
                  </g>
                );
              })}
            </g>
          </g>
        </svg>

        <div className="absolute bottom-3 left-4 pointer-events-none text-[11px] text-muted-foreground/90 flex items-center gap-1.5 bg-background/85 backdrop-blur-md px-3 py-1.5 rounded-panel border border-border/50 shadow-xs">
          <Info className="w-3.5 h-3.5 text-primary shrink-0" />
          <span>{t("graph.interactiveHint")}</span>
        </div>
      </div>

      {showInspector && selectedNode && (
        <div className="absolute top-14 right-4 w-80 max-w-[calc(100vw-2rem)] max-h-[calc(100%-4.5rem)] overflow-y-auto rounded-panel border border-border bg-card/98 backdrop-blur-xl p-4 shadow-2xl z-20 space-y-3.5 animate-in duration-200">
          <div className="flex items-start justify-between gap-2 pb-2.5 border-b border-border/60">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-1.5 mb-1">
                <span
                  className={
                    "text-[10px] font-semibold px-2 py-0.5 rounded-chip border " +
                    getEntityTypeTheme(selectedNode.type, kindLabel, isDark).badgeBgClass
                  }
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
              {selectedNode.original_name && selectedNode.original_name !== selectedNode.name && (
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

          {selectedNode.cover_image_url && (
            <div className="relative w-full max-h-44 rounded-lg overflow-hidden border border-border/60 bg-muted/60 flex items-center justify-center">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={selectedNode.cover_image_url}
                alt={selectedNode.name}
                className="w-full h-auto max-h-44 object-contain"
              />
            </div>
          )}

          {(selectedNode.country || selectedNode.disambiguation) && (
            <div className="space-y-1.5 text-xs text-muted-foreground bg-secondary/40 p-2.5 rounded-lg border border-border/40">
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
          )}

          <div className="space-y-2">
            <h5 className="text-xs font-semibold text-foreground flex items-center justify-between">
              <span>{t("graph.connectedEdges")}</span>
              <span className="text-[10px] font-mono text-muted-foreground px-1.5 py-0.5 rounded bg-secondary">
                {selectedNodeRelations.length}
              </span>
            </h5>

            <div className="max-h-40 overflow-y-auto space-y-1.5 pr-1 text-xs">
              {selectedNodeRelations.length > 0 ? (
                selectedNodeRelations.map((rel, i) => {
                  const relColor = getLinkColorHex(rel.link.color, rel.link.is_hierarchical, isDark);
                  return (
                    <div
                      key={rel.link.id || i}
                      className="flex items-center justify-between gap-1.5 p-1.5 rounded-lg bg-background border border-border/50 text-[11px]"
                    >
                      <div className="flex items-center gap-1 min-w-0">
                        {rel.isOutgoing ? (
                          <ArrowRight className="w-3 h-3 text-primary shrink-0" />
                        ) : (
                          <ArrowLeft className="w-3 h-3 text-success shrink-0" />
                        )}
                        <span
                          className="font-medium px-1.5 py-0.5 rounded text-[10px] truncate max-w-[115px]"
                          style={{
                            backgroundColor: relColor + "18",
                            color: relColor,
                          }}
                        >
                          {rel.link.label || rel.link.type}
                        </span>
                      </div>
                      {rel.otherNode ? (
                        <button
                          type="button"
                          onClick={() => setSelectedNode(rel.otherNode!)}
                          className="font-medium text-foreground hover:text-primary truncate max-w-[115px] text-right"
                          title={rel.otherNode.name}
                        >
                          {rel.otherNode.name}
                        </button>
                      ) : (
                        <span className="font-medium text-muted-foreground truncate max-w-[115px]">—</span>
                      )}
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
