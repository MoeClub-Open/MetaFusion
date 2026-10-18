"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Navbar } from "@/components/Navbar";
import { PageShell } from "@/components/ui/PageShell";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import {
  catalogEntityHref,
  emitNotificationsChanged,
  fetchNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  NOTIFICATION_PAGE_SIZE_DEFAULT,
} from "@/lib/api";
import type { Notification, NotificationListResponse } from "@/lib/api";
import { isoTimestamp, localDateTime } from "@/lib/datetime";
import { getAuthLoginUrl } from "@/lib/services";
import {
  AlertCircle,
  Bell,
  CheckCheck,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Inbox,
  Loader2,
  MessageSquare,
  PackagePlus,
  UploadCloud,
  XCircle,
} from "lucide-react";

/**
 * 类型 → 图标与文案键。服务端将来新增类型时这里不认识，
 * 按 unknown 渲染（图标用铃铛、文案走 notifications.type.unknown），不崩也不显示裸 key。
 */
const TYPE_ICON: Record<string, typeof Bell> = {
  "comment.replied": MessageSquare,
  "entity.included": PackagePlus,
  "entity.review_approved": CheckCircle2,
  "entity.review_rejected": XCircle,
  "import.completed": UploadCloud,
};

const TYPE_LABEL_KEY: Record<string, string> = {
  "comment.replied": "notifications.type.comment_replied",
  "entity.included": "notifications.type.entity_included",
  "entity.review_approved": "notifications.type.review_approved",
  "entity.review_rejected": "notifications.type.review_rejected",
  "import.completed": "notifications.type.import_completed",
};

type Translate = (key: string, vars?: Record<string, string | number>) => string;

/** payload 是键值袋，缺键、换型都按"没有这个值"处理（不渲染空行）。 */
function payloadText(payload: Record<string, unknown>, key: string): string {
  const value = payload[key];
  return typeof value === "string" ? value.trim() : "";
}

function payloadNumberText(payload: Record<string, unknown>, key: string): string {
  const value = payload[key];
  return typeof value === "number" && Number.isFinite(value) ? String(value) : "";
}

/**
 * 跳转映射（契约里 payload 可能缺键，缺了就退回列表页/通用条目页，不拼一个必然 404 的地址）：
 *   comment.replied  → /community/<topic_id>，缺 topic_id 时 /community
 *   entity.*         → catalogEntityHref(entity_id)；详情地址只由 id 决定（kind 不参与 URL 命名）
 */
function notificationHref(n: Notification): string | null {
  const payload = n.payload;
  switch (n.type) {
    case "comment.replied": {
      const topicId = payloadText(payload, "topic_id");
      if (topicId) return `/community/${encodeURIComponent(topicId)}`;
      // 实体短评没有主题可跳（短评是 community.topics 里锚定条目的行，不是论坛主题）：
      // 落到被评论的条目页，服务端会给 entity_kind；连 kind 都没有时才退回评论流首页。
      const entityId = payloadText(payload, "entity_id");
      if (entityId) return catalogEntityHref(entityId);
      return "/community";
    }
    case "entity.included":
    case "entity.review_approved":
    case "entity.review_rejected": {
      if (n.subject_type === "pull_request") return `/pull-requests/${encodeURIComponent(n.subject_id)}`;
      if (n.subject_type === "commit") return `/commits/${encodeURIComponent(n.subject_id)}`;
      const entityId = payloadText(payload, "entity_id") || (n.subject_type === "entity" ? n.subject_id : "");
      if (!entityId) return null;
      return catalogEntityHref(entityId);
    }
    case "import.completed":
      return "/contribute";
    default:
      return null;
  }
}

