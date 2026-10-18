"use client";

import { useCallback } from "react";
import { markConversationRead } from "@/lib/api";
import { UserAvatar } from "@/components/UserAvatar";
import { useI18n } from "@/i18n/I18nProvider";
import { Clock, X } from "lucide-react";
import MessageThread, { MessageThreadPeer } from "./MessageThread";

interface DirectMessageModalProps {
  // 对方资料来自公开投影；头像按账号 ID 解析，时间信息仍为可选项。
  peerUser: MessageThreadPeer;
  isOpen: boolean;
  onClose: () => void;
}

/**
 * 用户主页的私信弹窗：外壳（定位、关闭、高度约束）留在这里，
 * 消息流与输入区复用 MessageThread（与 /messages 页共用同一份行为）。
 */
export default function DirectMessageModal({
  peerUser,
  isOpen,
  onClose,
}: DirectMessageModalProps) {
  const { t, locale } = useI18n();

  // 会话真的读到内容后再标记已读；失败静默——已读只是角标口径，
  // 取不到既不该打断阅读，也不该弹一条用户处理不了的错误。
  const handleRead = useCallback(() => {
    markConversationRead(peerUser.id).catch(() => {});
  }, [peerUser.id]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm animate-fade-in">
      <div
        className="w-full max-w-lg bg-surface border border-line rounded-panel shadow-2xl flex flex-col h-[600px] max-h-[90vh] overflow-hidden animate-scale-up"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="px-4 py-3 bg-surface/90 border-b border-line flex items-center justify-between shrink-0">
          <div className="flex items-center gap-3 min-w-0">
            <UserAvatar user={peerUser} size="md" shape="circle" ring />
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="font-semibold text-emphasis text-sm truncate">
                  {peerUser.username}
                </span>
              </div>
              {/* 注册时间只有在账号服务真的给了 created_at 时才显示；字段缺席就不渲染这一行，
                  不能把 undefined 送进 Date 造出 "Invalid Date"。 */}
              {peerUser.created_at && (
                <span className="text-[10px] text-text-muted font-mono flex items-center gap-1">
                  <Clock className="w-2.5 h-2.5" />
                  {t("users.profile.registeredAt")}:{" "}
                  {new Date(peerUser.created_at).toLocaleDateString(locale)}
                </span>
              )}
            </div>
          </div>

          <button
            onClick={onClose}
            className="p-1.5 rounded-full hover:bg-emphasis/10 text-text-muted hover:text-emphasis transition-colors duration-fast ease-soft"
            title={t("users.profile.closeChat")}
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* 换人时用 key 重挂载：新会话从第一页重新开始，不给上一个人的消息留残留状态。 */}
        <MessageThread key={peerUser.id} peerUser={peerUser} onRead={handleRead} />
      </div>
    </div>
  );
}
