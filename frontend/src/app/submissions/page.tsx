"use client";
import { Navbar } from "@/components/Navbar";
import { CommitReviewQueue } from "@/components/catalog/CommitReview";
import { PageShell } from "@/components/ui/PageShell";
export default function SubmissionsPage() {
  return <><Navbar/><PageShell width="narrow"><CommitReviewQueue/></PageShell></>;
}
