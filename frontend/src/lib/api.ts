// 各服务的 API 客户端按域放在 ./api/*，这里汇总当前公共接口。

export { displayNameOf, ApiError, fetchApi } from "./api/client";
export type { User } from "./api/client";
export { fetchInviteLedger, createInviteCode, fetchSetupStatus, fetchAuthSettings, normalizeSessionUser } from "./api/auth";
export { fetchOAuthGrants, revokeOAuthGrant } from "./api/auth";
export { fetchPersonalAccessTokens, createPersonalAccessToken, revokePersonalAccessToken, updatePersonalAccessToken, deletePersonalAccessToken, updateOwnProfile } from "./api/auth";
export { fetchPrivacySettings, updatePrivacySettings } from "./api/auth";
export type { PrivacySettings } from "./api/auth";
export type { InviteCode, InviteLedger, SetupStatusResponse, PublicAuthSettings, AuthorizedApp, PersonalAccessToken, CreatedPersonalAccessToken } from "./api/auth";
export { toggleFavorite, fetchFavoriteStatus, fetchFavorites, normalizeBoard, boardDisplayName, boardDisplayDesc, ALL_FORUM_BOARD, fetchBoards, getBoardSync, shareContent, buildShareUrl, createTopic, createPost, setTopicPinned, fetchDirectMessages, sendDirectMessage, fetchConversations, fetchUnreadMessageCount, markConversationRead, fetchMessageSettings, updateMessageSettings, fetchEntityPosts, fetchEntityCollections, createEntityComment } from "./api/community";
export type { FavoriteTargetType, FavoriteItem, ForumPost, DiscussionTopic, CreateTopicPayload, CreatePostPayload, EntityComment, EntityCollectionRef, DirectMessage, ConversationItem, ForumBoard } from "./api/community";
export { pickLocalizedName, catalogEntityHref, mergeEntities } from "./api/catalog";
export type { Tag, ConnectedEntityItem, EntityRevision, EntityRelationship, RelationType, GraphNode, GraphLink } from "./api/catalog";
export { fetchUserProfile, fetchUserContributions, fetchUserCommunityStats } from "./api/users";
export type { PublicUser, PublicUserProfile, ContributionSummary, ContributionActor, ContributionEntity, ContributionDay, ContributionItem, UserContributions, CommunityUserStats } from "./api/users";
export { fetchOwnAuditLogs, AUDIT_PAGE_SIZE } from "./api/audit";
export type { AuditLogEntry, AuditLogPage } from "./api/audit";
export { fetchExternalDatabases, fetchAdminExternalDatabases, createExternalDatabase, updateExternalDatabase, deleteExternalDatabase } from "./api/admin";
export type { ExternalDatabaseDefinition, ExternalLinkDisplay } from "./api/admin";
export { fetchAdminRateLimits, saveAdminRateLimits, fetchCommunityAdminRateLimits, saveCommunityAdminRateLimits } from "./api/admin";
export type { RateLimitRule, RateLimitPolicy, RateLimitConfig, CommunityRateLimitConfig, MessageRateLimitPolicy } from "./api/admin";
export { fetchExternalSources } from "./api/externalSources";
export type { ExternalSource } from "./api/externalSources";
export { fetchNotifications, fetchUnreadCount, markNotificationRead, markAllNotificationsRead, emitNotificationsChanged, NOTIFICATIONS_CHANGED_EVENT, NOTIFICATION_PAGE_SIZE_DEFAULT } from "./api/notifications";
export type { Notification, NotificationType, NotificationSubjectType, NotificationListResponse, MarkNotificationReadResult, MarkAllNotificationsReadResult } from "./api/notifications";
