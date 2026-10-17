"use client";

import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import { User, clearAuthTokens, normalizeSessionUser, type PrivacySettings } from "./api";
import { clearSessionHint, hasSessionHint } from "./sessionHint";

interface AuthContextType {
  user: User | null;
  loading: boolean;
  logout: () => void;
  refreshProfile: () => Promise<void>;
  syncPrivacySettings: (userId: string, settings: PrivacySettings) => void;
}

const AuthContext = createContext<AuthContextType>({
  user: null,
  loading: true,
  logout: () => {},
  refreshProfile: async () => {},
  syncPrivacySettings: () => {},
});

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  const syncPrivacySettings = useCallback((userId: string, settings: PrivacySettings) => {
    setUser((current) => current?.id === userId ? { ...current, ...settings } : current);
  }, []);

  const logout = async () => {
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        credentials: "same-origin",
      });
    } catch {}
    clearAuthTokens();
    clearSessionHint();
    setUser(null);
  };

  const refreshProfile = async () => {
    try {
      const res = await fetch("/api/auth/me", {
        credentials: "same-origin",
      });
      if (!res.ok) {
        throw new Error("unauthorized");
      }
      const u = await res.json();
      setUser(normalizeSessionUser(u));
    } catch {
      clearAuthTokens();
      clearSessionHint();
      setUser(null);
    }
  };

  useEffect(() => {
    // 旧版主站曾把 Bearer 留在 localStorage；账号应用现在只更新 Cookie。
    // 先删旧凭据，再从 /me 读取同域 Cookie，避免两个账号的身份发生冲突。
    clearAuthTokens();
    // 没有会话提示 = 浏览器里没有 mf_session，直接按未登录处理，不发注定 401 的请求。
    if (!hasSessionHint()) {
      setUser(null);
      setLoading(false);
      return;
    }
    fetch("/api/auth/me", {
      credentials: "same-origin",
    })
      .then((res) => {
        if (!res.ok) throw new Error("unauthorized");
        return res.json();
      })
      .then((u) => {
        // 账号服务在 /me 里给了组与权限码，映射统一在 normalizeSessionUser 里做，
        // 免得这里与登录路径各写一份、其中一份漏字段。
        setUser(normalizeSessionUser(u));
      })
      .catch(() => {
        // 会话已失效：清掉提示，后续页面不再重复探测。
        clearSessionHint();
        setUser(null);
      })
      .finally(() => {
        setLoading(false);
      });
  }, []);

  return (
    <AuthContext.Provider value={{ user, loading, logout, refreshProfile, syncPrivacySettings }}>
      {children}
    </AuthContext.Provider>
  );
};

export const useAuth = () => useContext(AuthContext);
