// 导出当前可见条目的快照；修改使用目录提交。

import { ApiError, fetchApi } from "./client";
import { localizeCatalogError } from "@/lib/catalogErrors";

import type { Entity } from "@/components/catalog/api";

/** Export the visible canonical entity with the current catalog schema. */
export function fetchExchangeEntity(id: string): Promise<Entity> {
  return fetchApi<Entity>(`/exchange/entities/${encodeURIComponent(id)}`);
}

// 翻译函数形状只有一份定义（i18n/I18nProvider）：这里只做类型再导出，避免各模块各写一遍。
import type { TranslateFn } from "@/i18n/I18nProvider";
export type { TranslateFn };

const EXCHANGE_ERROR_KEYS: Record<string, string> = {
  forbidden: "admin.exchange.errForbidden",
  not_found: "admin.exchange.errNotFound",
  encode_failed: "admin.exchange.errEncode",
};

/**
 * 把交换端点的失败翻成如实的人话。
 *
 * 三层回退：交换专属码表 → 目录写入通用码表（unknown_field / four_locale_names_required /
 * translation_required 这类定义校验码已有四语文案，不重复造词）→ 未登记的码带状态码原样显示。
 * 绝不把失败说成成功，也不把裸码或空串吞掉。
 */
export function describeExchangeError(err: unknown, t: TranslateFn): string {
  if (err instanceof ApiError) {
    const message = String(err.message || "");
    // 目录服务的错误串可能带补充（unknown_field: duration），按冒号分段取第一个已登记的码。
    for (const segment of message.split(":")) {
      const key = EXCHANGE_ERROR_KEYS[segment.trim()];
      if (key) return t(key);
    }
    const localized = localizeCatalogError(message, t);
    if (localized !== message) return localized;
    return t("admin.exchange.errUnknown", {
      status: err.status,
      code: message !== "" ? message : `HTTP ${err.status}`,
    });
  }
  return t("admin.exchange.errNetwork", {
    message: err instanceof Error ? err.message : String(err),
  });
}
