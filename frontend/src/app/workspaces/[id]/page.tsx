"use client";

import { use } from "react";
import { Navbar } from "@/components/Navbar";
import { PageShell } from "@/components/ui/PageShell";
import { WorkspaceDetail } from "@/components/collaboration/WorkspaceDetail";

export default function WorkspacePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  return <><Navbar /><PageShell><WorkspaceDetail id={id} /></PageShell></>;
}
