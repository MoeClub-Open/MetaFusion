"use client";

import { useEffect, useState } from "react";
import { ArrowUpRight, MessageSquare } from "lucide-react";
import { fetchEntityPosts, type EntityComment } from "@/lib/api";
import { getForumEntityUrl } from "@/lib/services";
import { useI18n } from "@/i18n/I18nProvider";
import { SectionTitle } from "@/components/ui/SectionTitle";

/** 只读评论列表（发行版本详情页用）：发表评论仍走论坛入口。 */
export function EntityCommentsPanel({ entityId, onCount }: { entityId: string; onCount?: (n: number) => void }) {
  const { t } = useI18n();
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [posts, setPosts] = useState<EntityComment[]>([]);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!entityId) return;
    let alive = true;
    setState("loading");
    fetchEntityPosts(entityId)
      .then((items) => {
        if (!alive) return;
        setPosts(items);
        setState("ready");
        onCount?.(items.length);
      })
      .catch(() => { if (alive) setState("error"); });
    return () => { alive = false; };
    // onCount 由父组件用 useCallback 固定；不放进依赖，避免父组件重渲染触发重复请求。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [entityId, attempt]);

  return (
    <section className="space-y-3">
      <SectionTitle
        icon={<MessageSquare className="w-4 h-4 text-primary" strokeWidth={1.5} />}
        actions={
          <a href={getForumEntityUrl(entityId)} target="_blank" rel="noopener noreferrer" className="text-xs text-primary hover:underline inline-flex items-center gap-1 font-medium">
            <span>{t("entity.page.openInForum")}</span>
            <ArrowUpRight className="w-3.5 h-3.5" />
          </a>
        }
      >
        {t("entity.page.communityTitleCount", { count: posts.length })}
      </SectionTitle>
      {state === "loading" ? (
        <p className="text-xs text-text-muted">{t("catalog.loading")}</p>
      ) : state === "error" ? (
        <p role="alert" className="text-xs text-text-muted">
          {t("catalog.listFailed")}{" "}
          <button type="button" className="text-primary hover:underline" onClick={() => setAttempt((n) => n + 1)}>{t("catalog.retry")}</button>
        </p>
      ) : posts.length === 0 ? (
        <p className="text-xs text-text-muted">{t("common.noData")}</p>
      ) : (
        <ul className="space-y-2">
          {posts.map((p) => (
            <li key={p.id} className="rounded-lg border border-line bg-surface p-3 space-y-1">
              <div className="flex items-center justify-between text-[11px] text-text-faint font-mono">
                <span className="text-text-body">@{p.author_name}</span>
                <time dateTime={p.created_at}>{p.created_at.slice(0, 10)}</time>
              </div>
              <p className="text-sm text-text-strong whitespace-pre-wrap break-words">{p.body}</p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
