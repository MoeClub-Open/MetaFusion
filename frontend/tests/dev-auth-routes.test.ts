import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";

type RewriteRoute = { source: string; destination: string };

function rewrites(nodeEnv: string, backendOrigin?: string): RewriteRoute[] {
  const env: Record<string, string | undefined> = { ...process.env, NODE_ENV: nodeEnv };
  if (backendOrigin) env.BACKEND_ORIGIN = backendOrigin;
  else delete env.BACKEND_ORIGIN;
  const configURL = new URL("../next.config.ts", import.meta.url);
  const result = spawnSync(
    process.execPath,
    [
      "--input-type=module",
      "-e",
      `import config from ${JSON.stringify(configURL.href)}; console.log(JSON.stringify(await config.rewrites()));`,
    ],
    { env: env as NodeJS.ProcessEnv, encoding: "utf8", windowsHide: true },
  );
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout) as RewriteRoute[];
}

test("宿主机开发通过显式网关同时转发登录、初始化及账号资源", () => {
  const routes = rewrites("development", "http://127.0.0.1:8888/");
  for (const path of ["/api/:path*", "/login", "/setup", "/auth-user-assets/:path*"]) {
    assert.deepEqual(
      routes.find((route) => route.source === path),
      { source: path, destination: `http://127.0.0.1:8888${path}` },
    );
  }
  assert.equal(rewrites("development").length, 1);
  assert.equal(rewrites("production", "http://catalog:8080").length, 1);
});
