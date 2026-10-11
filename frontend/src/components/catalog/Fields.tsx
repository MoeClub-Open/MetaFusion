"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { api, Entity, Field, local, Names, Source, title } from "./api";
import { getKindName, useDefinitions } from "@/lib/definitions";
import { CORE_LOCALE_CODES } from "@/lib/languages";
import { entityIdentitySuffix } from "@/lib/entityIdentity";
import { Select } from "@/components/ui/Select";
export function NamesEditor({
  value,
  onChange,
}: {
  value: Names;
  onChange: (v: Names) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="cv-grid">
      {/* 命名四语来自语言单一来源（与后端 four_locale_names_required 同口径） */}
      {CORE_LOCALE_CODES.map((loc) => (
        <label key={loc}>
          {t(`catalog.${loc}`)}
          <input
            value={value?.[loc] || ""}
            onChange={(e) => onChange({ ...value, [loc]: e.target.value })}
          />
        </label>
      ))}
    </div>
  );
}
export function EntityPicker({
  value,
  onChange,
  kinds,
  query = "",
}: {
  value: string;
  onChange: (id: string) => void;
  kinds?: string[];
  query?: string;
}) {
  const { t, tr, locale } = useI18n();
  const { kinds: serverKinds } = useDefinitions();
  // 候选行的层级名：服务端 definitions.kinds 优先，字典只作兜底（缺键退原始码）。
  const kindLabel = (code: string) =>
    getKindName(serverKinds, code, locale, tr(`catalog.kind.${code}`, code));
  const [search, setSearch] = useState("");
  const [items, setItems] = useState<Entity[]>([]);
  const [selected, setSelected] = useState<Entity>();
  const [error, setError] = useState(false);
  useEffect(() => {
    let active = true;
    if (!value) {
      setSelected(undefined);
      return;
    }
    api<Entity>(`/catalog/entities/${value}`)
      .then((x) => {
        if (active) setSelected(x);
      })
      .catch(() => {
        if (active) setSelected(undefined);
      });
    return () => {
      active = false;
    };
  }, [value]);
  useEffect(() => {
    let active = true;
    setItems([]);
    const timer = setTimeout(() => {
      // kind 约束下沉到查询，避免前端截断合法候选。
      const qs = new URLSearchParams({ q: search, limit: "30" });
      if (kinds?.length === 1) qs.set("kind", kinds[0]);
      else if (kinds && kinds.length > 1) qs.set("kinds", kinds.join(","));
      api<{ items: Entity[] }>(`/catalog/entities?${qs}${query}`)
        .then((r) => {
          if (active) {
            setItems(r.items.filter((x) => !kinds?.length || kinds.includes(x.kind)));
            setError(false);
          }
        })
        .catch(() => {
          if (active) { setItems([]); setError(true); }
        });
    }, 200);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [search, JSON.stringify(kinds), query]);
  return (
    <div className="cv-picker">
      <input
        aria-label={t("catalog.searchEntity")}
        value={search}
        placeholder={
          selected ? title(selected, locale) : t("catalog.searchEntity")
        }
        onChange={(e) => setSearch(e.target.value)}
      />
      <Select
        aria-label={t("catalog.selectEntity")}
        value={value || ""}
        onChange={onChange}
        options={[
          { value: "", label: t("catalog.none") },
          ...(selected && !items.some((x) => x.id === selected.id)
            ? [{ value: selected.id || "", label: `${title(selected, locale)} · ${kindLabel(selected.kind)} · ${entityIdentitySuffix(selected)}` }]
            : []),
          ...items.map((x) => ({
            value: x.id || "",
            label: `${title(x, locale)} · ${kindLabel(x.kind)} · ${entityIdentitySuffix(x)}`,
          })),
        ]}
      />
      {error && (
        <small className="cv-error">{t("catalog.connectionError")}</small>
      )}
    </div>
  );
}
// GroupFieldInput：按字段码渲染一个 group 字段的子字段表单（值形如 {子字段码: 值}）。
// 专供"记录级附加属性"这类没有独立列、完全靠 definitions 声明的落点：
// subject_attributes（发行对象附加属性）、inclusion_attributes（收录附加属性）。
// 后台未声明任何子字段时不出表单——不发明字段，也不在前端写死子字段码。
export function GroupFieldInput({
  defs,
  code,
  value,
  onChange,
  codes,
}: {
  defs: any;
  code: string;
  value: Record<string, any> | undefined;
  onChange: (v: Record<string, any>) => void;
  /** 可选收敛：只显示这些子字段码（顺序即展示顺序）；缺省显示全部全局子字段。 */
  codes?: string[];
}) {
  const { locale } = useI18n();
  const field = defs?.fields?.[code];
  if (!field || field.type !== "group") return null;
  const order = codes && codes.length > 0 ? codes : Object.keys(field.fields || {});
  // 不按 hidden 过滤：hidden 只控制详情面板展示，编辑面必须能维护
  // hidden + required 的子字段，否则该实体永远无法保存。
  const entries = order
    .map((k): [string, any] => [k, (field.fields || {})[k]])
    .filter(([, f]: [string, any]) => f && f?.enabled !== false);
  if (entries.length === 0) return null;
  const current = value || {};
  return (
    <div className="cv-grid">
      {entries.map(([k, f]: [string, any]) => (
        <label key={k}>
          {local(f.names, locale, "", k)}
          <FieldInput
            field={f}
            value={current[k]}
            onChange={(v) => {
              const next = { ...current };
              if (v === "" || v === undefined || v === null) delete next[k];
              else next[k] = v;
              onChange(next);
            }}
          />
        </label>
      ))}
    </div>
  );
}

// 日期字段允许三种精度：年、年-月、年-月-日，与后端 catalog.Value 的 date 校验同一口径。
// 原生 date 控件只接受完整年月日，会把"2026"补成"2026-01-01"——日期精度会被就地改写，
// 因此这里用带格式校验的文本控件，未知月份/日期不再被凭空补造。
const DATE_PATTERN = /^\d{4}(-\d{2}(-\d{2})?)?$/;
const DAYS_IN_MONTH = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];

