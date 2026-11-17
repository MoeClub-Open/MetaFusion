"use client";

import React from "react";
import { AuthProvider } from "@/lib/authContext";
import { ContentPreferenceProvider } from "@/lib/adultContent";
import { AuthGate } from "./AuthGate";

export function ApplicationBoundary({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <AuthProvider>
      <ContentPreferenceProvider>
        <AuthGate>
          {children}
        </AuthGate>
      </ContentPreferenceProvider>
    </AuthProvider>
  );
}
