import { test } from "node:test";
import assert from "node:assert/strict";
import { downloadAssetFile, StorageRequestError } from "../src/lib/storage";

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
