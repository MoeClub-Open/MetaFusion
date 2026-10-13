import { PageShell } from "@/components/ui/PageShell";
import { EntityLoadError } from "@/components/catalog/EntityLoadError";

// page.tsx 判定条目不存在时调用 notFound()，这里渲染并返回真正的 HTTP 404。
export default function CatalogEntityNotFound() {
  return (
    <div className="min-h-screen bg-background relative flex flex-col overflow-clip">
      <PageShell width="narrow" center>
        <EntityLoadError kind="not-found" />
      </PageShell>
    </div>
  );
}
