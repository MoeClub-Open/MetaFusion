import { fetchApi } from "./client";
import type { Entity } from "@/components/catalog/api";
import { identityCandidateQuery, readIdentityCandidates, type IdentityReviewData } from "../identityReview";

export async function fetchIdentityCandidates(entity: Entity): Promise<IdentityReviewData> {
  const response = await fetchApi<unknown>("/catalog/entities/candidates", {
    method: "POST",
    body: JSON.stringify(identityCandidateQuery(entity)),
  });
  return readIdentityCandidates(response, entity);
}
