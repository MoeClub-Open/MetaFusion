import { Navbar } from "@/components/Navbar";
import { CommitDetail } from "@/components/collaboration/CommitDetail";
import { PageShell } from "@/components/ui/PageShell";

export default async function CommitPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <><Navbar/><PageShell><CommitDetail id={id}/></PageShell></>;
}
