"use client";

import { useCallback, useEffect } from "react";
import { ArrowLeft, ArrowRight, ExternalLink } from "lucide-react";
import { AdaptiveCover } from "@/components/common/AdaptiveCover";
import { Modal } from "@/components/ui/Modal";
import { useI18n } from "@/i18n/I18nProvider";

/**
 * 详情页画廊的灯箱：一张图的完整读数（大图 + 图注 + 用途 + 来源），不跳走。
 *
 * 为什么自实现而不是再引一个灯箱库：这里要的只是"覆盖层 + 左右翻图 + Esc 关掉"，
 * 现成的 `Modal` 已经把焦点陷阱、滚动锁、Esc 与栈顶判定处理干净了，剩下的都是排版。
 * 引库反而多一套焦点行为要对齐（旧行为是点图跳外部 URL，来源链接不能丢，所以灯箱里
 * 必须留得出站入口）。
 */
export interface LightboxPicture {
  /** 图片地址：自托管封面是 /api/storage/assets/<uuid>/content，取不到对象时由
   *  AdaptiveCover 退化成中性封面占位而不是画破图。 */
  url: string;
  /** 图注：调用方按当前语种解析好（词表名与图注都要 definitions/locale），本组件不取数。 */
  caption: string;
  /** 用途标签（picture_role 词表的本地化名）；未声明用途时留空，不显示占位。 */
  roleLabel?: string;
  /** 这张图自身的时间元信息（拍摄/发布/改版）。只展示，**不参与排序**。 */
  takenAt?: string;
  versionLabel?: string;
  inUseFrom?: string;
  inUseUntil?: string;
  /** 首张即封面（顺序契约见 lib/cover.ts 的 coverPicture）。 */
  isCover?: boolean;
  /** 来源声明：灯箱负责把它显示出来，热链来源不再是"点图才看得到"。 */
  source?: { kind?: string | null; citation?: string | null; url?: string | null };
}

export function PictureLightbox({
  pictures,
  index,
  entityId,
  onIndexChange,
  onClose,
}: {
  pictures: LightboxPicture[];
  /** null = 关闭；越界按关闭处理（画廊数据在编辑后可能整体变短）。 */
  index: number | null;
  entityId?: string;
  onIndexChange: (next: number) => void;
  /** 必须传稳定引用：Modal 的副作用依赖它，换了身份就会在每次翻图时重挂并抢焦点。 */
  onClose: () => void;
}) {
  const { t } = useI18n();
  const total = pictures.length;
  const open = index !== null && index >= 0 && index < total;
  const current = open ? pictures[index as number] : null;

  // 翻图到两端就回绕：灯箱里"再按一次 → 回到第一张"比停在末端更省事，
  // 位置计数（3 / 12）始终写明当前在哪一张，不会绕得没有方向感。
  const step = useCallback(
    (delta: number) => {
      if (index === null || total < 2) return;
      onIndexChange(((index + delta) % total + total) % total);
    },
    [index, total, onIndexChange],
  );

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowRight") {
        e.preventDefault();
        step(1);
      } else if (e.key === "ArrowLeft") {
        e.preventDefault();
        step(-1);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, step]);

  if (!current) return null;
  const heading = current.caption || t("catalog.pictureGallery");

  return (
    <Modal open onClose={onClose} title={heading} maxWidth="max-w-3xl">
      <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-1.5">
            {current.isCover && (
              <span className="px-2 py-0.5 rounded-md bg-primary/15 text-primary font-mono text-[10px] font-semibold tracking-wide">
                {t("catalog.pictureCoverBadge")}
              </span>
            )}
            {current.roleLabel && (
              <span className="px-2 py-0.5 rounded-md bg-black/[0.05] dark:bg-white/[0.08] text-text-body font-mono text-[10px]">
                {current.roleLabel}
              </span>
            )}
          </div>
          {total > 1 && (
            <span className="font-mono text-[10px] text-text-faint">
              {`${(index as number) + 1} / ${total}`}
            </span>
          )}
        </div>

        {/* key 用地址：换图时整块重挂，AdaptiveCover 才不会沿用上一张的比例。 */}
        <AdaptiveCover
          key={current.url}
          src={current.url}
          alt={heading}
          title={heading}
          id={entityId}
          maxHeight="60vh"
          loading="eager"
        />

        {current.caption && (
          <p className="text-sm text-text-strong leading-relaxed">{current.caption}</p>
        )}

        {current.versionLabel && <p className="text-xs text-text-body">{current.versionLabel}</p>}
        {(current.inUseFrom || current.inUseUntil) && (
          <p className="font-mono text-[11px] text-text-muted">
            {t("catalog.picturePeriod", { from: current.inUseFrom || "?", until: current.inUseUntil || "?" })}
          </p>
        )}

        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[11px] text-text-muted">
          {current.takenAt && <span>{t("catalog.pictureTakenAtValue", { date: current.takenAt })}</span>}
          {String(current.source?.citation || "").trim() && (
            <span className="min-w-0 break-words">
              {t("catalog.pictureSourceLine", {
                citation: String(current.source?.citation).trim(),
              })}
            </span>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {String(current.source?.url || "").trim() && (
            <a
              href={String(current.source?.url).trim()}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1.5 px-3 h-8 rounded-lg border border-line text-xs text-text-body hover:border-primary/60 transition-colors duration-fast ease-soft"
            >
              <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
              {t("catalog.pictureSourceLink")}
            </a>
          )}
          {current.url && (
            <a
              href={current.url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1.5 px-3 h-8 rounded-lg border border-line text-xs text-text-body hover:border-primary/60 transition-colors duration-fast ease-soft"
            >
              <ExternalLink className="w-3.5 h-3.5" aria-hidden="true" />
              {t("catalog.pictureOpenFile")}
            </a>
          )}
        </div>

        {total > 1 && (
          <div className="flex items-center justify-between pt-1 border-t border-line-subtle">
            <button
              type="button"
              onClick={() => step(-1)}
              className="inline-flex items-center gap-1.5 px-3 h-8 rounded-lg border border-line text-xs text-text-body hover:border-primary/60 transition-colors duration-fast ease-soft cursor-pointer"
            >
              <ArrowLeft className="w-3.5 h-3.5" aria-hidden="true" />
              {t("catalog.picturePrevious")}
            </button>
            <button
              type="button"
              onClick={() => step(1)}
              className="inline-flex items-center gap-1.5 px-3 h-8 rounded-lg border border-line text-xs text-text-body hover:border-primary/60 transition-colors duration-fast ease-soft cursor-pointer"
            >
              {t("catalog.pictureNext")}
              <ArrowRight className="w-3.5 h-3.5" aria-hidden="true" />
            </button>
          </div>
        )}
      </div>
    </Modal>
  );
}
