"use client";
import React, { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";

// 可聚焦元素：与浏览器 Tab 顺序一致，用于最小实现的焦点陷阱。
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

// 同时打开的对话框栈：Esc 只关栈顶，滚动锁只由"第一个打开/最后一个关闭"成对增删，
// 否则嵌套对话框关掉内层会把外层还开着的滚动锁一起解开。
const modalStack: HTMLElement[] = [];
let savedBodyOverflow = "";

export function Modal({
 open,
 onClose,
 title,
 icon,
 children,
 maxWidth = "max-w-lg",
 initialFocus,
 footer,
}: {
 open: boolean;
 onClose: () => void;
 title: React.ReactNode;
 icon?: React.ReactNode;
 children: React.ReactNode;
 maxWidth?: string;
 /** 打开后优先聚焦的面板内元素，例如搜索输入框。 */
 initialFocus?: string;
 /** 固定操作区；提供时只让内容区滚动，长表单仍能访问底部操作。 */
 footer?: React.ReactNode;
}) {
 const { t } = useI18n();
 const titleId = useId();
 const panelRef = useRef<HTMLDivElement>(null);
 const [portalTarget, setPortalTarget] = useState<HTMLElement | null>(null);
 useEffect(() => { setPortalTarget(document.body); }, []);
 // 关闭后把焦点还给打开它的按钮：读屏与键盘用户不会"掉"到页面顶部。
 const restoreRef = useRef<HTMLElement | null>(null);
 const onCloseRef = useRef(onClose);
 useEffect(() => { onCloseRef.current = onClose; }, [onClose]);

 useEffect(() => {
  if (!open || !portalTarget) return;
  const panel = panelRef.current;
  if (!panel) return;
  restoreRef.current = (document.activeElement as HTMLElement | null) ?? null;
  modalStack.push(panel);

  if (modalStack.length === 1) {
   savedBodyOverflow = document.body.style.overflow;
   document.body.style.overflow = "hidden";
  }

  const focusable = () => Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((item) => !item.matches(":disabled") && item.getClientRects().length > 0);
  (initialFocus ? panel.querySelector<HTMLElement>(initialFocus) : null)?.focus();
  if (!panel.contains(document.activeElement)) (focusable()[0] ?? panel).focus();

  function onKeyDown(e: KeyboardEvent) {
   if (e.defaultPrevented) return;
   if (modalStack[modalStack.length - 1] !== panel) return;
   if (e.key === "Escape") {
    e.preventDefault();
    onCloseRef.current();
    return;
   }
   if (e.key !== "Tab") return;
   const items = focusable();
   if (items.length === 0) {
    e.preventDefault();
    panel.focus();
    return;
   }
   const first = items[0];
   const last = items[items.length - 1];
   const active = document.activeElement;
   if (e.shiftKey && (active === first || active === panel || !panel.contains(active))) {
    e.preventDefault();
    last.focus();
   } else if (!e.shiftKey && active === last) {
    e.preventDefault();
    first.focus();
   }
  }

  document.addEventListener("keydown", onKeyDown);
  return () => {
   document.removeEventListener("keydown", onKeyDown);
   const index = modalStack.indexOf(panel);
   if (index >= 0) modalStack.splice(index, 1);
   if (modalStack.length === 0) document.body.style.overflow = savedBodyOverflow;
   const restore = restoreRef.current;
   if (restore && restore.isConnected) restore.focus();
  };
 }, [open, initialFocus, portalTarget]);

 if (!open || !portalTarget) return null;
 // Escape page stacking contexts so sticky navigation cannot cover the dialog.
 return createPortal(
 <div className="fixed inset-0 z-50 bg-black/70 backdrop-blur-sm flex items-center justify-center p-4" onClick={onClose}>
 <div
 ref={panelRef}
 role="dialog"
 aria-modal="true"
 aria-labelledby={titleId}
 tabIndex={-1}
 onClick={(e) => e.stopPropagation()}
 className={`w-full min-w-0 ${maxWidth} rounded-panel border border-line bg-surface p-5 sm:p-6 space-y-4 shadow-elevated max-h-[90dvh] ${footer ? "flex flex-col overflow-hidden" : "overflow-y-auto"} outline-none`}
 >
 <div className="flex shrink-0 items-center justify-between gap-2 border-b border-line-subtle pb-3">
 <h3 id={titleId} className="min-w-0 text-sm font-semibold text-text-strong flex items-center gap-2 break-words">
 {icon}
 {title}
 </h3>
 <button type="button" onClick={onClose} aria-label={t("revisions.close")} className="shrink-0 text-text-muted hover:text-gray-900 dark:hover:text-white p-2 rounded-md hover:bg-black/5 dark:hover:bg-white/5 transition-colors duration-fast ease-soft w-9 h-9 max-sm:min-h-[44px] grid place-items-center cursor-pointer">
 <X className="w-4 h-4" aria-hidden="true" />
 </button>
 </div>
 {footer ? <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">{children}</div> : children}
 {footer && <div className="shrink-0 border-t border-line-subtle pt-3">{footer}</div>}
 </div>
 </div>, portalTarget
 );
}
