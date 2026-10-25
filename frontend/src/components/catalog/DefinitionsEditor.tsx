"use client";
import React, { useEffect, useMemo, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import {
  api,
  Definitions,
  Field,
  Names,
  local,
} from "./api";
import { CATALOG_DEFINITIONS_MANAGE, can } from "@/lib/permissions";
import { useCatalog } from "./CatalogProvider";
import { useAuth } from "@/lib/authContext";
import {
  getKindName,
  getDefinitionETag,
  refreshDefinitions,
  resolveKindOptions,
  useDefinitions,
} from "@/lib/definitions";
import type { DynamicDefinitions } from "@/lib/definitions";
import { ChangeNote, ErrorMessage, NamesEditor } from "./Fields";
import { TemplateRulesEditor } from "./TemplateRulesEditor";
import { RelationRulesEditor } from "./RelationRulesEditor";
import { localizeCatalogError } from "@/lib/catalogErrors";

// 同一份服务端定义文档在前端有两个方向不同的类型：lib/definitions.ts 的 DynamicDefinitions
// 是只读消费视图，./api 的 Definitions 是写入视图。
// 已发布文档就是写入视图的输入，这里按写入视图收窄，免得编辑器整篇跟着换类型。
const asEditableDefinitions = (d: DynamicDefinitions): Definitions =>
  d as unknown as Definitions;

const newField = (): Field => ({ names: {}, type: "text", enabled: true, applicable_kinds: [] });
function Checks({
  values,
  selected,
  onChange,
  label,
}: {
  values: Record<string, string>;
  selected: string[];
  onChange: (v: string[]) => void;
  label: string;
}) {
  return (
    <fieldset>
      <legend>{label}</legend>
      <div className="cv-checks">
        {Object.entries(values).map(([k, name]) => (
          <label key={k}>
            <input
              type="checkbox"
              checked={(selected || []).includes(k)}
              onChange={(e) =>
                onChange(
                  e.target.checked
                    ? [...(selected || []), k]
                    : (selected || []).filter((x) => x !== k),
                )
              }
            />
            {name}
          </label>
        ))}
      </div>
    </fieldset>
  );
}
function TextList({
  value,
  onChange,
  label,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  label: string;
}) {
  return (
    <label>
      {label}
      <input
        value={(value || []).join(", ")}
        onChange={(e) =>
          onChange(
            e.target.value
              .split(",")
              .map((x) => x.trim())
              .filter(Boolean),
          )
        }
      />
    </label>
  );
}
function Dictionary<T extends { names: Names }>({
  value,
  onChange,
  create,
  render,
}: {
  value: Record<string, T>;
  onChange: (v: Record<string, T>) => void;
  create: () => T;
  render: (item: T, change: (v: T) => void, key: string) => React.ReactNode;
}) {
  const { t, locale } = useI18n();
  const [code, setCode] = useState("");
  const [search, setSearch] = useState("");
  const [selectedKey, setSelectedKey] = useState("");
  const visibleEntries = Object.entries(value).filter(([key, item]) =>
    `${key} ${local(item.names, locale, "", key)}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()),
  );
  const activeKey = visibleEntries.some(([key]) => key === selectedKey) ? selectedKey : (visibleEntries[0]?.[0] ?? "");
  const activeItem = activeKey ? value[activeKey] : undefined;
  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 rounded-xl border border-line-subtle bg-surfaceSubtle p-3 sm:flex-row sm:items-end">
        <label className="min-w-0 flex-1">
          {t("catalog.code")}
          <input value={code} onChange={(e) => setCode(e.target.value)} pattern="[a-z][a-z0-9_]*" />
        </label>
        <button type="button" disabled={!/^[a-z][a-z0-9_]*$/.test(code) || !!value[code]} onClick={() => {
          onChange({ ...value, [code]: create() });
          setSelectedKey(code);
          setSearch("");
          setCode("");
        }}>{t("catalog.add")}</button>
      </div>
      <div className="grid gap-4 xl:grid-cols-[15rem_minmax(0,1fr)]">
        <div className="min-w-0 rounded-xl border border-line-subtle bg-surfaceSubtle p-3">
          <label className="sr-only" htmlFor="definition-search">{t("catalog.definitionSearch")}</label>
          <input id="definition-search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("catalog.definitionSearch")} className="w-full" />
          <p className="my-2 text-xs text-text-faint">{t("catalog.definitionCount", { count: visibleEntries.length })}</p>
          <div className="max-h-[28rem] space-y-1 overflow-y-auto">
            {visibleEntries.map(([key, item]) => (
              <button key={key} type="button" onClick={() => setSelectedKey(key)} aria-current={activeKey === key ? "true" : undefined} className={`w-full text-left ${activeKey === key ? "cv-primary" : ""}`}>
                <span className="block truncate">{local(item.names, locale, "", key)}</span>
                <span className="block truncate text-[11px] font-mono text-text-faint">{key}</span>
              </button>
            ))}
            {visibleEntries.length === 0 && <p className="px-2 py-4 text-xs text-text-muted">{t("catalog.definitionEmpty")}</p>}
          </div>
        </div>
        {activeItem && (
          <section key={activeKey} className="min-w-0 space-y-4 rounded-xl border border-line-subtle bg-surfaceSubtle p-4">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line-subtle pb-3">
              <h2 className="text-sm font-semibold text-text-strong">{local(activeItem.names, locale, "", activeKey)} <span className="font-mono text-xs text-text-faint">({activeKey})</span></h2>
              <button type="button" onClick={() => {
                const next = { ...value };
                delete next[activeKey];
                onChange(next);
                setSelectedKey("");
              }}>{t("catalog.removeDefinition")}</button>
            </div>
            {render(activeItem, (next) => onChange({ ...value, [activeKey]: next }), activeKey)}
          </section>
        )}
      </div>
    </div>
  );
}
function FieldDefinition({
  value,
  onChange,
  d,
  code = "",
  groupFields,
}: {
  value: Field;
  onChange: (v: Field) => void;
  d: Definitions;
  /** 本字段在所属组/字典中的码，用于排除"自己当自己的起点"。 */
  code?: string;
  /** 所属 group 的子字段集合：number 子字段可从中选区间起点。 */
  groupFields?: Record<string, Field>;
}) {
  const { t, tr, locale } = useI18n();
  const { kinds: serverKinds } = useDefinitions();
  const patch = (v: Partial<Field>) => onChange({ ...value, ...v });
  // 允许种类：可选项取服务端 definitions.kinds（未停用），名称服务端优先、字典兜底。
  const kindNames = useMemo(
    () =>
      Object.fromEntries(
        resolveKindOptions(serverKinds).map((k) => [
          k,
          getKindName(serverKinds, k, locale, tr(`catalog.kind.${k}`, k)),
        ]),
      ),
    [serverKinds, locale, tr],
  );
  return (
    <>
      <NamesEditor value={value.names} onChange={(names) => patch({ names })} />
      <label>
        {t("catalog.fieldType")}
        <select
          value={value.type}
          onChange={(e) =>
            onChange({
              ...newField(),
              names: value.names,
              applicable_kinds: value.applicable_kinds,
              type: e.target.value,
              ...(e.target.value === "list"
                ? { items: newField() }
                : e.target.value === "group"
                  ? { fields: {} }
                  : {}),
            })
          }
        >
          {[
            "text",
            "multilingual",
            "number",
            "date",
            "boolean",
            "url",
            "enum",
            "entity",
            "list",
            "group",
          ].map((k) => (
            <option key={k} value={k}>
              {t(`catalog.fieldTypeNames.${k}`)}
            </option>
          ))}
        </select>
      </label>
      <div className="cv-checks">
        {(["enabled", "required", "searchable", "comparable", "hidden"] as const).map(
          (k) => (
            <label key={k}>
              <input
                type="checkbox"
                checked={!!value[k]}
                onChange={(e) => patch({ [k]: e.target.checked })}
              />
              {t(`catalog.${k}`)}
            </label>
          ),
        )}
      </div>
      {!groupFields && (
        <Checks
          label={tr("catalog.applicableKinds", "适用实体层级")}
          values={kindNames}
          selected={value.applicable_kinds || []}
          onChange={(applicable_kinds) => patch({ applicable_kinds })}
        />
      )}
      {value.type === "date" && <label>
        <input type="checkbox" checked={!!value.allow_yearless} onChange={(event) => patch({ allow_yearless: event.target.checked })} />
        {t("catalog.allowYearlessDate")}
      </label>}
      {/* 对比语义：闭集选择，只提供系统真正实现的规则，避免自由填写出不生效的配置。 */}
      <label>
        {t("catalog.semantics")}
        <select
          value={value.semantics || ""}
          onChange={(e) => patch({ semantics: e.target.value || undefined })}
        >
          <option value="">{t("catalog.semanticsDefault")}</option>
          <option value="content">{t("catalog.semanticsContent")}</option>
          <option value="locating">{t("catalog.semanticsLocating")}</option>
        </select>
      </label>
      {/* 锚点子字段：仅 group 字段有意义——组内其它子字段有值时该子字段必填。 */}
      {value.type === "group" && Object.keys(value.fields || {}).length > 0 && (
        <label>
          {t("catalog.anchorKey")}
          <select
            value={value.anchor_key || ""}
            onChange={(e) => patch({ anchor_key: e.target.value || undefined })}
          >
            <option value="">{t("catalog.none")}</option>
            {Object.keys(value.fields || {}).map((k) => (
              <option key={k} value={k}>
                {local(value.fields?.[k]?.names, locale, "", k)}
              </option>
            ))}
          </select>
        </label>
      )}
      {/* 区间顺序只认显式声明：number 子字段指定同组起点字段后才校验大小关系，
          后台新增数字字段不会因命名碰巧相似而触发隐含规则。 */}
      {value.type === "number" &&
        groupFields &&
        Object.keys(groupFields).filter((k) => k !== code).length > 0 && (
          <label>
            {t("catalog.rangeStart")}
            <select
              value={value.range_start || ""}
              onChange={(e) => patch({ range_start: e.target.value || undefined })}
            >
              <option value="">{t("catalog.none")}</option>
              {Object.keys(groupFields)
                .filter((k) => k !== code)
                .map((k) => (
                  <option key={k} value={k}>
                    {local(groupFields[k]?.names, locale, "", k)}
                  </option>
                ))}
            </select>
          </label>
        )}
      <label>{t("catalog.unit")}</label>
      <NamesEditor
        value={value.unit || {}}
        onChange={(unit) => patch({ unit })}
      />
      <div className="cv-grid">
        {(["min", "max"] as const).map((k) => (
          <label key={k}>
            {t(`catalog.${k}`)}
            <input
              type="number"
              step="any"
              value={value[k] ?? ""}
              onChange={(e) =>
                patch({
                  [k]:
                    e.target.value === "" ? undefined : Number(e.target.value),
                })
              }
            />
          </label>
        ))}
      </div>
      {value.type === "enum" && (
        <label>
          {t("catalog.vocabulary")}
          <select
            value={value.vocabulary || ""}
            onChange={(e) => patch({ vocabulary: e.target.value })}
          >
            <option value="">{t("catalog.select")}</option>
            {Object.entries(d.vocabularies).map(([k, v]) => (
              <option key={k} value={k}>
                {local(v.names, locale, "", k)}
              </option>
            ))}
          </select>
        </label>
      )}
      {value.type === "entity" && (
        <Checks
          label={t("catalog.allowedKinds")}
          values={kindNames}
          selected={value.kinds || []}
          onChange={(kinds) => patch({ kinds })}
        />
      )}{" "}
      {value.type === "list" && (
        <fieldset>
          <legend>{t("catalog.listItem")}</legend>
          <FieldDefinition
            value={value.items || newField()}
            onChange={(items) => patch({ items })}
            d={d}
          />
        </fieldset>
      )}
      {value.type === "group" && (
        <Dictionary
          value={value.fields || {}}
          onChange={(fields) => patch({ fields })}
          create={newField}
          render={(f, change, key) => (
            <FieldDefinition
              value={f}
              onChange={change}
              d={d}
              code={key}
              groupFields={value.fields}
            />
          )}
        />
      )}
    </>
  );
}
export function DefinitionsEditor() {
  const { t, tr, locale } = useI18n();
  const { modules } = useCatalog();
  const { user } = useAuth();
  // capabilities 缺少模块列表时显示空状态。
  const moduleList = Array.isArray(modules) ? modules : [];
  // 定义文档与并发校验标记来自同一份缓存。
  const { definitions: published, kinds: serverKinds, relationshipRules, etag } = useDefinitions();
  const [d, setD] = useState<Definitions>();
  const [base, setBase] = useState("");
  const [issues, setIssues] = useState<string[]>();
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [tab, setTab] = useState<keyof Definitions | "schemes">("fields");
  useEffect(() => {
    if (published && etag !== null && !d) {
      setD(structuredClone(asEditableDefinitions(published)));
      setBase(etag);
    }
  }, [published, etag, d]);
  // 权限码判定统一走 can()；未授予权限的令牌不按角色补权。
  const manageDefinitions = can(user, CATALOG_DEFINITIONS_MANAGE);
  if (!manageDefinitions) return <p>{t("catalog.adminRequired")}</p>;
  if (!d) return <p>{t("catalog.loading")}</p>;
  const change = (next: Definitions) => {
    setD(next);
    setIssues(undefined);
  };
  // 保存后刷新所有定义消费方，并从同一响应获取新的 etag。
  const reloadPublished = async () => {
    const current = await refreshDefinitions();
    const currentETag = getDefinitionETag();
    if (!current || currentETag === null) {
      // 保存已经落库成功，只是本地刷新没拿到：如实说明刷新失败。
      throw new Error("definition_refresh_failed");
    }
    setD(structuredClone(asEditableDefinitions(current)));
    setBase(currentETag);
    setIssues(undefined);
    setError("");
  };
  const names = (items: Record<string, { names: Names }>) =>
    Object.fromEntries(
      Object.entries(items).map(([k, v]) => [k, local(v.names, locale, "", k)]),
    );
  // 骨架层级名：服务端 definitions.kinds 优先，字典只作兜底（缺键退原始码）。
  const kindNames = Object.fromEntries(
    resolveKindOptions(serverKinds).map((k) => [
      k,
      getKindName(serverKinds, k, locale, tr(`catalog.kind.${k}`, k)),
    ]),
  );
  return (
    <div className="cv-form">
      <div className="cv-heading"><h1>{t("catalog.configure")}</h1></div>
      <ErrorMessage error={error} />
      <p className="cv-muted">{t("catalog.definitionHelp")}</p>
      <details className="rounded-xl border border-line-subtle bg-surfaceSubtle p-3">
        <summary className="cursor-pointer text-sm font-medium text-text-body">{t("catalog.modules")}</summary>
        <div className="mt-3 flex flex-wrap gap-2">
          {moduleList.map((m) => (
            <span className="rounded-lg border border-line px-2.5 py-1.5 text-xs text-text-muted" key={m.id}>
              {t(`catalog.module.${m.id}`)} {!m.enabled && t("catalog.unavailable")}
            </span>
          ))}
        </div>
      </details>
      <nav className="cv-tabs flex flex-wrap gap-2 rounded-xl border border-line-subtle bg-surfaceSubtle p-2" aria-label={t("catalog.configure")}>
        {(
          ["fields", "vocabularies", "relations", "structure", "templates", "schemes"] as const
        ).map((k) => (
          <button
            key={k}
            className={tab === k ? "cv-primary" : ""}
            onClick={() => setTab(k)}
          >
            {t(`catalog.${k}`)}
          </button>
        ))}
      </nav>
      {tab === "fields" && (
        <Dictionary
          value={d.fields}
          onChange={(fields) => change({ ...d, fields })}
          create={newField}
          render={(v, set) => (
            <FieldDefinition value={v} onChange={set} d={d} />
          )}
        />
      )}
      {tab === "vocabularies" && (
        <Dictionary
          value={d.vocabularies}
          onChange={(vocabularies) => change({ ...d, vocabularies })}
          create={() => ({ names: {}, terms: {} })}
          render={(v, set, vocabularyCode) => (
            <>
              <NamesEditor
                value={v.names}
                onChange={(names) => set({ ...v, names })}
              />
              <Dictionary
                value={v.terms}
                onChange={(terms) => set({ ...v, terms })}
                create={() => ({ names: {}, enabled: true, is_bonus: false })}
                render={(term, change) => (
                  <>
                    <NamesEditor
                      value={term.names}
                      onChange={(names) => change({ ...term, names })}
                    />
                    <label className="cv-check">
                      <input
                        type="checkbox"
                        checked={term.enabled}
                        onChange={(e) =>
                          change({ ...term, enabled: e.target.checked })
                        }
                      />
                      {t("catalog.enabled")}
                    </label>
                    {vocabularyCode === "role" && (
                      <label className="cv-check">
                        <input
                          type="checkbox"
                          checked={term.is_bonus === true}
                          onChange={(e) => change({ ...term, is_bonus: e.target.checked })}
                        />
                        {t("catalog.bonusContent")}
                      </label>
                    )}
                  </>
                )}
              />
            </>
          )}
        />
      )}
      {tab === "relations" && (
        <>
        <section className="rounded-md border border-line p-4 space-y-2">
          <h3 className="font-semibold">{t("catalog.structuralRules")}</h3>
          <p className="text-sm text-text-faint">{t("catalog.structuralRulesHint")}</p>
          <ul className="grid gap-2 md:grid-cols-2">
            {relationshipRules.filter((rule) => rule.read_only).map((rule) => (
              <li key={rule.code} className="rounded border border-line-subtle px-3 py-2 text-sm">
                <strong>{local(rule.names, locale, "", rule.code)}</strong>
                <span className="ml-2 text-text-faint">{t(`catalog.relationshipClass.${rule.class}`)}</span>
                <div className="text-xs text-text-faint">
                  {rule.source_kinds.map((kind) => getKindName(serverKinds, kind, locale, kind)).join(", ")}
                  {" → "}
                  {rule.target_kinds.map((kind) => getKindName(serverKinds, kind, locale, kind)).join(", ")}
                </div>
                <div className="text-xs text-text-faint">
                  {t("catalog.ruleParentLimit")}: {rule.max_incoming || t("catalog.ruleUnbounded")}
                  {rule.scope && <> · {t("catalog.ruleScope")}: {getKindName(serverKinds, rule.scope, locale, rule.scope)}</>}
                  {rule.ordered && <> · {t("catalog.ruleOrdered")}</>}
                </div>
              </li>
            ))}
          </ul>
        </section>
        <Dictionary
          value={d.relations}
          onChange={(relations) => change({ ...d, relations })}
          create={() => ({
            names: {},
            reverse_names: {},
            source_kinds: ["agent"],
            target_kinds: ["work"],
            fields: [],
            symmetric: false,
            acyclic: false,
            aggregate: false,
            participant_slot: "peer",
            counts_as_credit: false,
            max_outgoing: 0,
            max_incoming: 0,
            group: "",
            enabled: true,
          })}
          render={(v, set, relCode) => (
            <>
              <NamesEditor
                value={v.names}
                onChange={(names) => set({ ...v, names })}
              />
              <label>{t("catalog.reverseNames")}</label>
              <NamesEditor
                value={v.reverse_names}
                onChange={(reverse_names) => set({ ...v, reverse_names })}
              />
              {(
                [
                  "source_kinds",
                  "target_kinds",
                  "fields",
                ] as const
              ).map((k) => (
                <Checks
                  key={k}
                  label={t(`catalog.${k}`)}
                  values={
                    k.endsWith("kinds") ? kindNames : names(d.fields)
                  }
                  selected={v[k]}
                  onChange={(values) => set({ ...v, [k]: values })}
                />
              ))}
              <div className="cv-checks">
                {(["enabled", "symmetric", "acyclic", "aggregate"] as const).map((k) => (
                  <label key={k}>
                    <input
                      type="checkbox"
                      checked={v[k] === true}
                      onChange={(e) => set({ ...v, [k]: e.target.checked })}
                    />
                    {t(`catalog.${k}`)}
                  </label>
                ))}
                {/* 署名聚合声明：口径只看它，不看分组码（见 lib/definitions.ts 的
                    RelationDef 注释）。字典键缺省时 tr 给英文回退，四语字典同步后即本地化
                    （messages 不在本文件改动范围，见提交说明）。 */}
                <label>
                  <input
                    type="checkbox"
                    checked={v.counts_as_credit === true}
                    onChange={(e) =>
                      set({ ...v, counts_as_credit: e.target.checked })
                    }
                  />
                  {tr("catalog.countsAsCredit", "Count as credit")}
                </label>
              </div>
              <RelationRulesEditor value={v} onChange={set} definitions={d} />
              <label>
                {tr("catalog.participantSlot", "Participant slot")}
                <select
                  value={v.participant_slot}
                  onChange={(e) =>
                    set({ ...v, participant_slot: e.target.value })
                  }
                >
                  {/* 闭集与后端 Validate 的 invalid_participant_slot 同源。 */}
                  <option value="person">
                    {tr("catalog.participantSlotPerson", "Person / organization")}
                  </option>
                  <option value="character">
                    {tr("catalog.participantSlotCharacter", "Fictional character")}
                  </option>
                  <option value="peer">
                    {tr("catalog.participantSlotPeer", "Peer object (no credit)")}
                  </option>
                </select>
              </label>
              <p className="cv-hint">
                {tr(
                  "catalog.creditDeclHint",
                  "Credit display follows these two declarations, not the display group: moving a relation to another group never changes who counts as a credit."
                )}
              </p>
              {!v.usage && <p className="cv-hint">{t("catalog.aggregateHint")}</p>}
              {/* F04 样例预览：aggregate 开关的目录效果就地可见，与 WorkContentDirectory
                  的 componentEntries(isAggregate) 同口径（aggregate 才收进组成/所属区块）。
                  文案走字典插值，不硬编码关系码与区块名。 */}
              <p className="cv-hint" aria-live="polite">
                {v.usage === "expression_composition" ? t("catalog.compositionPreview")
                  : v.usage === "release_group" ? t("catalog.editionGroupPreview")
                  : v.aggregate === true
                  ? t("catalog.aggregatePreviewOn", {
                      type: relCode,
                      forward: local(v.names, locale, "", relCode),
                    })
                  : t("catalog.aggregatePreviewOff", { type: relCode })}
              </p>
              {(["max_outgoing", "max_incoming"] as const).map((k) => (
                <label key={k}>
                  {t(`catalog.${k}`)}
                  <input
                    type="number"
                    min="0"
                    value={v[k]}
                    onChange={(e) => set({ ...v, [k]: Number(e.target.value) })}
                  />
                </label>
              ))}
              <label>
                {t("catalog.displayGroup")}
                <input
                  value={v.group}
                  onChange={(e) => set({ ...v, group: e.target.value })}
                />
              </label>
              <label>{t("catalog.groupNames")}</label>
              <NamesEditor
                value={v.group_names || {}}
                onChange={(group_names) => set({ ...v, group_names })}
              />
            </>
          )}
        />
        </>
      )}
      {tab === "structure" && (
        <div className="space-y-4">
          <p className="cv-hint">{t("catalog.structuralRulesHint")}</p>
          {Object.entries(d.structure || {}).map(([kind, rule]) => {
            const update = (next: typeof rule) => change({
              ...d,
              structure: { ...d.structure, [kind]: next },
            });
            return (
              <fieldset key={kind} className="rounded-md border border-line p-4 space-y-4">
                <legend className="font-semibold">{kindNames[kind] || kind}</legend>
                <label className="cv-check">
                  <input type="checkbox" checked={rule.resources === true}
                    onChange={(e) => update({ ...rule, resources: e.target.checked })} />
                  {t("entity.detail.resourcesTitle")}
                </label>
                {(rule.fields || []).map((field, index) => (
                  <div key={field.code} className="rounded border border-line-subtle p-3 space-y-2">
                    <strong className="text-sm">{field.code}</strong>
                    <div className="text-xs text-text-faint">
                      {(field.target_kinds?.length ? field.target_kinds : [kind])
                        .map((target) => kindNames[target] || target).join(", ")}
                    </div>
                    <NamesEditor value={field.names || {}} onChange={(names) => update({
                      ...rule, fields: (rule.fields || []).map((item, i) => i === index ? { ...item, names } : item),
                    })} />
                    <label>{t("catalog.reverseNames")}</label>
                    <NamesEditor value={field.reverse_names || {}} onChange={(reverse_names) => update({
                      ...rule, fields: (rule.fields || []).map((item, i) => i === index ? { ...item, reverse_names } : item),
                    })} />
                  </div>
                ))}
                {rule.subjects && <div className="rounded border border-line-subtle p-3 space-y-2">
                  <strong>{t("catalog.subjects")}</strong>
                  <NamesEditor value={rule.subject_names || {}} onChange={(subject_names) => update({ ...rule, subject_names })} />
                  <label>{t("catalog.reverseNames")}</label>
                  <NamesEditor value={rule.subject_reverse_names || {}} onChange={(subject_reverse_names) => update({ ...rule, subject_reverse_names })} />
                </div>}
                {rule.contents && <div className="rounded border border-line-subtle p-3 space-y-2">
                  <strong>{t("catalog.contents")}</strong>
                  <NamesEditor value={rule.content_names || {}} onChange={(content_names) => update({ ...rule, content_names })} />
                  <label>{t("catalog.reverseNames")}</label>
                  <NamesEditor value={rule.content_reverse_names || {}} onChange={(content_reverse_names) => update({ ...rule, content_reverse_names })} />
                </div>}
              </fieldset>
            );
          })}
        </div>
      )}
      {tab === "templates" && (
        <Dictionary
          value={d.templates}
          onChange={(templates) => change({ ...d, templates })}
          create={() => ({
            names: {},
            kinds: [],
            sections: [],
            columns: [],
            relation_groups: [],
            directory: "tree",
            modules: [],
          })}
          render={(v, set) => (
            <>
              <NamesEditor
                value={v.names}
                onChange={(names) => set({ ...v, names })}
              />
              <Checks
                label={t("catalog.allowedKinds")}
                values={kindNames}
                selected={v.kinds || []}
                onChange={(kinds) => set({ ...v, kinds })}
              />
              <TemplateRulesEditor value={v} onChange={set} definitions={d} />
              {v.sections.map((s, i) => (
                <fieldset key={i}>
                  <legend>
                    {t("catalog.section")} {i + 1}
                  </legend>
                  <NamesEditor
                    value={s.names}
                    onChange={(names) =>
                      set({
                        ...v,
                        sections: v.sections.map((x, j) =>
                          i === j ? { ...x, names } : x,
                        ),
                      })
                    }
                  />
                  <TextList
                    label={t("catalog.fieldOrder")}
                    value={s.fields}
                    onChange={(fields) =>
                      set({
                        ...v,
                        sections: v.sections.map((x, j) =>
                          i === j ? { ...x, fields } : x,
                        ),
                      })
                    }
                  />
                  <button
                    type="button"
                    onClick={() =>
                      set({
                        ...v,
                        sections: v.sections.filter((_, j) => i !== j),
                      })
                    }
                  >
                    {t("catalog.remove")}
                  </button>
                </fieldset>
              ))}
              <button
                type="button"
                onClick={() =>
                  set({
                    ...v,
                    sections: [...v.sections, { names: {}, fields: [] }],
                  })
                }
              >
                {t("catalog.addSection")}
              </button>
              <TextList
                label={t("catalog.columns")}
                value={v.columns}
                onChange={(columns) => set({ ...v, columns })}
              />
              <TextList
                label={t("catalog.relationGroups")}
                value={v.relation_groups}
                onChange={(relation_groups) => set({ ...v, relation_groups })}
              />
              <label>
                {t("catalog.directoryMode")}
                <select
                  value={v.directory}
                  onChange={(e) => set({ ...v, directory: e.target.value })}
                >
                  {["tree", "list", "discs"].map((k) => (
                    <option key={k} value={k}>
                      {t(`catalog.directoryModes.${k}`)}
                    </option>
                  ))}
                </select>
              </label>
              <TextList
                label={t("catalog.badgeFields")}
                value={v.badge_fields || []}
                onChange={(badge_fields) => set({ ...v, badge_fields })}
              />
              <TextList
                label={t("catalog.facetFields")}
                value={v.facet_fields || []}
                onChange={(facet_fields) => set({ ...v, facet_fields })}
              />
              <label>
                {t("catalog.primaryDateField")}
                <input
                  value={v.primary_date_field || ""}
                  onChange={(e) =>
                    set({ ...v, primary_date_field: e.target.value })
                  }
                />
              </label>
              <Checks
                label={t("catalog.modules")}
                values={Object.fromEntries(
                  moduleList.map((m) => [m.id, t(`catalog.module.${m.id}`)]),
                )}
                selected={v.modules}
                onChange={(modules) => set({ ...v, modules })}
              />
            </>
          )}
        />
      )}
      {tab === "schemes" && (
        <Dictionary
          value={d.schemes || {}}
          onChange={(schemes) => change({ ...d, schemes })}
          create={() => ({
            names: {},
            slot: "locator",
            kinds: [],
            medium_formats: [],
            fields: [],
            required: [],
            require_range: false,
            enabled: true,
          })}
          render={(v, set) => {
            // 子字段候选：所选 slot 全局组已声明的子字段（顺序即全局声明顺序）。
            const groupFields: Record<string, string> = Object.fromEntries(
              Object.entries((d.fields as any)?.[v.slot]?.fields || {}).map(
                ([k, f]: [string, any]) => [k, local((f as any)?.names, locale, "", k)],
              ),
            );
            return (
              <>
                <NamesEditor
                  value={v.names}
                  onChange={(names) => set({ ...v, names })}
                />
                <label>
                  {t("catalog.schemeSlot")}
                  <select
                    value={v.slot}
                    onChange={(e) =>
                      set({ ...v, slot: e.target.value, fields: [], required: [] })
                    }
                  >
                    {["locator", "inclusion_attributes", "subject_attributes"].map((k) => (
                      <option key={k} value={k}>
                        {t(`catalog.schemeSlotNames.${k}`)}
                      </option>
                    ))}
                  </select>
                </label>
                <Checks
                  label={t("catalog.allowedKinds")}
                  values={kindNames}
                  selected={v.kinds || []}
                  onChange={(kinds) => set({ ...v, kinds })}
                />
                {v.slot !== "subject_attributes" && (!v.kinds?.length || v.kinds.includes("track")) && (
                  <Checks
                    label={t("catalog.schemeMediumFormats")}
                    values={names(d.vocabularies.format?.terms || {})}
                    selected={v.medium_formats || []}
                    onChange={(medium_formats) => set({ ...v, medium_formats })}
                  />
                )}
                <Checks
                  label={t("catalog.schemeFields")}
                  values={groupFields}
                  selected={v.fields || []}
                  onChange={(fields) =>
                    set({
                      ...v,
                      fields,
                      required: (v.required || []).filter((k) => fields.includes(k)),
                    })
                  }
                />
                <Checks
                  label={t("catalog.schemeRequired")}
                  values={groupFields}
                  selected={v.required || []}
                  onChange={(required) => set({ ...v, required })}
                />
                <div className="cv-checks">
                  {(["require_range", "enabled"] as const).map((k) => (
                    <label key={k}>
                      <input
                        type="checkbox"
                        checked={!!(v as any)[k]}
                        onChange={(e) => set({ ...v, [k]: e.target.checked } as any)}
                      />
                      {t(`catalog.${k}`)}
                    </label>
                  ))}
                </div>
              </>
            );
          }}
        />
      )}
      <ChangeNote note={note} setNote={setNote} />
      <div className="cv-row">
        <button onClick={async () => {
          try {
            const result = await api<{ issues: string[] }>("/admin/catalog-definitions/impact", "POST", { document: d });
            setIssues(result.issues);
            setError("");
          } catch (err) {
            setError((err as Error).message);
          }
        }}>{t("catalog.impact")}</button>
        <button className="cv-primary" onClick={async () => {
          try {
            await api("/admin/catalog-definitions", "PUT", {
              document: d, expected_etag: base, edit_note: note, sources: [],
            });
            await reloadPublished();
          } catch (err) {
            setError(localizeCatalogError((err as Error).message, t));
          }
        }}>{t("catalog.save")}</button>
      </div>
      {issues && (
        <section>
          <h2>{t("catalog.impact")}</h2>
          {issues.length ? (
            <ul>
              {issues.map((x) => (
                <li key={x}>{x}</li>
              ))}
            </ul>
          ) : (
            <p>{t("catalog.noIssues")}</p>
          )}
        </section>
      )}
    </div>
  );
}
