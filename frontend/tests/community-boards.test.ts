import { test } from "node:test";
import assert from "node:assert/strict";
import { ALL_FORUM_BOARD, fetchBoards, getBoardSync, shareContent } from "../src/lib/api/community";

test("boards expose request and response failures without inventing board definitions", async () => {
  const originalFetch = globalThis.fetch;
  let response: unknown = [];
  let status = 200;
  globalThis.fetch = (async () => {
    if (response instanceof Error) throw response;
    return Response.json(response, { status });
  }) as typeof fetch;
  try {
    const realBoard = {
      code: "actual_board", names: { "en-US": "Actual board" }, descriptions: {},
      color: "emerald", icon: "BookOpen", sort_order: 10, is_enabled: true, show_in_feed: false,
    };
    response = [realBoard];
    const boards = await fetchBoards({ force: true });
    assert.deepEqual(boards.map((board) => board.code), ["all", "actual_board"]);
    assert.equal(getBoardSync("actual_board", boards)?.show_in_feed, false);
    assert.equal(getBoardSync("announcement", boards), undefined);

    response = { error: "module_error" };
    status = 503;
    await assert.rejects(fetchBoards({ force: true }), /module_error/);
    status = 200;
    response = new Error("offline");
    await assert.rejects(fetchBoards({ force: true }), /offline/);

    for (const malformed of [null, {}, { items: [] }, [null], [{ ...realBoard, code: "" }], [{ ...realBoard, is_enabled: undefined }]]) {
      response = malformed;
      await assert.rejects(fetchBoards({ force: true }), /invalid_response: boards/);
    }
    response = [];
    assert.deepEqual(await fetchBoards({ force: true }), [ALL_FORUM_BOARD]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("sharing without supported browser capabilities reports failure", async () => {
  const navigatorDescriptor = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: {} });
  try {
    assert.equal(await shareContent({ title: "Topic", url: "https://example.invalid/community/topic" }), "failed");
  } finally {
    if (navigatorDescriptor) Object.defineProperty(globalThis, "navigator", navigatorDescriptor);
    else Reflect.deleteProperty(globalThis, "navigator");
  }
});

test("sharing cancelled via Web Share AbortError does not fall back to clipboard", async () => {
  const navigatorDescriptor = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  let clipboardCalls = 0;
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: {
      share: async () => {
        const err = new Error("Share canceled");
        err.name = "AbortError";
        throw err;
      },
      canShare: () => true,
      clipboard: {
        writeText: async () => {
          clipboardCalls += 1;
        },
      },
    },
  });
  try {
    assert.equal(await shareContent({ title: "Topic", url: "https://example.invalid/community/topic" }), "cancelled");
    assert.equal(clipboardCalls, 0);
  } finally {
    if (navigatorDescriptor) Object.defineProperty(globalThis, "navigator", navigatorDescriptor);
    else Reflect.deleteProperty(globalThis, "navigator");
  }
});

test("sharing falls back to clipboard copy when Web Share is unavailable", async () => {
  const navigatorDescriptor = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: {
      clipboard: {
        writeText: async (value: string) => {
          assert.equal(value, "https://example.invalid/community/topic");
        },
      },
    },
  });
  try {
    assert.equal(await shareContent({ title: "Topic", url: "https://example.invalid/community/topic" }), "copied");
  } finally {
    if (navigatorDescriptor) Object.defineProperty(globalThis, "navigator", navigatorDescriptor);
    else Reflect.deleteProperty(globalThis, "navigator");
  }
});

