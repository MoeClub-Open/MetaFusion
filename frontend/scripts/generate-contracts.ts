#!/usr/bin/env node
// 契约单源生成器：把后端与各子系统里"已经声明的事实"生成成前端常量，避免手抄。
//
//   bun scripts/generate-contracts.ts           写入生成物
//   bun scripts/generate-contracts.ts --check   只校验（不一致或来源缺失时非 0 退出）
//   bun scripts/generate-contracts.ts --check --allow-missing-sources  主仓单独检出的部分校验
//
// 来源：
//   backend/internal/catalog/permission.go              目录权限码（本仓库，必需）
//   backend/migrations/baseline.json 指向的安装基线     实体骨架 kinds（本仓库，必需）
//   ../metafusion-auth/internal/store/access.go         账号权限码 + 全量码表（兄弟仓库）
//   ../metafusion-community/internal/auth/permission.go 互动权限码（兄弟仓库）
//   ../metafusion-storage/internal/auth/permission.go   存储权限码（兄弟仓库）
//
//   ../metafusion-{auth,community,storage}/admin/           三个域管理台登记的权限码（兄弟仓库）
//
// 默认所有来源都必需；显式 --check --allow-missing-sources 才可沿用既有生成物做部分校验。
// 缺少来源时不能写入生成物，跨仓 CI 不使用豁免开关。

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(HERE, "../..");
const CHECK = process.argv.includes("--check");
const ALLOW_MISSING = CHECK && process.argv.includes("--allow-missing-sources");
if (!CHECK && process.argv.includes("--allow-missing-sources")) {
  console.error("--allow-missing-sources 仅限 --check 部分校验，不能用于生成");
  process.exit(1);
}

const GENERATED_PERMISSIONS = path.join(HERE, "../src/lib/permissions.generated.ts");
const GENERATED_KINDS = path.join(HERE, "../src/lib/kinds.generated.ts");
const PERMISSIONS_TS = path.join(HERE, "../src/lib/permissions.ts");
const CATALOG_API_TS = path.join(HERE, "../src/components/catalog/api.ts");

const SERVICES = [
  { id: "assistant", prefix: "assistant.", file: path.join(ROOT, "backend/internal/assistant/permissions.go"), required: true, note: "backend/internal/assistant/permissions.go",
    why: "LLM 配置、积分、BYO 凭据和生成缓存的后台管理权限。" },
  { id: "catalog", prefix: "catalog.", file: path.join(ROOT, "backend/internal/catalog/permission.go"), required: true, note: "backend/internal/catalog/permission.go",
    why: "目录权限：条目/关系/定义/生命周期/货架（判定在 backend/internal/catalog/permission.go 的 User.Can）。" },
  { id: "auth", prefix: "auth.", file: path.join(ROOT, "../metafusion-auth/internal/store/access.go"), required: false, note: "../metafusion-auth/internal/store/access.go",
    why: "账号权限：auth.oauth.manage 覆盖客户端管理、密钥轮换、令牌吊销与审计（metafusion-auth/internal/handler/oauth_admin.go）。" },
  { id: "mcp", prefix: "mcp.", file: path.join(ROOT, "../metafusion-auth/internal/store/access.go"), required: false, note: "../metafusion-auth/internal/store/access.go",
    why: "MCP 连接身份由账号服务声明，与个人 API Key 的业务权限分开。" },
  { id: "community", prefix: "community.", file: path.join(ROOT, "../metafusion-community/internal/auth/permission.go"), required: false, note: "../metafusion-community/internal/auth/permission.go",
    why: "互动权限：发帖/审核/置顶/板块管理（metafusion-community/internal/auth/permission.go）。" },
  { id: "storage", prefix: "storage.", file: path.join(ROOT, "../metafusion-storage/internal/auth/permission.go"), required: false, note: "../metafusion-storage/internal/auth/permission.go",
    why: "存储权限：上传与内容审核（metafusion-storage/internal/auth/permission.go）。" },
];

const problems = [];
const warnings = [];

/** 从 Go 源码里取带点号的权限码字面量（按服务前缀过滤）。 */
function codesFromGo(file, prefix) {
  const text = fs.readFileSync(file, "utf8");
  const found = new Set();
  const re = /"([a-z][a-z0-9]*(?:[.][a-z0-9_]+)+)"/g;
  let m;
  while ((m = re.exec(text)) !== null) if (m[1].startsWith(prefix)) found.add(m[1]);
  return [...found].sort();
}

/** 取文件里所有带点号的码（不限服务前缀）：账号服务持有全量码表，需要整体比对。 */
function allCodesFromGo(file) {
  const text = fs.readFileSync(file, "utf8");
  const found = new Set();
  const re = /"([a-z][a-z0-9]*(?:[.][a-z0-9_]+)+)"/g;
  let m;
  while ((m = re.exec(text)) !== null) found.add(m[1]);
  return found;
}

