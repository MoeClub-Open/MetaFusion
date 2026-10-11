import { test } from "node:test";
import assert from "node:assert/strict";
import { canUseCatalogAdminTab } from "../src/lib/catalogAdminNavigation";

const user = (permissions: string[]) => ({ id: "fixture", username: "fixture", permissions });

test("catalog console access does not grant every management section", () => {
  const definitions = user(["catalog.definitions.manage"]);
  for (const section of ["definitions", "extdb", "ratelimits"] as const) assert.ok(canUseCatalogAdminTab(definitions, section));
  for (const section of ["reviews", "merge", "shelves"] as const) assert.equal(canUseCatalogAdminTab(definitions, section), false);
  assert.ok(canUseCatalogAdminTab(user(["catalog.shelves.manage"]), "shelves"));
  assert.equal(canUseCatalogAdminTab(user(["catalog.shelves.manage"]), "definitions"), false);
});

test("foreign management permissions and empty grants cannot enter catalog governance", () => {
  for (const permissions of [[], ["auth.users.manage"], ["storage.asset.moderate"], ["community.report.review"]]) {
    for (const section of ["overview", "entities", "merge", "definitions"] as const) assert.equal(canUseCatalogAdminTab(user(permissions), section), false);
  }
  assert.equal(canUseCatalogAdminTab(null, "merge"), false);
});

test("lifecycle and wildcard grants expose their corresponding management work", () => {
  for (const section of ["entities", "reviews", "merge"] as const) assert.ok(canUseCatalogAdminTab(user(["catalog.lifecycle.manage"]), section));
  assert.equal(canUseCatalogAdminTab(user(["catalog.lifecycle.manage"]), "definitions"), false);
  for (const section of ["overview", "reviews", "definitions", "shelves", "merge"] as const) assert.ok(canUseCatalogAdminTab(user(["*"]), section));
});

test("community rate-limit grant opens only the shared rate-limit workspace", () => {
  const operator = user(["community.rate_limits.manage"]);
  assert.ok(canUseCatalogAdminTab(operator, "ratelimits"));
  for (const section of ["overview", "entities", "reviews", "merge", "definitions", "extdb", "shelves", "modules", "exchange"] as const) {
    assert.equal(canUseCatalogAdminTab(operator, section), false);
  }
});
