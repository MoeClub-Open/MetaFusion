import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchUserContributions } from "../src/lib/api/users";

function emptyContributions() {
  return {
    year: 2026,
    timezone: "UTC",
    summary: {
      merged_commits: 0,
      changed_entities: 0,
      created_entities: 0,
      merged_pull_requests: 0,
      legacy_revisions: 0,
    },
    calendar: [],
    items: [],
    total: 0,
    page: 1,
    page_size: 20,
  };
}

function mergedContribution() {
  return {
    id: "commit-one",
    commit_id: "commit-one",
    message: "Correct release credits",
    authored_at: "2026-10-17T16:00:00Z",
    merged_at: "2026-10-18T02:00:00Z",
    author: { id: "contributor", name: "Contributor" },
    authors: [{ id: "contributor", name: "Contributor" }],
    merged_by: { id: "maintainer", name: "Maintainer" },
    pull_request_id: "pull-request-one",
    changes_count: 2,
    entities: [
      { id: "release-one", kind: "release", title: "Original release title" },
      { id: "agent-one", kind: "agent", title: "Original artist name" },
    ],
    legacy: false,
  };
}

async function withResponse<T>(payload: unknown, run: () => Promise<T>): Promise<T> {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => Response.json(payload)) as typeof fetch;
  try {
    return await run();
  } finally {
    globalThis.fetch = originalFetch;
  }
}

