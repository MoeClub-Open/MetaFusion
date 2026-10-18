export type CatalogAssetContext = { kind: "workspace" | "pull-request" | "commit"; id: string };

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** Project the image URL for a private view; never write this URL into a catalog document. */
export function catalogPicturePreviewUrl(picture: { url?: unknown; asset_id?: unknown }, context?: CatalogAssetContext): string {
  const url = typeof picture.url === "string" ? picture.url : "";
  const assetID = typeof picture.asset_id === "string" ? picture.asset_id : "";
  if (!context || !uuid.test(context.id) || !uuid.test(assetID)) return url;
  // Only canonical self-hosted asset references are projected. External image
  // URLs and malformed pairs retain the normal image URL validation.
  try {
    const parsed = new URL(url, "https://catalog.invalid");
    if (parsed.pathname !== `/api/storage/assets/${assetID}/content` || parsed.search || parsed.hash || parsed.username || parsed.password) return url;
  } catch { return url; }
  const collection = context.kind === "workspace" ? "workspaces" : context.kind === "pull-request" ? "pull-requests" : "commits";
  return `/api/catalog/${collection}/${context.id}/assets/${assetID}/content`;
}