/** 从现有生成物里取某个服务已有的码（兄弟仓库缺席时的兜底）。 */
function codesFromGenerated(prefix) {
  if (!fs.existsSync(GENERATED_PERMISSIONS)) return [];
  const text = fs.readFileSync(GENERATED_PERMISSIONS, "utf8");
  const found = new Set();
  const re = new RegExp('export const [A-Z0-9_]+ = "(' + prefix.replace(/[.]/g, "[.]") + '[a-z0-9_.]+)"', "g");
  let m;
  while ((m = re.exec(text)) !== null) found.add(m[1]);
  return [...found].sort();
}

/** 实体骨架来自当前安装基线的命名 CHECK 约束，不从历史夹具生成。 */
const migrationDir = path.join(ROOT, "backend/migrations");
const baselineManifest = JSON.parse(fs.readFileSync(path.join(migrationDir, "baseline.json"), "utf8"));
const baselinePrefix = String(baselineManifest.version).padStart(6, "0") + "_";
const baselineFiles = fs.readdirSync(migrationDir).filter((file) => file.startsWith(baselinePrefix) && file.endsWith(".up.sql"));
if (baselineFiles.length !== 1) throw new Error("安装基线文件必须唯一且与 baseline.json 一致");
const baselineSource = "backend/migrations/" + baselineFiles[0];
function kindsFromMigration() {
  const text = fs.readFileSync(path.join(ROOT, baselineSource), "utf8");
  const constraint = text.match(/CONSTRAINT\s+entities_kind_check\s+CHECK\s*\(([^\n]+)\)/);
  if (!constraint) throw new Error("未在安装基线找到 entities_kind_check 约束");
  const names = [...constraint[1].matchAll(/'([a-z_]+)'/g)].map((match) => match[1]);
  if (names.length < 2 || new Set(names).size !== names.length) throw new Error("实体种类约束无法解析或有重复");
  return names;
}

const codeNames = (code) => code.toUpperCase().replace(/[.]/g, "_");

const codesByService = {};
let authAggregate = null;
for (const s of SERVICES) {
  if (fs.existsSync(s.file)) {
    codesByService[s.id] = codesFromGo(s.file, s.prefix);
    if (s.id === "auth") authAggregate = allCodesFromGo(s.file);
  } else {
    codesByService[s.id] = codesFromGenerated(s.prefix);
    if (s.required) {
      problems.push(s.note + " 缺失：本仓库必需来源读不到，无法生成目录权限码");
    } else if (ALLOW_MISSING) {
      warnings.push(s.id + "：来源 " + s.note + " 不存在，沿用现有生成物里的 " + codesByService[s.id].length + " 个码（未经来源校验）");
    } else {
      problems.push(s.note + " 缺失：无法完成跨仓权限来源校验；独立检出仅可显式使用 --check --allow-missing-sources");
    }
  }
}

// 跨服务一致性：账号服务持有全量码表，必须覆盖其它服务声明的码
if (authAggregate) {
  const authSet = authAggregate;
  for (const s of SERVICES) {
    if (s.id === "auth") continue;
    for (const code of codesByService[s.id]) {
      if (!authSet.has(code)) problems.push("账号服务的码表缺少 " + code + "（" + s.note + " 声明了它）");
    }
  }
}

const kinds = kindsFromMigration();
const flatCodes = [];
for (const s of SERVICES) for (const code of codesByService[s.id]) flatCodes.push(code);
const uniqueCodes = [...new Set(flatCodes)].sort();
if (!uniqueCodes.length) problems.push("没有解析出任何权限码");

// 三个域管理台是各服务仓库里的独立应用，不参与本仓库构建、拿不到生成物，只能自己登记码表。
// 这里钉住"登记进管理台的码必须真的存在于某个服务的码表"：加码/改码时漏改一个应用，
// 表现是入口该出现却不出现、或点进去注定 403，两边都不报错，只能靠人肉发现。
const ADMIN_APPS = [
  { id: "auth", note: "../metafusion-auth/admin/src/lib/permissions.ts",
    file: path.join(ROOT, "../metafusion-auth/admin/src/lib/permissions.ts") },
  { id: "community", note: "../metafusion-community/admin/src/lib/permissions.ts",
    file: path.join(ROOT, "../metafusion-community/admin/src/lib/permissions.ts") },
  { id: "storage", note: "../metafusion-storage/admin/src/lib/session.ts",
    file: path.join(ROOT, "../metafusion-storage/admin/src/lib/session.ts") },
];

