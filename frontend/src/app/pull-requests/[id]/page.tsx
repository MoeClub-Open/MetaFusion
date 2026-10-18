"use client";

import { use } from "react";
import { Navbar } from "@/components/Navbar";
import { PullRequestDetail } from "@/components/catalog/PullRequestReview";
import { PageShell } from "@/components/ui/PageShell";

export default function PullRequestPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = use(params);
  return <><Navbar /><PageShell><PullRequestDetail key={id} id={id} /></PageShell></>;
}
