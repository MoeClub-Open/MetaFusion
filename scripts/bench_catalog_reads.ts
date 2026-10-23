#!/usr/bin/env node
// Read-only capacity probe. Run against a staging replica with a dedicated token;
// the caller chooses the rate. This does not modify quotas or catalog data.
import fs from "node:fs";
import { performance } from "node:perf_hooks";

const flags = {};
for (let i = 2; i < process.argv.length; i += 2) {
  const key = process.argv[i];
  if (
    ![
      "--base",
      "--rps",
      "--seconds",
      "--concurrency",
      "--query",
      "--entity",
      "--credentials",
    ].includes(key) ||
    process.argv[i + 1] === undefined
  )
    throw new Error("invalid_argument");
  flags[key] = process.argv[i + 1];
}
const credentials = flags["--credentials"]
  ? JSON.parse(fs.readFileSync(flags["--credentials"], "utf8"))
  : {};
const base = new URL(
  flags["--base"] ??
    process.env.MF_BASE ??
    credentials.baseUrl ??
    credentials.base_url,
);
if (
  !["http:", "https:"].includes(base.protocol) ||
  base.username ||
  base.password
)
  throw new Error("invalid_origin");
const pat = process.env.MF_PAT ?? credentials.pat;
const rps = Number(flags["--rps"] ?? 5),
  seconds = Number(flags["--seconds"] ?? 30),
  concurrency = Number(flags["--concurrency"] ?? 16);
if (
  !Number.isInteger(rps) ||
  rps < 1 ||
  rps > 1000 ||
  !Number.isInteger(seconds) ||
  seconds < 1 ||
  seconds > 60 ||
  !Number.isInteger(concurrency) ||
  concurrency < 1 ||
  concurrency > 128
)
  throw new Error("invalid_load_bounds");
const entity = flags["--entity"],
  query = flags["--query"] ?? "Encore";
const latencies = [],
  statuses = {},
  errors = {},
  started = performance.now();
let active = 0,
  scheduled = 0,
  dropped = 0,
  completed = 0;
const running = new Set();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
function scenario(i) {
  if (entity && i % 10 === 0)
    return {
      name: "entity",
      url: `/api/catalog/entities/${encodeURIComponent(entity)}`,
    };
  if (entity && i % 10 === 1)
    return {
      name: "relationships",
      url: "/api/catalog/relationships/query",
      body: { ids: [entity], limit: 20 },
    };
  return {
    name: "search",
    url: `/api/catalog/entities?q=${encodeURIComponent(query)}&limit=20`,
  };
}
async function sample(i) {
  const spec = scenario(i),
    before = performance.now();
  active++;
  try {
    const response = await fetch(new URL(spec.url, base.origin), {
      method: spec.body ? "POST" : "GET",
      headers: {
        Accept: "application/json",
        ...(pat ? { Authorization: `Bearer ${pat}` } : {}),
        ...(spec.body ? { "Content-Type": "application/json" } : {}),
      },
      body: spec.body ? JSON.stringify(spec.body) : undefined,
      redirect: "manual",
      signal: AbortSignal.timeout(10000),
    });
    const text = await response.text();
    let body;
    try {
      body = JSON.parse(text);
    } catch {}
    statuses[response.status] = (statuses[response.status] ?? 0) + 1;
    if (
      response.ok &&
      (spec.name === "search"
        ? !Array.isArray(body?.items)
        : spec.name === "relationships"
          ? !Array.isArray(body?.pages)
          : body?.id !== entity || !Number.isSafeInteger(body?.version))
    )
      errors.protocol = (errors.protocol ?? 0) + 1;
    if (!response.ok) {
      const code =
        typeof body?.error === "string" && /^[a-z_]+$/.test(body.error)
          ? body.error
          : `http_${response.status}`;
      errors[code] = (errors[code] ?? 0) + 1;
    }
  } catch {
    statuses.network = (statuses.network ?? 0) + 1;
    errors.network = (errors.network ?? 0) + 1;
  } finally {
    latencies.push(performance.now() - before);
    active--;
    completed++;
  }
}
for (let i = 0; i < rps * seconds; i++) {
  const due = started + (i * 1000) / rps,
    wait = due - performance.now();
  if (wait > 0) await sleep(wait);
  scheduled++;
  if (active >= concurrency) {
    dropped++;
    continue;
  }
  const work = sample(i);
  running.add(work);
  work.finally(() => running.delete(work));
}
await Promise.allSettled([...running]);
latencies.sort((a, b) => a - b);
const percentile = (p) =>
  latencies.length
    ? Number(
        latencies[
          Math.min(latencies.length - 1, Math.ceil(p * latencies.length) - 1)
        ].toFixed(1),
      )
    : null;
const failures = Object.values(errors).reduce((a, b) => a + b, 0),
  elapsed = (performance.now() - started) / 1000;
console.log(
  JSON.stringify(
    {
      origin: base.origin,
      read_only: true,
      target_rps: rps,
      seconds,
      concurrency,
      scheduled,
      completed,
      dropped,
      achieved_rps: Number((completed / elapsed).toFixed(1)),
      error_rate: completed ? failures / completed : null,
      statuses,
      errors,
      latency_ms: {
        p50: percentile(0.5),
        p95: percentile(0.95),
        p99: percentile(0.99),
      },
      capacity_proven: false,
    },
    null,
    2,
  ),
);
if (failures || dropped) process.exitCode = 1;