const knownCodes = new Set(uniqueCodes);
const adminCounts = [];
for (const app of ADMIN_APPS) {
  if (!fs.existsSync(app.file)) {
    if (ALLOW_MISSING) warnings.push("管理台 " + app.id + "：来源 " + app.note + " 不存在，跳过其权限码校验");
    else problems.push(app.note + " 缺失：无法校验管理台权限码来源");
    continue;
  }
  const text = fs.readFileSync(app.file, "utf8");
  const declared = new Set();
  const re = /"([a-z][a-z0-9]*(?:[.][a-z0-9_]+)+)"/g;
  let m;
  while ((m = re.exec(text)) !== null) {
    // 只认四个服务前缀：这个文件里还有错误码、i18n 键一类的字面量，不能一起收。
    if (SERVICES.some((s) => m[1].startsWith(s.prefix))) declared.add(m[1]);
  }
  for (const code of [...declared].sort()) {
    if (!knownCodes.has(code)) {
      problems.push(
        "管理台 " + app.id + " 登记了不存在于任何服务码表的权限码 " + code + "（" + app.note +
        "）：加码 / 改码 / 删码时漏改了这个应用",
      );
    }
  }
  adminCounts.push(app.id + "=" + declared.size);
}

const header = (title, sources) =>
  [
    "// 本文件由 frontend/scripts/generate-contracts.ts 生成，勿手改。",
    "// " + title,
    "// 来源：" + sources.join("、"),
    "// 校验：cd frontend && bun scripts/generate-contracts.ts --check",
    "",
    "",
  ].join("\n");

const permissionsOut =
  header("权限码单一来源（各服务自己声明，账号服务持有全量码表）。", SERVICES.map((s) => s.note)) +
  SERVICES.flatMap((s) => {
    const codes = codesByService[s.id];
    if (!codes.length) return [];
    return [
      "// ── " + s.id + " ──",
      "// " + s.why,
      ...codes.map((code) => 'export const ' + codeNames(code) + ' = "' + code + '" as const;'),
      "",
      "export const " + s.id.toUpperCase() + "_PERMISSION_CODES = [",
      ...codes.map((code) => "  " + codeNames(code) + ","),
      "] as const;",
      "",
    ];
  }).join("\n") +
  [
    "",
    "/** 各服务的码表（判定逻辑仍由各服务自己实现，这里只做前端入口显隐）。 */",
    "export const PERMISSION_CODES_BY_SERVICE = {",
    ...SERVICES.map((s) => "  " + s.id + ": " + s.id.toUpperCase() + "_PERMISSION_CODES,"),
    "} as const;",
    "",
    "/** 全部权限码（去重、按字母序）。 */",
    "export const ALL_PERMISSION_CODES = [",
    ...uniqueCodes.map((code) => '  "' + code + '",'),
    "] as const;",
    "",
  ].join("\n");

const kindsOut =
  header("实体骨架八元组（目录库基线的 catalog.entities.kind 约束）。", [baselineSource]) +
  [
    "export const ENTITY_KINDS = [",
    ...kinds.map((k) => '  "' + k + '",'),
    "] as const;",
    "",
    "export type EntityKind = (typeof ENTITY_KINDS)[number];",
    "",
  ].join("\n");

// 反手抄守卫：手写文件里不该再出现码字面量 / kinds 字面量
const permissionsSrc = fs.readFileSync(PERMISSIONS_TS, "utf8");
const strayCodes = permissionsSrc.match(/"[a-z][a-z0-9]*[.][a-z0-9_.]+"/g);
if (strayCodes) problems.push("frontend/src/lib/permissions.ts 里还有权限码字面量：" + [...new Set(strayCodes)].join(", ") + "（应改从 permissions.generated 导入）");
const catalogApiSrc = fs.readFileSync(CATALOG_API_TS, "utf8");
if (!catalogApiSrc.includes("ENTITY_KINDS")) problems.push("frontend/src/components/catalog/api.ts 没有使用生成的 ENTITY_KINDS");
if (!catalogApiSrc.includes("kinds.generated")) problems.push("frontend/src/components/catalog/api.ts 没有引用 kinds.generated");

const targets = [
  { file: GENERATED_PERMISSIONS, content: permissionsOut, label: "src/lib/permissions.generated.ts" },
  { file: GENERATED_KINDS, content: kindsOut, label: "src/lib/kinds.generated.ts" },
];

if (CHECK) {
  for (const t of targets) {
    if (!fs.existsSync(t.file)) problems.push(t.label + " 不存在，请先跑 bun scripts/generate-contracts.ts");
    else if (fs.readFileSync(t.file, "utf8") !== t.content) problems.push(t.label + " 与来源不一致，请重跑生成器");
  }
} else if (!problems.length) {
  for (const t of targets) {
    fs.mkdirSync(path.dirname(t.file), { recursive: true });
    fs.writeFileSync(t.file, t.content);
    console.log("生成 " + t.label + "（" + t.content.split("\n").length + " 行）");
  }
}

for (const w of warnings) console.log("警告：" + w);
console.log("权限码：" + SERVICES.map((s) => s.id + "=" + codesByService[s.id].length).join(" ") + "；kinds=" + kinds.length);
if (adminCounts.length) console.log("管理台登记码：" + adminCounts.join(" "));
if (problems.length) {
  console.error("契约校验失败：");
  for (const p of problems) console.error("  - " + p);
  process.exit(1);
}
console.log(CHECK ? (warnings.length ? "部分契约校验通过：缺席来源未经校验，不能视为全量验证" : "契约校验通过：生成物与全部来源一致") : "契约生成完成");
