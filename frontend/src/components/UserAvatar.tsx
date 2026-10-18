"use client";

import React, { useState, useEffect } from "react";
import { displayNameOf } from "@/lib/api";
import { useAuth } from "@/lib/authContext";

export interface UserAvatarProps {
  user?: {
    id?: string;
    username?: string;
    display_name?: string | null;
    avatar_url?: string | null;
  } | null;
  src?: string | null;
  alt?: string;
  size?: "xs" | "sm" | "md" | "lg" | "xl" | "2xl" | "3xl";
  className?: string;
  shape?: "rounded" | "circle" | "square";
  ring?: boolean;
}

const sizeMap = {
  xs: "w-6 h-6 text-xs",
  sm: "w-7 h-7 text-xs font-semibold",
  md: "w-9 h-9 text-sm font-semibold",
  lg: "w-11 h-11 text-base font-bold",
  xl: "w-14 h-14 text-xl font-bold",
  "2xl": "w-20 h-20 text-2xl font-bold",
  "3xl": "w-24 h-24 text-3xl font-bold",
};

const shapeMap = {
  rounded: "rounded-md",
  circle: "rounded-full",
  square: "rounded-none",
};

// 根据用户名生成稳定的柔和渐变/背景色，让无头像的用户色彩丰富
function getFallbackBg(name: string): string {
  if (!name) return "bg-primary text-white";
  const colors = [
    "bg-emerald-600 text-emphasis",
    "bg-sky-600 text-emphasis",
    "bg-indigo-600 text-emphasis",
    "bg-purple-600 text-emphasis",
    "bg-pink-600 text-emphasis",
    "bg-amber-600 text-emphasis",
    "bg-teal-600 text-emphasis",
    "bg-rose-600 text-emphasis",
    "bg-cyan-600 text-emphasis",
  ];
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = name.charCodeAt(i) + ((hash << 5) - hash);
  }
  const index = Math.abs(hash) % colors.length;
  return colors[index];
}

export const UserAvatar: React.FC<UserAvatarProps> = ({
  user,
  src,
  alt,
  size = "md",
  className = "",
  shape = "rounded",
  ring = false,
}) => {
  const [imgError, setImgError] = useState(false);

  const {user:viewer}=useAuth();
  const userID=user?.id;
  const publicAvatar=userID && /^[0-9a-f-]{36}$/i.test(userID) ? `/api/users/${encodeURIComponent(userID)}/avatar` : null;
  const avatarUrl = src ?? (userID===viewer?.id ? viewer?.avatar_url : undefined) ?? user?.avatar_url ?? publicAvatar;
  const username = user?.username || "";
  const name = user ? displayNameOf(user as { username: string; display_name?: string }) : alt || "U";
  const initial = (name ? name.slice(0, 1) : "U").toUpperCase();

  useEffect(() => {
    setImgError(false);
  }, [avatarUrl]);

  const sizeClass = sizeMap[size] || sizeMap.md;
  const shapeClass = shapeMap[shape] || shapeMap.rounded;
  const ringClass = ring ? "ring-2 ring-primary/40 ring-offset-2 ring-offset-surface" : "";

  if (avatarUrl && !imgError) {
    return (
      <div
        className={`relative shrink-0 overflow-hidden select-none dark:bg-white/5 border border-line ${sizeClass} ${shapeClass} ${ringClass} ${className}`}
      >
        <img
          src={avatarUrl}
          alt={alt || name || "User Avatar"}
          className="w-full h-full object-cover shrink-0"
          loading="lazy"
          onError={() => setImgError(true)}
        />
      </div>
    );
  }

  const bgClass = getFallbackBg(username || name);

  return (
    <div
      className={`shrink-0 flex items-center justify-center font-sans font-bold select-none border border-line shadow-2xs keep-white ${bgClass} ${sizeClass} ${shapeClass} ${ringClass} ${className}`}
      title={name}
      aria-label={name}
    >
      <span className="leading-none">{initial}</span>
    </div>
  );
};

export default UserAvatar;
