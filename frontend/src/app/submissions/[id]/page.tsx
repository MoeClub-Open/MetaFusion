"use client";
import { use } from "react";
import { CommitReview } from "@/components/catalog/CommitReview";
import { Navbar } from "@/components/Navbar";
import { PageShell } from "@/components/ui/PageShell";
export default function SubmissionPage({params}: {params:Promise<{id:string}>}) {
  const {id} = use(params);
  return <><Navbar/><PageShell><CommitReview id={id}/></PageShell></>;
}