/** isValidDateValue 判断字符串是否为合法的年 / 年-月 / 年-月-日。 */
export function isValidDateValue(v: string): boolean {
  const s = v.trim();
  if (!DATE_PATTERN.test(s)) return false;
  const [y, m, d] = s.split("-").map(Number);
  if (y < 1) return false;
  if (m !== undefined && (m < 1 || m > 12)) return false;
  if (d !== undefined) {
    let last = DAYS_IN_MONTH[m - 1];
    if (m === 2 && ((y % 4 === 0 && y % 100 !== 0) || y % 400 === 0)) last = 29;
    if (d < 1 || d > last) return false;
  }
  return true;
}

function DateInput({ value, onChange }: { value: any; onChange: (v: any) => void }) {
  const { t } = useI18n();
  const text = value === null || value === undefined ? "" : String(value);
  const invalid = text.trim() !== "" && !isValidDateValue(text);
  return (
    <>
      <input
        className="cv-date"
        type="text"
        inputMode="numeric"
        placeholder={t("catalog.datePlaceholder")}
        aria-invalid={invalid || undefined}
        value={text}
        onChange={(e) => onChange(e.target.value)}
      />
      {invalid && <small className="cv-error">{t("catalog.dateInvalid")}</small>}
    </>
  );
}

export function FieldInput({
  field,
  value,
  onChange,
}: {
  field?: Field;
  value: any;
  onChange: (v: any) => void;
}) {
  const { definitions } = useDefinitions();
  const { t, locale } = useI18n();
  if (!definitions) return null;
  // 字段/词表定义退役后，历史属性可能仍存在。保留原值并允许 EntityEditor 删除，
  // 避免编辑页崩溃或静默丢弃数据。
  if (!field)
    return (
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md border border-line bg-surfaceSubtle p-2 text-xs text-text-muted">
        {JSON.stringify(value, null, 2) ?? String(value)}
      </pre>
    );
  if (field.type === "entity")
    return (
      <EntityPicker
        kinds={field.kinds}
        value={value || ""}
        onChange={onChange}
      />
    );
  if (field.type === "boolean")
    return (
      <input
        type="checkbox"
        checked={!!value}
        onChange={(e) => onChange(e.target.checked)}
      />
    );
  if (field.type === "multilingual")
    return <NamesEditor value={value || {}} onChange={onChange} />;
  if (field.type === "enum")
    return (
      <Select
        value={value || ""}
        onChange={onChange}
        options={[
          { value: "", label: t("catalog.none") },
          ...Object.entries(
            definitions.vocabularies[field.vocabulary || ""]?.terms || {},
          )
            .filter(([k, v]) => v.enabled || k === value)
            .map(([k, v]) => ({
              value: k,
              label: local(v.names, locale, "", k),
            })),
        ]}
      />
    );
  if (field.type === "group")
    return (
      <div className="cv-group">
        {/* 同 GroupFieldInput：hidden 只影响详情面板，嵌套编辑不隐藏字段。 */}
        {Object.entries(field.fields || {})
          .filter(([, f]) => f?.enabled !== false)
          .map(([k, f]) => (
            <label key={k}>
              {local(f.names, locale, "", k)}
              <FieldInput
                field={f}
                value={value?.[k]}
                onChange={(v) => onChange({ ...value, [k]: v })}
              />
            </label>
          ))}
      </div>
    );
  if (field.type === "list" && field.items)
    return (
      <div className="cv-group">
        {(value || []).map((v: any, i: number) => (
          <div key={i} className="cv-row">
            <FieldInput
              field={field.items!}
              value={v}
              onChange={(x) =>
                onChange(value.map((a: any, j: number) => (i === j ? x : a)))
              }
            />
            <button
              type="button"
              onClick={() =>
                onChange(value.filter((_: any, j: number) => i !== j))
              }
            >
              {t("catalog.remove")}
            </button>
          </div>
        ))}
        <button
          type="button"
          onClick={() =>
            onChange([
              ...(value || []),
              field.items?.type === "group" ||
              field.items?.type === "multilingual"
                ? {}
                : "",
            ])
          }
        >
          {t("catalog.add")}
        </button>
      </div>
    );
  if (field.type === "date") return <DateInput value={value} onChange={onChange} />;
  return (
    <input
      type={
        field.type === "number" ? "number" : field.type === "url" ? "url" : "text"
      }
      min={field.min}
      max={field.max}
      step="any"
      value={value ?? ""}
      onChange={(e) =>
        onChange(
          field.type === "number"
            ? e.target.value === ""
              ? null
              : Number(e.target.value)
            : e.target.value,
        )
      }
    />
  );
}
export function EntityLink({ id, fallback }: { id: string; fallback?: string }) {
  const [e, setE] = useState<Entity>();
  const [failed, setFailed] = useState(false);
  const { locale, t } = useI18n();
  useEffect(() => {
    let active = true;
    setFailed(false);
    api<Entity>(`/catalog/entities/${id}/resolve`)
      .then((x) => {
        if (active) setE(x);
      })
      .catch(() => {
        if (active) setFailed(true);
      });
    return () => {
      active = false;
    };
  }, [id]);
  // 解析不到（引用已失效、或该 id 不可见）时显示可读占位而不是裸 UUID，也不留成死链：
  // 指向一个取不回来的实体的链接只会把用户带到下一个错误页。
  if (failed && !e) {
    return <span className="text-text-muted">{fallback || t("catalog.referenceUnknown")}</span>;
  }
  return (
    <Link href={`/catalog/${e?.id || id}`} className="text-primary hover:underline">
      {e ? title(e, locale) : t("catalog.entityReference")}
    </Link>
  );
}
export function Evidence({
  note,
  setNote,
  sources,
  setSources,
}: {
  note: string;
  setNote: (s: string) => void;
  sources: Source[];
  setSources: (s: Source[]) => void;
}) {
  const { t } = useI18n();
  return (
    <fieldset>
      <legend>{t("catalog.evidence")}</legend>
      <p className="text-xs text-text-muted">{t("catalog.evidenceSourceHint")}</p>
      <label>
        {t("catalog.editNote")}
        <textarea
          required
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </label>
      {sources.map((s, i) => (
        <div className="cv-group" key={i}>
          <Select
            aria-label={t("catalog.sourceKind")}
            value={s.kind}
            onChange={(v) =>
              setSources(
                sources.map((x, j) =>
                  i === j ? { ...x, kind: v } : x,
                ),
              )
            }
            options={[...(!["url", "publication"].includes(s.kind) ? [s.kind] : []), "url", "publication"].map((k) => ({
              value: k,
              label: t(`catalog.source.${k}`),
              disabled: k !== "url" && k !== "publication",
            }))}
          />
          <input
            required
            aria-label={t("catalog.citation")}
            placeholder={t("catalog.citation")}
            value={s.citation}
            onChange={(e) =>
              setSources(
                sources.map((x, j) =>
                  i === j ? { ...x, citation: e.target.value } : x,
                ),
              )
            }
          />
          <input
            type="url"
            aria-label={t("catalog.sourceUrl")}
            placeholder={t("catalog.sourceUrl")}
            required={s.kind === "url"}
            value={s.url || ""}
            onChange={(e) =>
              setSources(
                sources.map((x, j) =>
                  i === j ? { ...x, url: e.target.value } : x,
                ),
              )
            }
          />
          {sources.length > 1 && (
            <button
              type="button"
              onClick={() => setSources(sources.filter((_, j) => i !== j))}
            >
              {t("catalog.remove")}
            </button>
          )}
        </div>
      ))}
      <button
        type="button"
        onClick={() => setSources([...sources, { kind: "url", citation: "" }])}
      >
        {t("catalog.addSource")}
      </button>
    </fieldset>
  );
}
export function ErrorMessage({ error }: { error: string }) {
  const { t } = useI18n();
  return error ? (
    <p role="alert" className="cv-error">
      {t("catalog.requestError")} <code>{error}</code>
    </p>
  ) : null;
}
