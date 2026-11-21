"use client";
import React, { createContext, useContext, useEffect, useState } from "react";
import { api, Capability } from "./api";
import { fetchSetupStatus } from "@/lib/api";
import { useI18n } from "@/i18n/I18nProvider";

const CAPABILITIES_CACHE_KEY = "mf_capabilities_v1";
const CAPABILITIES_TTL_MS = 5 * 60_000;
let cachedModules: Capability[] | null = null;
let cachedModulesAt = 0;
let capabilitiesInflight: Promise<Capability[]> | null = null;

function readStoredCapabilities(): Capability[] | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(CAPABILITIES_CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { modules?: Capability[]; savedAt?: number };
    if (Array.isArray(parsed?.modules) && typeof parsed?.savedAt === "number" && Date.now() - parsed.savedAt < CAPABILITIES_TTL_MS) {
      return parsed.modules;
    }
  } catch {}
  return null;
}

async function loadCapabilitiesDedup(force = false): Promise<Capability[]> {
  if (!force) {
    if (cachedModules && Date.now() - cachedModulesAt < CAPABILITIES_TTL_MS) {
      return cachedModules;
    }
    const stored = readStoredCapabilities();
    if (stored) {
      cachedModules = stored;
      cachedModulesAt = Date.now();
      return stored;
    }
  }
  if (!capabilitiesInflight) {
    capabilitiesInflight = api<{ modules: Capability[] }>("/capabilities")
      .then((res) => {
        const list = Array.isArray(res?.modules) ? res.modules : [];
        cachedModules = list;
        cachedModulesAt = Date.now();
        if (typeof window !== "undefined") {
          try {
            window.sessionStorage.setItem(CAPABILITIES_CACHE_KEY, JSON.stringify({ modules: list, savedAt: cachedModulesAt }));
          } catch {}
        }
        return list;
      })
      .finally(() => {
        capabilitiesInflight = null;
      });
  }
  return capabilitiesInflight;
}

// 目录页的共享上下文只保留非定义职责：模块开关与实例初始化状态。
// 定义（fields/relations/vocabularies…）的唯一来源是 lib/definitions.ts 的
// useDefinitions()（带版本缓存、订阅与发布后失效）；会话用户来自 lib/authContext 的
// useAuth()。这两样此前在这里各存了一份 React state，于是同一份数据有了第二份缓存：
// 后台发布新定义后，只有拿到 Provider 那一份的组件会变，没挂 Provider 的路由
// （例如对比页或发行版布局）则连字段名都取不到，只能显示裸字段码。
const Context = createContext<{
  modules: Capability[];
  setup: boolean;
  refresh: () => Promise<void>;
}>({ modules: [], setup: false, refresh: async () => {} });

export const useCatalog = () => useContext(Context);

export function CatalogProvider({ children }: { children: React.ReactNode }) {
  const { t } = useI18n();
  const [modules, setModules] = useState<Capability[]>([]);
  const [setup, setSetup] = useState(false);
  const [error, setError] = useState("");

  const refresh = async (force = false) => {
    const results = await Promise.allSettled([
      loadCapabilitiesDedup(force),
      fetchSetupStatus(),
    ]);
    if (results[0].status === "fulfilled") {
      setModules(results[0].value);
      setError("");
    } else setError((results[0].reason as Error).message);
    setSetup(results[1].status === "fulfilled" && !results[1].value.is_initialized);
  };

  useEffect(() => {
    void refresh(false);
  }, []);

  return (
    <Context.Provider value={{ modules, setup, refresh: () => refresh(true) }}>
      {error && (
        <div role="alert" className="p-3 bg-red-500/10 border border-red-500/20 text-danger text-xs font-mono text-center">
          {t("catalog.connectionError")}
          <button onClick={() => void refresh(true)} className="ml-2 underline">{t("catalog.retry")}</button>
        </div>
      )}
      {children}
    </Context.Provider>
  );
}
