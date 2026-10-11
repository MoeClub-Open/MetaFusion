import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { I18nProvider } from "../src/i18n/I18nProvider";
import { emptyEntity } from "../src/components/catalog/api";
import { IdentityReviewPanel } from "../src/components/catalog/IdentityReviewPanel";
import { ApiError, fetchApi } from "../src/lib/api/client";
import { fetchIdentityCandidates } from "../src/lib/api/identity";
import {
  identityCandidateQuery, identityConflictIDs, identityReviewKey, readIdentityCandidates,
  reviewedIdentityIDs, type IdentityCandidateEntity, type IdentityReviewState,
} from "../src/lib/identityReview";

const ownID = "11111111-1111-4111-8111-111111111111";
const otherID = "22222222-2222-4222-8222-222222222222";
const aliasID = "33333333-3333-4333-8333-333333333333";
const scopeID = "44444444-4444-4444-8444-444444444444";
const candidate = (id = otherID): IdentityCandidateEntity => ({
  id, kind: "work", title: "Candidate title", status: "published", version: 3,
  translations: { ja: { title: "候補", aliases: ["別名"] } }, external_ids: { database: "123" }, attributes: {},
});
const entity = { ...emptyEntity(), id: ownID, title: "Candidate title" };
const response = (items: unknown[], complete = true, total = items.length) => ({ items, complete, total, basis: "postgres_repeatable_read" });
const match = (canonical = candidate(), matched = canonical) => ({ matched, canonical });

test("lookup criteria match server titles, external identifiers and kind-specific ownership", () => {
  const draft = { ...entity, translations: { ja: { title: "日本語", aliases: ["not a criterion"] }, en: { title: "English" } }, external_ids: { b: "2", a: "1" }, work_id: scopeID, release_id: scopeID, medium_id: scopeID };
  const query = identityCandidateQuery(draft);
  assert.deepEqual(query, { kind: "work", titles: ["Candidate title", "English", "日本語"], external_ids: [{ provider: "a", value: "1" }, { provider: "b", value: "2" }] });
  for (const kind of ["expression", "content_unit"]) assert.equal(identityCandidateQuery({ ...draft, kind }).work_id, scopeID);
  assert.equal(identityCandidateQuery({ ...draft, kind: "medium" }).release_id, scopeID);
  assert.equal(identityCandidateQuery({ ...draft, kind: "track" }).medium_id, scopeID);
  assert.equal(identityCandidateQuery({ ...draft, kind: "release" }).work_id, undefined);
});

test("complete review filters own aliases and deduplicates canonical acknowledgement IDs", () => {
  const other = candidate();
  const alias = { ...other, id: aliasID, status: "merged", title: "Old title" };
  const data = readIdentityCandidates(response([match(other), match(other, alias), match(candidate(ownID))]), entity);
  assert.deepEqual(data.candidateIDs, [otherID]);
  assert.equal(data.items.length, 2);
  assert.equal(data.items[1].matched.title, "Old title");
  assert.equal(data.items[1].canonical.id, otherID);
});

test("incomplete, malformed and unresolved lookups cannot be acknowledged as empty results", () => {
  for (const incomplete of [response([], false, 12), response([], true, 1)]) {
    assert.throws(() => readIdentityCandidates(incomplete, entity), /identity_candidates_incomplete/);
  }
  for (const unresolved of [
    response([{ matched: candidate(), canonical: null, resolution_error: "canonical_identity_unverified" }]),
    response([match({ ...candidate(), status: "deleted" })]),
    response([match({ ...candidate(), status: "merged" })]),
    response([match({ ...candidate(), kind: "agent" })]),
  ]) assert.throws(() => readIdentityCandidates(unresolved, entity), /identity_candidate_unresolved/);
  for (const malformed of [{}, response([match(), match()]), response([match({ ...candidate(), external_ids: { unsafe: 123 } } as unknown as IdentityCandidateEntity)])]) {
    assert.throws(() => readIdentityCandidates(malformed, entity), /invalid_identity_review|identity_candidate_unresolved/);
  }
  assert.deepEqual(readIdentityCandidates(response([]), entity), { candidateIDs: [], items: [] });
});

