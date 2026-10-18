import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchNotifications } from "../src/lib/api/notifications";
import { fetchUserContributions } from "../src/lib/api/users";

test("notification pagination preserves server filters and supports cancelling stale reads", async () => {
  const original=globalThis.fetch;
  const signal=new AbortController().signal;
  const seen: URL[]=[];
  globalThis.fetch=(async (url,init)=>{
    seen.push(new URL(String(url),"http://localhost"));
    assert.equal(init?.signal,signal);
    return Response.json({items:[],total:0,unread:7});
  }) as typeof fetch;
  try {
    for(const offset of [0,20]){
      const data=await fetchNotifications({category:"review",read:"unread",from:"2026-10-01",to:"2026-10-18",offset},signal);
      assert.equal(data.unread,7);
      const query=seen.at(-1)!.searchParams;
      assert.equal(query.get("category"),"review");assert.equal(query.get("read"),"unread");
      assert.equal(query.get("from"),"2026-10-01");assert.equal(query.get("to"),"2026-10-18");assert.equal(query.get("offset"),String(offset));
      assert.equal(query.has("recipient_id"),false);
    }
  } finally {globalThis.fetch=original;}
});

test("contribution date ranges remain attached when paging the public year", async () => {
  const original=globalThis.fetch;
  globalThis.fetch=(async url=>{
    const query=new URL(String(url),"http://localhost").searchParams;
    assert.equal(query.get("year"),"2026");assert.equal(query.get("from"),"2026-10-17");assert.equal(query.get("to"),"2026-10-18");assert.equal(query.get("page"),"2");
    return Response.json({year:2026,timezone:"UTC",summary:{merged_commits:0,changed_entities:0,created_entities:0,merged_pull_requests:0,legacy_revisions:0},calendar:[],items:[],total:0,page:2,page_size:20});
  }) as typeof fetch;
  try {await fetchUserContributions("viewer",{year:2026,from:"2026-10-17",to:"2026-10-18",page:2});}
  finally {globalThis.fetch=original;}
});
