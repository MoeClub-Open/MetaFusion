import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ADMIN_WORKSPACE_CHANNEL, adminWorkspaceUrl, parseAdminFrameMessage,
  permittedAdminWorkspaces, readAdminWorkspace, safeWorkspaceHref,
} from "../src/lib/adminWorkspace";

const user = (permissions: string[]) => ({ id: "fixture", username: "fixture", permissions });

test("workspace navigation only exposes the user's service governance permissions", () => {
  assert.deepEqual(permittedAdminWorkspaces(null), []);
  assert.deepEqual(permittedAdminWorkspaces(user([])), []);
  assert.deepEqual(permittedAdminWorkspaces(user(["storage.asset.upload", "community.post.create", "catalog.entity.edit"])), []);
  assert.deepEqual(permittedAdminWorkspaces(user(["auth.groups.manage"])), ["account"]);
  assert.deepEqual(permittedAdminWorkspaces(user(["community.report.review"])), ["community"]);
  assert.deepEqual(permittedAdminWorkspaces(user(["storage.asset.moderate"])), ["storage"]);
  assert.deepEqual(permittedAdminWorkspaces(user(["community.rate_limits.manage"])), ["catalog"]);
  assert.deepEqual(permittedAdminWorkspaces(user(["*"])), ["catalog", "account", "community", "storage"]);
});

test("workspace links retain page, search and hash for refresh and sharing", () => {
  const path = "/admin/account/groups/?q=a%26b#permissions";
  const url = new URL(adminWorkspaceUrl("account", path), "https://site.invalid");
  assert.equal(url.pathname, "/admin");
  assert.deepEqual(readAdminWorkspace(url.search), { workspace: "account", href: path });
  const community = new URL(adminWorkspaceUrl("community", "/admin/community/#reports"), "https://site.invalid");
  assert.equal(readAdminWorkspace(community.search).href, "/admin/community/#reports");
  assert.equal(adminWorkspaceUrl("catalog", "#definitions"), "/admin#definitions");
  assert.deepEqual(readAdminWorkspace("?workspace=account"), { workspace: "account", href: "/admin/account/" });
  assert.deepEqual(readAdminWorkspace("?workspace=unknown"), { workspace: null });
});

test("invalid iframe destinations never escape the selected service", () => {
  for (const input of [
    null, {}, "https://other.invalid/admin/account/", "//other.invalid/admin/account/", "javascript:alert(1)",
    "/login", "/admin/accounting/", "/admin/storage/", "/admin/account/../storage/", "/admin/account/%2e%2e/storage/",
    "/admin/account/%2e%2e%2fstorage/", "/admin/account/%252f..", "/admin/account/\\..\\storage/",
    "/admin/account/api/health", "/admin/account/%61pi/health", "/admin/account/_next/static/app.js",
    "/admin/account/%00", "/admin/account/%bad-encoding", "/admin/account/\n",
  ]) assert.equal(safeWorkspaceHref("account", input), null, String(input));
  assert.equal(safeWorkspaceHref("account", "/admin/account"), "/admin/account/");
  assert.equal(safeWorkspaceHref("storage", "/admin/storage/assets/?id=x"), "/admin/storage/assets/?id=x");
  const fallback = new URL(adminWorkspaceUrl("account", "/admin/storage/"), "https://site.invalid");
  assert.equal(readAdminWorkspace(fallback.search).href, "/admin/account/");
});

test("frame protocol rejects another workspace, unsupported messages and invalid navigation", () => {
  const message = { channel: ADMIN_WORKSPACE_CHANNEL, workspace: "community", type: "navigate", href: "/admin/community/#posts" };
  assert.deepEqual(parseAdminFrameMessage("community", message), { type: "navigate", href: message.href, push: false });
  assert.deepEqual(parseAdminFrameMessage("community", { ...message, history: "push" }), { type: "navigate", href: message.href, push: true });
  assert.equal((parseAdminFrameMessage("community", { ...message, type: "ready", history: "push" }) as { push: boolean }).push, false);
  assert.equal(parseAdminFrameMessage("account", message), null);
  assert.equal(parseAdminFrameMessage("community", { ...message, channel: "other" }), null);
  assert.equal(parseAdminFrameMessage("community", { ...message, type: "preferences" }), null);
  assert.equal(parseAdminFrameMessage("community", { ...message, href: "/api/auth/me" }), null);
  assert.equal(parseAdminFrameMessage("community", null), null);
  assert.deepEqual(parseAdminFrameMessage("community", { ...message, type: "login" }), { type: "login" });
});