/** 每行下方的上下文（label/value 成对），缺键的行直接不出现。 */
function notificationRows(n: Notification, t: Translate): { label: string; value: string }[] {
  const payload = n.payload;
  const rows: { label: string; value: string }[] = [];
  const actor = n.actor_name.trim();
  if (actor) rows.push({ label: t("notifications.field.actor"), value: actor });

  switch (n.type) {
    case "comment.replied": {
      const topic = payloadText(payload, "topic_title") || payloadText(payload, "topic_id");
      if (topic) rows.push({ label: t("notifications.field.topic"), value: topic });
      const post = payloadNumberText(payload, "post_number");
      if (post) rows.push({ label: t("notifications.field.postNumber"), value: post });
      const entity = payloadText(payload, "entity_title");
      if (entity) rows.push({ label: t("notifications.field.entity"), value: entity });
      break;
    }
    case "entity.included": {
      const entity = payloadText(payload, "entity_title");
      if (entity) rows.push({ label: t("notifications.field.entity"), value: entity });
      const container = payloadText(payload, "container_title");
      if (container) rows.push({ label: t("notifications.field.container"), value: container });
      const role = payloadText(payload, "role");
      if (role) rows.push({ label: t("notifications.field.role"), value: role });
      break;
    }
    case "entity.review_approved":
    case "entity.review_rejected": {
      const entity = payloadText(payload, "entity_title") || payloadText(payload,"pull_request_title");
      if (entity) rows.push({ label: t(n.subject_type === "pull_request" ? "notifications.field.pullRequest" : "notifications.field.entity"), value: entity });
      const version = payloadNumberText(payload, "version");
      if (version) rows.push({ label: t("notifications.field.version"), value: version });
      break;
    }
    case "import.completed": {
      const source = payloadText(payload, "source");
      if (source) rows.push({ label: t("notifications.field.source"), value: source });
      const status = payloadText(payload, "status");
      if (status) rows.push({ label: t("notifications.field.status"), value: status });
      // 仅渲染退役前已保存的历史通知；服务端不再生成此事件。
      // entities / relations 直接读取历史载荷，不重新计算或推导新建数。
      const entities = payloadNumberText(payload, "entities");
      if (entities) rows.push({ label: t("notifications.field.entities"), value: entities });
      const relations = payloadNumberText(payload, "relations");
      if (relations) rows.push({ label: t("notifications.field.relations"), value: relations });
      break;
    }
    default:
      break;
  }
  return rows;
}

export default function NotificationsPage() {
  const {user,loading} = useAuth();
  const {t} = useI18n();
  return <><Navbar /><PageShell title={t("notifications.title")} icon={<Bell className="size-5 text-primary" />} spacing="compact">
    {loading ? <p role="status">{t("common.loading")}</p> : user ? <NotificationInbox key={user.id} /> : <a href={getAuthLoginUrl("/notifications")} className="text-sm text-primary">{t("notifications.goLogin")}</a>}
  </PageShell></>;
}

