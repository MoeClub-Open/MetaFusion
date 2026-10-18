import { Navbar } from "@/components/Navbar";
import { WorkspaceList } from "@/components/collaboration/WorkspaceList";
import { PageShell } from "@/components/ui/PageShell";

export default function WorkspacesPage() {
  return <><Navbar/><PageShell width="narrow"><WorkspaceList/></PageShell></>;
}
