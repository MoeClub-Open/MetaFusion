import { test } from "node:test";
import assert from "node:assert/strict";
import { downloadAssetFile, storageErrorKey, StorageRequestError, uploadCatalogImage } from "../src/lib/storage";

async function withDownloadBrowser(response: Response, check: (state: {
  opened: string[];
  blobs: Blob[];
  saved: { href: string; download: string }[];
}) => Promise<void>) {
  const originalFetch = globalThis.fetch;
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");
  const originalCreate = URL.createObjectURL;
  const state = { opened: [] as string[], blobs: [] as Blob[], saved: [] as { href: string; download: string }[] };
  globalThis.fetch = (async (_input, options) => {
    assert.equal(options?.credentials, "same-origin");
    return response;
  }) as typeof fetch;
  Object.defineProperty(globalThis, "window", { configurable: true, value: {
    open: (url: string) => { state.opened.push(url); },
    setTimeout: () => 0,
  } });
  Object.defineProperty(globalThis, "document", { configurable: true, value: {
    body: { appendChild: () => {} },
    createElement: () => {
      const link = { href: "", download: "", click: () => { state.saved.push({ href: link.href, download: link.download }); }, remove: () => {} };
      return link;
    },
  } });
  URL.createObjectURL = (blob) => {
    assert.ok(blob instanceof Blob);
    state.blobs.push(blob);
    return "blob:test-download";
  };
  try {
    await check(state);
  } finally {
    globalThis.fetch = originalFetch;
    URL.createObjectURL = originalCreate;
    if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
    else Reflect.deleteProperty(globalThis, "window");
    if (originalDocument) Object.defineProperty(globalThis, "document", originalDocument);
    else Reflect.deleteProperty(globalThis, "document");
  }
}

test("JSON attachments are saved unchanged and cannot control download navigation", async () => {
  for (const content of ['{"download_url":"https://attacker.invalid/phishing"}', "null", "[1,2,3]"]) {
    const response = new Response(content, { headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Content-Disposition": 'attachment; filename="catalog.json"',
    } });
    await withDownloadBrowser(response, async (state) => {
      assert.deepEqual(await downloadAssetFile("asset", "catalog.json"), { mode: "stream" });
      assert.deepEqual(state.opened, []);
      assert.equal(await state.blobs[0]?.text(), content);
      assert.deepEqual(state.saved, [{ href: "blob:test-download", download: "catalog.json" }]);
    });
  }
});

test("presigned download metadata still opens the signed URL", async () => {
  const url = "https://objects.example.test/file?X-Amz-Signature=test";
  await withDownloadBrowser(Response.json({ download_url: url }), async (state) => {
    assert.deepEqual(await downloadAssetFile("asset", "file.bin"), { mode: "presigned" });
    assert.deepEqual(state.opened, [url]);
    assert.deepEqual(state.saved, []);
  });
});

test("malformed download metadata fails without navigation or a saved file", async () => {
  for (const data of [null, {}, { download_url: "" }, { download_url: "javascript:alert(1)" }]) {
    await withDownloadBrowser(Response.json(data), async (state) => {
      await assert.rejects(downloadAssetFile("asset", "file.bin"), (error) => error instanceof StorageRequestError);
      assert.deepEqual(state.opened, []);
      assert.deepEqual(state.saved, []);
    });
  }
});

test("storageErrorKey maps visual-asset-only and unsupported media errors explicitly", () => {
  assert.deepEqual(storageErrorKey(new StorageRequestError("catalog_assets_only", 403)), {
    key: "storage.files.errVisualOnly",
  });
  assert.deepEqual(storageErrorKey(new StorageRequestError("unsupported_media_type", 415)), {
    key: "storage.files.errUnsupportedMedia",
  });
});

test("uploadCatalogImage completes instant upload and falls back to stream upload on presigned CORS failure", async () => {
  const assetId = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
  const file = new File([new Uint8Array([137, 80, 78, 71])], "scan.png", { type: "image/png" });
  const originalFetch = globalThis.fetch;
  const originalXHR = (globalThis as { XMLHttpRequest?: unknown }).XMLHttpRequest;
  const putCalls: string[] = [];
  let instant = true;
  globalThis.fetch = (async (input, init) => {
    const url = String(input);
    if (url.endsWith("/storage/upload/initiate")) {
      const body = JSON.parse(String(init?.body || "{}"));
      assert.equal(body.binding_role, "cover_image");
      return Response.json(instant ? {
        is_instant_upload: true,
        asset_id: assetId.toUpperCase(),
        object_key: "k",
        expires_at: "2026-12-01T00:00:00Z",
      } : {
        is_instant_upload: false,
        asset_id: assetId,
        upload_id: "u1",
        presigned_urls: ["https://s3.example.test/presigned"],
        object_key: "k",
        expires_at: "2026-12-01T00:00:00Z",
      });
    }
    if (url.endsWith("/storage/upload/complete")) {
      return Response.json({ asset: { id: assetId, status: "complete" } });
    }
    throw new Error(`unexpected fetch: ${url}`);
  }) as typeof fetch;
  class FakeXHR {
    url = "";
    status = 200;
    responseText = "";
    upload: { onprogress?: (ev: { lengthComputable: boolean; loaded: number; total: number }) => void } = {};
    onload?: () => void;
    onerror?: () => void;
    onabort?: () => void;
    open(_method: string, url: string) { this.url = url; }
    setRequestHeader() {}
    abort() { this.onabort?.(); }
    send() {
      putCalls.push(this.url);
      if (this.url.startsWith("https://s3.example.test/")) {
        this.onerror?.();
        return;
      }
      this.upload.onprogress?.({ lengthComputable: true, loaded: 2, total: 4 });
      this.status = 200;
      this.onload?.();
    }
  }
  (globalThis as { XMLHttpRequest?: unknown }).XMLHttpRequest = FakeXHR;
  try {
    const r1 = await uploadCatalogImage(file);
    assert.deepEqual(r1, { assetId, url: `/api/storage/assets/${assetId}/content` });
    assert.deepEqual(putCalls, []);

    instant = false;
    const progress: number[] = [];
    const r2 = await uploadCatalogImage(file, { onProgress: (p) => progress.push(p) });
    assert.deepEqual(r2, { assetId, url: `/api/storage/assets/${assetId}/content` });
    assert.deepEqual(putCalls, ["https://s3.example.test/presigned", `/api/storage/upload/stream/${assetId}`]);
    assert.deepEqual(progress, [50, 100]);
  } finally {
    globalThis.fetch = originalFetch;
    if (originalXHR === undefined) Reflect.deleteProperty(globalThis, "XMLHttpRequest");
    else (globalThis as { XMLHttpRequest?: unknown }).XMLHttpRequest = originalXHR;
  }
});