function NotificationInbox() {
  const {t,locale} = useI18n();
  const [category,setCategory] = useState("");
  const [read,setRead] = useState("");
  const [dates,setDates] = useState({from:"",to:""});
  const [draft,setDraft] = useState({from:"",to:""});
  const [offset,setOffset] = useState(0);
  const [data,setData] = useState<NotificationListResponse|null>(null);
  const [loading,setLoading] = useState(true);
  const [failed,setFailed] = useState(false);
  const [actionFailed,setActionFailed] = useState(false);
  const [busy,setBusy] = useState(false);
  const [reload,setReload] = useState(0);
  useEffect(()=>{
    const controller = new AbortController();
    setLoading(true);setFailed(false);setData(null);
    fetchNotifications({category,read,...dates,offset,limit:NOTIFICATION_PAGE_SIZE_DEFAULT},controller.signal).then(result=>{
      if(controller.signal.aborted)return;
      if(offset>0 && offset>=result.total){setOffset(Math.max(0,Math.ceil(result.total/NOTIFICATION_PAGE_SIZE_DEFAULT)-1)*NOTIFICATION_PAGE_SIZE_DEFAULT);return;}
      setData(result);
    }).catch(()=>{if(!controller.signal.aborted)setFailed(true);}).finally(()=>{if(!controller.signal.aborted)setLoading(false);});
    return ()=>controller.abort();
  },[category,read,dates,offset,reload]);
  async function mark(id?:string){
    if(busy)return;
    setBusy(true);setActionFailed(false);
    try { if(id)await markNotificationRead(id);else await markAllNotificationsRead();emitNotificationsChanged();setReload(n=>n+1); }
    catch {setActionFailed(true);} finally{setBusy(false);}
  }
  const selectClass="h-8 rounded-control border border-line bg-surface px-2 text-xs text-text-body";
  return <div className="grid items-start gap-5 md:grid-cols-[11rem_minmax(0,1fr)]">
    <nav aria-label={t("notifications.category")} className="flex gap-1 overflow-x-auto md:sticky md:top-20 md:flex-col">
      {["","discussion","catalog","review","system"].map(value=><button type="button" key={value} onClick={()=>{setCategory(value);setOffset(0);}} aria-current={category===value?"page":undefined} className={`flex shrink-0 items-center justify-between gap-2 rounded-control px-3 py-2 text-left text-sm ${category===value?"bg-primary/10 font-medium text-primary":"text-text-muted hover:bg-surfaceSubtle"}`}>
        {t(`notifications.category.${value||"all"}`)}{!value && data && data.unread>0 && <span className="rounded-chip bg-primary/10 px-1.5 text-xs">{data.unread}</span>}
      </button>)}
    </nav>
    <div className="min-w-0 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line-subtle pb-3">
        <div className="flex items-center gap-1" role="group" aria-label={t("notifications.readFilter")}>
          {["","unread","read"].map(value=><button key={value} type="button" aria-pressed={read===value} onClick={()=>{setRead(value);setOffset(0);}} className={`rounded-control px-3 py-1.5 text-xs ${read===value?"bg-primary/10 text-primary":"text-text-muted hover:bg-surfaceSubtle"}`}>{t(`notifications.read.${value||"all"}`)}</button>)}
        </div>
        <button type="button" onClick={()=>void mark()} disabled={busy||!data?.unread} className="inline-flex items-center gap-1.5 rounded-control border border-line px-2.5 py-1.5 text-xs text-text-muted disabled:opacity-40"><CheckCheck className="size-3.5" />{t("notifications.markAllRead")}</button>
      </div>
      <form className="flex flex-wrap items-end gap-2" onSubmit={event=>{event.preventDefault();if(!draft.from||!draft.to||draft.from<=draft.to){setDates(draft);setOffset(0);}}}>
        {(["from","to"] as const).map(field=><label key={field} className="space-y-1 text-xs text-text-muted"><span className="block">{t(`accountUI.${field}`)} (UTC)</span><input aria-label={t(`accountUI.${field}`)} type="date" className={selectClass} value={draft[field]} onChange={event=>setDraft({...draft,[field]:event.target.value})} max={field==="from"?draft.to||undefined:undefined} min={field==="to"?draft.from||undefined:undefined}/></label>)}
        <button className={selectClass} type="submit">{t("accountUI.filter")}</button>
        {(dates.from||dates.to) && <button className="px-2 py-1 text-xs text-text-muted" type="button" onClick={()=>{setDraft({from:"",to:""});setDates({from:"",to:""});setOffset(0);}}>{t("accountUI.reset")}</button>}
      </form>
      {(failed||actionFailed) && <div role="alert" className="flex items-center gap-2 rounded-control border border-danger/20 p-3 text-sm text-danger"><AlertCircle className="size-4"/><span className="flex-1">{t(failed?"notifications.error":"notifications.actionFailed")}</span><button type="button" onClick={()=>setReload(n=>n+1)}>{t("common.retry")}</button></div>}
      {loading ? <div role="status" className="flex justify-center gap-2 py-12 text-sm text-text-muted"><Loader2 className="size-4 animate-spin"/>{t("common.loading")}</div> : !failed && !data?.items.length ? <div className="rounded-control border border-line-subtle py-12 text-center text-sm text-text-muted"><Inbox className="mx-auto mb-3 size-6"/>{t("notifications.empty")}</div> : null}
      {!!data?.items.length && <ul className="divide-y divide-line-subtle overflow-hidden rounded-control border border-line bg-surface">
        {data.items.map(n=>{
          const Icon=TYPE_ICON[n.type]||Bell;
          const label=t(n.subject_type==="pull_request" && n.type==="entity.review_approved"?"notifications.type.prMerged":n.subject_type==="pull_request"&&n.type==="entity.review_rejected"?"notifications.type.prClosed":TYPE_LABEL_KEY[n.type]||"notifications.type.unknown");
          const href=notificationHref(n);
          const context=notificationRows(n,t).filter(row=>![t("notifications.field.actor"),t("notifications.field.role"),t("notifications.field.version")].includes(row.label));
          return <li key={n.id} className={`flex gap-3 px-3 py-3 sm:px-4 ${n.read?"":"bg-primary/[0.04]"}`}>
            <Icon className={`mt-0.5 size-4 shrink-0 ${n.read?"text-text-faint":"text-primary"}`}/>
            <div className="min-w-0 flex-1 space-y-1">
              <div className="flex flex-wrap items-center gap-2"><span className={`text-sm ${n.read?"text-text-body":"font-medium text-text-strong"}`}>{label}</span>{!n.read&&<span className="size-1.5 rounded-full bg-primary" aria-label={t("notifications.read.unread")}/>} {n.count>1&&<span className="text-xs text-text-muted">×{n.count}</span>}<time className="ml-auto text-xs text-text-faint" dateTime={n.updated_at} title={localDateTime(n.updated_at,locale)}>{isoTimestamp(n.updated_at)}</time></div>
              {context.length>0&&<p className="line-clamp-2 break-words text-sm text-text-body">{context.map(row=>row.value).join(" · ")}</p>}
              <div className="flex items-center gap-3 text-xs text-text-muted">{n.actor_name&&<span>{n.actor_name}</span>}{href&&<Link className="text-primary hover:underline" href={href} onClick={()=>{if(!n.read)void mark(n.id);}}>{t("notifications.open")}</Link>}{!n.read&&<button type="button" disabled={busy} className="ml-auto hover:text-primary disabled:opacity-40" onClick={()=>void mark(n.id)}>{t("notifications.markRead")}</button>}</div>
            </div>
          </li>;
        })}
      </ul>}
      {data&&data.total>NOTIFICATION_PAGE_SIZE_DEFAULT&&<nav className="flex items-center justify-between text-xs text-text-muted"><span>{t("accountUI.total",{count:data.total})}</span><div className="flex items-center gap-2"><button type="button" disabled={offset===0||loading} onClick={()=>setOffset(n=>Math.max(0,n-NOTIFICATION_PAGE_SIZE_DEFAULT))} aria-label={t("users.profile.prevPage")} className="rounded-control border border-line p-2 disabled:opacity-40"><ChevronLeft className="size-3.5"/></button><span>{Math.floor(offset/NOTIFICATION_PAGE_SIZE_DEFAULT)+1} / {Math.ceil(data.total/NOTIFICATION_PAGE_SIZE_DEFAULT)}</span><button type="button" disabled={offset+NOTIFICATION_PAGE_SIZE_DEFAULT>=data.total||loading} onClick={()=>setOffset(n=>n+NOTIFICATION_PAGE_SIZE_DEFAULT)} aria-label={t("users.profile.nextPage")} className="rounded-control border border-line p-2 disabled:opacity-40"><ChevronRight className="size-3.5"/></button></div></nav>}
    </div>
  </div>;
}
