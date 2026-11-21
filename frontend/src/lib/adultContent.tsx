"use client";

import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import { useAuth } from "./authContext";
import { fetchApi } from "./api";
import { ADULT_MODE_CACHE_PREFIX, HOME_FEED_CACHE_PREFIX } from "./sessionHint";
import { tagCode } from "./definitions";

export type AdultContentMode = "hide" | "blur" | "show";

const ADULT_TAGS = new Set(["r18", "18+", "adult"]);

export function isAdultEntity(entity?: { kind?: string; attributes?: Record<string, any> | null } | null): boolean {
  if (!entity?.attributes) return false;
  const rating = String(entity.attributes.content_rating || "").trim().toLowerCase();
  if (rating === "r18") return true;
  const form = String(entity.attributes.creation_form || "").trim().toLowerCase();
  if (form === "eroge") return true;
  if (Array.isArray(entity.attributes.tags)) {
    for (const raw of entity.attributes.tags) {
      const code = tagCode(raw).trim().toLowerCase();
      if (ADULT_TAGS.has(code)) return true;
    }
  }
  return false;
}

interface ContentPreferenceContextValue {
  mode: AdultContentMode;
  loading: boolean;
  loadFailed: boolean;
  refresh: () => Promise<void>;
  updateMode: (next: AdultContentMode) => Promise<AdultContentMode>;
}

const ContentPreferenceContext = createContext<ContentPreferenceContextValue>({
  mode: "blur",
  loading: false,
  loadFailed: false,
  refresh: async () => {},
  updateMode: async (next) => next,
});

function normalizeMode(raw: unknown): AdultContentMode {
  if (raw === "hide" || raw === "blur" || raw === "show") return raw;
  return "blur";
}

function readCachedAdultMode(userId: string): AdultContentMode | null {
  if (!userId || typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(ADULT_MODE_CACHE_PREFIX + userId);
    if (raw === "hide" || raw === "blur" || raw === "show") return raw;
  } catch {}
  return null;
}

function writeCachedAdultMode(userId: string, mode: AdultContentMode): void {
  if (!userId || typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(ADULT_MODE_CACHE_PREFIX + userId, mode);
  } catch {}
}

export function ContentPreferenceProvider({ children }: { children: React.ReactNode }) {
  const { user } = useAuth();
  const userId = user?.id || "";
  const [mode, setMode] = useState<AdultContentMode>("blur");
  const [loading, setLoading] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);

  const refresh = useCallback(async () => {
    if (!userId) {
      setMode("blur");
      setLoading(false);
      setLoadFailed(false);
      return;
    }
    const cached = readCachedAdultMode(userId);
    if (cached) {
      setMode(cached);
      setLoading(false);
    } else {
      setLoading(true);
    }
    setLoadFailed(false);
    try {
      const data = await fetchApi<{ adult_content_mode?: string }>("/catalog/me/content-preferences");
      const resolved = normalizeMode(data?.adult_content_mode);
      setMode(resolved);
      writeCachedAdultMode(userId, resolved);
    } catch {
      if (!cached) setMode("blur");
      setLoadFailed(true);
    } finally {
      setLoading(false);
    }
  }, [userId]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const updateMode = useCallback(async (next: AdultContentMode) => {
    const res = await fetchApi<{ adult_content_mode?: string }>("/catalog/me/content-preferences", {
      method: "PUT",
      body: JSON.stringify({ adult_content_mode: next }),
    });
    const resolved = normalizeMode(res?.adult_content_mode ?? next);
    setMode(resolved);
    writeCachedAdultMode(userId, resolved);
    if (typeof window !== "undefined") {
      try {
        window.sessionStorage.removeItem(`${HOME_FEED_CACHE_PREFIX}auth`);
      } catch {}
    }
    setLoadFailed(false);
    return resolved;
  }, [userId]);

  return (
    <ContentPreferenceContext.Provider value={{ mode, loading, loadFailed, refresh, updateMode }}>
      {children}
    </ContentPreferenceContext.Provider>
  );
}

export const useAdultContentMode = () => useContext(ContentPreferenceContext);
