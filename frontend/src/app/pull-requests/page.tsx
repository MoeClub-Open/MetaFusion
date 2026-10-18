"use client";

import { Navbar } from "@/components/Navbar";
import { PullRequestQueue } from "@/components/catalog/PullRequestReview";
import { PageShell } from "@/components/ui/PageShell";

export default function PullRequestsPage() {
  return <><Navbar /><PageShell width="narrow"><PullRequestQueue /></PageShell></>;
}
