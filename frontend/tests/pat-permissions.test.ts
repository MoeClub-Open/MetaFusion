import { test } from "node:test";
import assert from "node:assert/strict";
import { can, grantablePermissionCodes } from "../src/lib/permissions";
import { ALL_PERMISSION_CODES } from "../src/lib/permissions.generated";
import { readFileSync } from "node:fs";

const user = (permissions: string[]) => ({ id: "fixture", permissions });

test("PAT selector offers held business permissions without management grants", () => {
  const member = user([
    "auth.users.manage", "catalog.lifecycle.manage", "community.rate_limits.manage",
    "catalog.entity.edit", "community.post.moderate", "storage.asset.upload",
  ]);
  assert.deepEqual(grantablePermissionCodes(member), [
    "catalog.entity.edit", "community.post.moderate", "storage.asset.upload",
  ]);
  assert.ok(can(member, "catalog.lifecycle.manage"));
});

test("wildcard ownership expands only to delegable concrete PAT scopes", () => {
  const admin = user(["*"]);
  const scopes = grantablePermissionCodes(admin);
  assert.ok(scopes.includes("catalog.entity.edit"));
  assert.ok(scopes.includes("auth.audit.read"));
  assert.ok(scopes.includes("community.report.review"));
  assert.ok(scopes.every((scope) => scope !== "*" && !scope.endsWith(".manage")));
  assert.ok(!scopes.includes("mcp.connect"));
  assert.ok(!scopes.includes("catalog.review.bypass"));
  assert.ok(can(admin, "catalog.lifecycle.manage"));
});

test("permission names exist in every locale, including permissions on existing keys", () => {
  for (const locale of ["zh-CN", "zh-TW", "en-US", "ja-JP"]) {
    const messages = JSON.parse(readFileSync(new URL(`../src/messages/${locale}.json`, import.meta.url), "utf8"));
    for (const code of ALL_PERMISSION_CODES) {
      if (code === "mcp.connect") continue;
      const label = messages[`developer.apiKeyScope.${code}`];
      assert.ok(typeof label === "string" && label.trim() && label !== code, `${locale}: ${code}`);
    }
  }
  assert.deepEqual(grantablePermissionCodes(user(["catalog.entity.edit", "catalog.review.bypass"])), ["catalog.entity.edit"]);
});

test("PAT selector preserves custom business scopes and filters custom management scopes", () => {
  assert.deepEqual(grantablePermissionCodes(user([
    "future.configuration.manage", "future.data.export", "future.manage.audit",
    "catalog.entity.edit",
  ])), ["catalog.entity.edit", "future.data.export", "future.manage.audit"]);
});

test("management-only and missing identities have no issuable PAT scopes", () => {
  assert.deepEqual(grantablePermissionCodes(user(["auth.users.manage", "future.configuration.manage"])), []);
  assert.deepEqual(grantablePermissionCodes(user([])), []);
  assert.deepEqual(grantablePermissionCodes(user(["mcp.connect"])), []);
  assert.deepEqual(grantablePermissionCodes(null), []);
  assert.deepEqual(grantablePermissionCodes(undefined), []);
});