test("contribution queries select a year and page without sending the removed tab filter", async () => {
  const originalFetch = globalThis.fetch;
  let request: URL | undefined;
  let method: string | undefined;
  globalThis.fetch = (async (url, init) => {
    request = new URL(String(url), "http://localhost");
    method = init?.method ?? "GET";
    return Response.json({ ...emptyContributions(), page: 2, page_size: 40 });
  }) as typeof fetch;
  try {
    await fetchUserContributions("person/with space", { year: 2026, page: 2, pageSize: 40 });
    assert.equal(method, "GET");
    assert.equal(request?.pathname, "/api/users/person%2Fwith%20space/contributions");
    assert.deepEqual(Object.fromEntries(request!.searchParams), {
      year: "2026",
      page: "2",
      page_size: "40",
    });
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("a successful empty contribution year preserves real zero counts and empty lists", async () => {
  const payload = emptyContributions();
  const result = await withResponse(payload, () => fetchUserContributions("contributor", { year: 2026 }));
  assert.deepEqual(result, payload);
});

test("merged contribution metadata keeps authors separate from mergers and preserves legacy revisions", async () => {
  const payload = {
    ...emptyContributions(),
    summary: {
      merged_commits: 1,
      changed_entities: 2,
      created_entities: 0,
      merged_pull_requests: 1,
      legacy_revisions: 1,
    },
    calendar: [{ date: "2026-10-18", count: 1 }],
    items: [
      mergedContribution(),
      {
        id: "legacy-revision-one",
        message: "Historical title correction",
        authored_at: "2026-09-01T03:00:00Z",
        merged_at: "2026-09-01T03:00:00Z",
        author: { id: "contributor", name: "Contributor" },
        authors: [{ id: "contributor", name: "Contributor" }],
        changes_count: 1,
        entities: [{ id: "work-one", kind: "work", title: "Original work title" }],
        legacy: true,
      },
    ],
    total: 2,
  };
  const result = await withResponse(payload, () => fetchUserContributions("contributor", { year: 2026 }));
  assert.deepEqual(result, payload);
  assert.notEqual(result.items[0]?.author.id, result.items[0]?.merged_by?.id);
  assert.equal(result.items[1]?.commit_id, undefined);
});

test("contributions preserve coauthors independently from the actual commit caller", async () => {
  const item = {
    ...mergedContribution(),
    author: { id: "commit-caller", name: "Commit caller" },
    authors: [
      { id: "contributor", name: "Contributor" },
      { id: "coauthor", name: "Coauthor" },
    ],
  };
  const payload = { ...emptyContributions(), items: [item], total: 1 };
  const result = await withResponse(payload, () => fetchUserContributions("contributor", { year: 2026 }));
  assert.deepEqual(result.items, [item]);
  assert.notEqual(result.items[0]?.author.id, "contributor");
  assert.equal(result.items[0]?.authors.some((author) => author.id === "contributor"), true);
});

test("partially visible merged contributions preserve redacted messages and absent detail links", async () => {
  const { commit_id: _commitId, pull_request_id: _pullRequestId, ...visibleItem } = mergedContribution();
  const item = {
    ...visibleItem,
    message: "",
    changes_count: 1,
    entities: [visibleItem.entities[0]],
  };
  const payload = { ...emptyContributions(), items: [item], total: 1 };
  const result = await withResponse(payload, () => fetchUserContributions("contributor", { year: 2026 }));
  assert.deepEqual(result.items, [item]);
  assert.equal(result.items[0]?.legacy, false);
  assert.equal(result.items[0]?.message, "");
  assert.equal(Object.hasOwn(result.items[0]!, "commit_id"), false);
  assert.equal(Object.hasOwn(result.items[0]!, "pull_request_id"), false);
});

test("old statistics responses and absent contribution summaries fail instead of becoming an empty year", async () => {
  const { summary: _summary, ...withoutSummary } = emptyContributions();
  for (const payload of [
    { items: [], total: 0, stats: { revisions_count: 0, audit_actions: 0 } },
    withoutSummary,
    { ...emptyContributions(), summary: null },
    { ...emptyContributions(), summary: { revisions_count: 0 } },
  ]) {
    await withResponse(payload, () => assert.rejects(fetchUserContributions("contributor"), /invalid_response/));
  }
});

test("malformed contribution calendars, item lists and timezones are rejected without fallback", async () => {
  for (const payload of [
    { ...emptyContributions(), calendar: null },
    { ...emptyContributions(), calendar: {} },
    { ...emptyContributions(), items: null },
    { ...emptyContributions(), items: {} },
    { ...emptyContributions(), timezone: "Asia/Shanghai" },
    { ...emptyContributions(), timezone: undefined },
    { ...emptyContributions(), calendar: [{ date: "2026-02-29", count: 0 }] },
  ]) {
    await withResponse(payload, () => assert.rejects(fetchUserContributions("contributor"), /invalid_response/));
  }
});

test("contribution rows require explicit authors, merge timestamps and legacy markers without old aliases", async () => {
  const row = mergedContribution();
  const { author: _author, ...withoutAuthor } = row;
  const { merged_at: _mergedAt, ...withoutMergedAt } = row;
  const { legacy: _legacy, ...withoutLegacy } = row;
  for (const item of [
    withoutAuthor,
    { ...withoutAuthor, author_id: "contributor", author_name: "Contributor" },
    { ...row, author: "Contributor" },
    { ...row, author: { id: "contributor" } },
    withoutMergedAt,
    { ...withoutMergedAt, created_at: row.merged_at },
    withoutLegacy,
    { ...row, legacy: "false" },
  ]) {
    await withResponse({ ...emptyContributions(), items: [item], total: 1 }, () =>
      assert.rejects(fetchUserContributions("contributor"), /invalid_response/),
    );
  }
});

test("contribution attribution requires an explicit nonempty author list with valid actors", async () => {
  const { authors: _authors, ...withoutAuthors } = mergedContribution();
  for (const item of [
    withoutAuthors,
    { ...withoutAuthors, authors: [] },
    { ...withoutAuthors, authors: [{ name: "Contributor" }] },
    { ...withoutAuthors, authors: [{ id: "contributor" }] },
  ]) {
    await withResponse({ ...emptyContributions(), items: [item], total: 1 }, () =>
      assert.rejects(fetchUserContributions("contributor"), /invalid_response/),
    );
  }
});

test("failed contribution requests remain failures instead of appearing as no contributions", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => Response.json({ error: "catalog_unavailable" }, { status: 503 })) as typeof fetch;
  try {
    await assert.rejects(fetchUserContributions("contributor"), /catalog_unavailable/);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
