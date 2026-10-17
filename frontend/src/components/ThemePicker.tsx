"use client";

// 调色板管理配色、表面色调与背景图案，显示模式使用独立入口。
import { Palette } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { ThemeControls } from "./ThemeControls";
import { ThemePopover } from "./ThemePopover";

export function ThemePicker({ withinDialog = false }: { withinDialog?: boolean }) {
  const { t } = useI18n();
  return <ThemePopover label={t("theme.palette")} icon={<Palette className="h-4 w-4 text-primary" aria-hidden="true" />} withinDialog={withinDialog}>
    {() => <ThemeControls layout="compact" />}
  </ThemePopover>;
}
