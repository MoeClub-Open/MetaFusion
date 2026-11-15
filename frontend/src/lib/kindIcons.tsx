import React from "react";
import { Layers, Disc, Users, Network, BookOpen, Film, Calendar } from "lucide-react";

// 实体骨架 kind 的图标映射：卡片兜底图标、列表徽标与搜索联想共用同一份。
// 新增 kind 取默认图标即可，不会因为这里缺项而消失。
const KIND_ICONS: Record<string, React.ElementType> = {
  work: Layers,
  release: Disc,
  agent: Users,
  collection: Network,
  event: Calendar,
  content_unit: BookOpen,
  expression: Film,
  medium: Disc,
  track: Disc,
};

export function kindIcon(kind: string): React.ElementType {
  return KIND_ICONS[kind] || Layers;
}
