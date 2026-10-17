"use client";

import { useState, useRef, useEffect } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { Languages, Check } from "lucide-react";
import type { Locale } from "@/i18n/routing";
import { uiLanguages } from "@/lib/languages";

// 界面语言候选来自语言单一来源（与 i18n/routing 的 locales 同源），
// 名称用语言表的自称，不再在组件里另抄一份语种清单。
export const LOCALES: { id: Locale; name: string; nativeName: string }[] = uiLanguages().map((l) => ({
 id: l.code as Locale,
 name: l.en,
 nativeName: l.native,
}));

export function LocaleSwitcher({ compact }: { compact?: boolean }) {
 const { locale, setLocale, t } = useI18n();
 const [isOpen, setIsOpen] = useState(false);
 const containerRef = useRef<HTMLDivElement>(null);

 useEffect(() => {
 function handleClickOutside(event: MouseEvent) {
 if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
 setIsOpen(false);
 }
 }
 if (isOpen) {
 document.addEventListener("mousedown", handleClickOutside);
 }
 return () => {
 document.removeEventListener("mousedown", handleClickOutside);
 };
 }, [isOpen]);

 return (
 <div className="relative" ref={containerRef}>
 <button
 type="button"
 onClick={() => setIsOpen(!isOpen)}
 title={t("locale.switchTitle")}
        aria-label={t("locale.switchTitle")}
 className={
 compact
 ? "w-9 h-9 max-sm:min-h-[44px] grid place-items-center rounded-control bg-black/5 dark:bg-white/[0.04] hover:bg-black/10 dark:hover:bg-white/[0.08] border border-line text-text-body transition-colors duration-fast ease-soft"
 : "inline-flex items-center gap-2 h-10 max-sm:min-h-[44px] px-3 rounded-control bg-black/5 dark:bg-white/[0.04] hover:bg-black/10 dark:hover:bg-white/[0.08] border border-line text-sm font-mono text-text-body transition-colors duration-fast ease-soft"
 }
 >
 <Languages className="w-4 h-4" strokeWidth={1.6} />
 {!compact && <span>{t("locale.currentLabel")}</span>}
 </button>

 {isOpen && (
 <div
 onClick={(e) => e.stopPropagation()}
 className="absolute right-0 mt-2 w-48 rounded-panel border border-line bg-surface shadow-elevated p-1.5 z-50 animate-slide-up text-sm"
 >
 <div className="px-3.5 py-2 font-mono text-xs tracking-[0.14em] text-text-muted border-b border-line-subtle mb-1">
 {t("locale.languageChoice")}
 </div>
 <div className="space-y-0.5">
 {LOCALES.map((l) => {
 const active = locale === l.id;
 return (
 <button
 key={l.id}
 type="button"
 onClick={(e) => {
 e.stopPropagation();
 setLocale(l.id);
 setIsOpen(false);
 }}
 className={`w-full px-3.5 py-2.5 rounded-lg flex items-center justify-between transition-colors duration-fast ease-soft cursor-pointer ${
 active
 ? "bg-primary text-white keep-white font-semibold shadow-soft"
 : "text-text-body hover:text-gray-900 dark:hover:text-white hover:bg-black/5 dark:hover:bg-surfaceHover"
 }`}
 >
 <span className="text-[13px]">{l.nativeName}</span>
 {active && <Check className="w-4 h-4 text-emphasis keep-white stroke-[2.2]" />}
 </button>
 );
 })}
 </div>
 </div>
 )}
 </div>
 );
}