test("receipt IDs are metadata, never implicit confirmation; explicit confirmation is bound to current identity", () => {
  const details = { error: "identity_candidates_changed", identity_review: { operation: 0, candidate_ids: [otherID] } };
  assert.deepEqual(identityConflictIDs(details), [otherID]);
  for (const bad of [{}, { ...details, identity_review: { candidate_ids: ["not-a-uuid"] } }, { ...details, identity_review: { candidate_ids: [otherID, otherID] } }]) assert.equal(identityConflictIDs(bad), null);
  const key = identityReviewKey(entity);
  const review: IdentityReviewState = { key, expectedIDs: [otherID], status: "ready", confirmed: false, data: readIdentityCandidates(response([match()]), entity) };
  assert.equal(reviewedIdentityIDs(review, key), undefined);
  assert.deepEqual(reviewedIdentityIDs({ ...review, confirmed: true }, key), [otherID]);
  for (const edited of [{ ...entity, title: "Changed" }, { ...entity, external_ids: { database: "456" } }, { ...entity, translations: { ja: { title: "Changed" } } }, { ...entity, translations: { ja: { title: "", aliases: ["Changed"] } } }]) {
    assert.equal(reviewedIdentityIDs({ ...review, confirmed: true }, identityReviewKey(edited)), undefined);
  }
  assert.equal(identityReviewKey({ ...entity, attributes: { unrelated: "changed" } }), key);
  assert.notEqual(identityReviewKey({ ...entity, kind: "expression", work_id: scopeID }), identityReviewKey({ ...entity, kind: "expression", work_id: ownID }));
  for (const status of ["loading", "failed"] as const) assert.equal(reviewedIdentityIDs({ ...review, confirmed: true, status }, key), undefined);
});

test("fetchApi preserves the stable 409 receipt and the candidate API checks completion", async () => {
  const originalFetch = globalThis.fetch;
  const details = { error: "identity_candidates_changed", applied: false, identity_review: { operation: 0, candidate_ids: [otherID] } };
  try {
    globalThis.fetch = async () => new Response(JSON.stringify(details), { status: 409, headers: { "Content-Type": "application/json" } });
    await assert.rejects(fetchApi("/catalog/entities", { method: "POST", body: "{}" }), (error: unknown) => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.status, 409);
      assert.equal(error.message, "identity_candidates_changed");
      assert.deepEqual(error.details, details);
      return true;
    });
    globalThis.fetch = async (input, init) => {
      assert.ok(String(input).endsWith("/api/catalog/entities/candidates"));
      assert.equal(init?.method, "POST");
      assert.deepEqual(JSON.parse(String(init?.body)), identityCandidateQuery(entity));
      return new Response(JSON.stringify(response([match()])), { headers: { "Content-Type": "application/json" } });
    };
    assert.deepEqual((await fetchIdentityCandidates(entity)).candidateIDs, [otherID]);
    globalThis.fetch = async () => new Response(JSON.stringify(response([], false, 1001)), { headers: { "Content-Type": "application/json" } });
    await assert.rejects(fetchIdentityCandidates(entity), /identity_candidates_incomplete/);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("review panel shows matched and canonical identities and starts without a checked confirmation", () => {
  const canonical = candidate();
  const matched = { ...canonical, id: aliasID, status: "merged", title: "Historical title" };
  const review: IdentityReviewState = { key: identityReviewKey(entity), expectedIDs: [otherID], status: "ready", confirmed: false, data: readIdentityCandidates(response([match(canonical, matched)]), entity) };
  const props = { review, busy: false, definitions: null, kindLabel: (kind: string) => kind, onConfirm: () => {}, onReload: () => {} };
  const html = renderToStaticMarkup(createElement(IdentityReviewPanel, props));
  assert.match(html, /Historical title/);
  assert.match(html, /Candidate title/);
  assert.match(html, new RegExp(`/catalog/${otherID}`));
  assert.match(html, new RegExp(`/catalog/${aliasID}`));
  assert.match(html, /候補.*別名/);
  assert.match(html, /database/);
  assert.doesNotMatch(html, /checked=""/);
  for (const [locale, title] of [["zh-CN", "审查可能重复的条目"], ["en-US", "Review possible duplicate entries"]]) {
    const localized = renderToStaticMarkup(createElement(I18nProvider, {
      initialLocale: locale, children: createElement(IdentityReviewPanel, props),
    }));
    assert.ok(localized.includes(title), locale);
    assert.doesNotMatch(localized, /catalog\.identityReview\.|catalog\.error\./);
    assert.doesNotMatch(localized, /checked=""/);
  }
  const failed = renderToStaticMarkup(createElement(IdentityReviewPanel, { ...props, review: { ...review, status: "failed", data: undefined } }));
  assert.match(failed, /type="checkbox" disabled=""/);
  assert.doesNotMatch(failed, /identityReview.empty/);
});
