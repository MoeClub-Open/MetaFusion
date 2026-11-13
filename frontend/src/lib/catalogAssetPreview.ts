export type CatalogAssetContext = {
  kind: "workspace" | "pull-request" | "commit";
  id: string;
  allowedAssetIds?: ReadonlySet<string>;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const inlineStoragePath = /^\/api\/storage\/assets\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/content$/;
const bodyStorageRegex = /(?:!\[(?:[^\[\]\\]|\\.|\[(?:[^\[\]\\]|\\.)*\])*\]\s*\(\s*<?|(?:^|\n)[ \t]*\[[^\]\n]+\]:\s*<?)\/api\/storage\/assets\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/content(?=[\s>)"]|$)/g;

function contextCollection(kind: CatalogAssetContext["kind"]): string {
  return kind === "workspace" ? "workspaces" : kind === "pull-request" ? "pull-requests" : "commits";
}

/** Project the image URL for a private view; never write this URL into a catalog document. */
export function catalogPicturePreviewUrl(picture: { url?: unknown; asset_id?: unknown }, context?: CatalogAssetContext): string {
  const url = typeof picture.url === "string" ? picture.url : "";
  const assetID = typeof picture.asset_id === "string" ? picture.asset_id : "";
  if (!context || !uuid.test(context.id) || !uuid.test(assetID)) return url;
  if (context.allowedAssetIds && !context.allowedAssetIds.has(assetID)) return url;
  // Only canonical self-hosted asset references are projected. External image
  // URLs and malformed pairs retain the normal image URL validation.
  try {
    const parsed = new URL(url, "https://catalog.invalid");
    if (parsed.pathname !== `/api/storage/assets/${assetID}/content` || parsed.search || parsed.hash || parsed.username || parsed.password) return url;
  } catch { return url; }
  return `/api/catalog/${contextCollection(context.kind)}/${context.id}/assets/${assetID}/content`;
}

/** Project a canonical inline storage image URL for private workspace/PR/commit views. */
export function catalogInlineAssetPreviewUrl(url: string, context?: CatalogAssetContext): string {
  if (typeof url !== "string" || !url.startsWith("/api/storage/assets/") || !context || !uuid.test(context.id)) return url;
  const match = inlineStoragePath.exec(url);
  if (!match) return url;
  const assetID = match[1];
  if (context.allowedAssetIds && !context.allowedAssetIds.has(assetID)) return url;
  return `/api/catalog/${contextCollection(context.kind)}/${context.id}/assets/${assetID}/content`;
}

/** Collect canonical self-hosted asset UUIDs referenced by an entity snapshot's pictures or article bodies. */
export function extractDocumentAssetIds(doc: unknown, into: Set<string> = new Set()): Set<string> {
  if (!doc || typeof doc !== "object") return into;
  const record = doc as { pictures?: unknown; translations?: unknown };
  if (Array.isArray(record.pictures)) {
    for (const p of record.pictures) {
      if (!p || typeof p !== "object") continue;
      const assetID = typeof (p as { asset_id?: unknown }).asset_id === "string" ? (p as { asset_id: string }).asset_id : "";
      const url = typeof (p as { url?: unknown }).url === "string" ? (p as { url: string }).url : "";
      if (uuid.test(assetID) && url === `/api/storage/assets/${assetID}/content`) {
        into.add(assetID);
      }
    }
  }
  if (record.translations && typeof record.translations === "object" && !Array.isArray(record.translations)) {
    for (const tr of Object.values(record.translations as Record<string, unknown>)) {
      const body = tr && typeof tr === "object" && typeof (tr as { body?: unknown }).body === "string"
        ? (tr as { body: string }).body
        : "";
      if (!body.includes("/api/storage/assets/")) continue;
      bodyStorageRegex.lastIndex = 0;
      let match: RegExpExecArray | null;
      while ((match = bodyStorageRegex.exec(body)) !== null) {
        into.add(match[1]);
      }
    }
  }
  return into;
}
