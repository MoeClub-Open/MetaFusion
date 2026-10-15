import { test } from "node:test";
import assert from "node:assert/strict";
import { commitSubmitter, entityOperation } from "../src/lib/catalogCommits";
import { emptyEntity } from "../src/components/catalog/api";

test("editor excludes server fields and patches locales independently", () => {
  const base={...emptyEntity(),id:"id",version:7,translations:{en:{title:"Entry",body:"old"},ja:{title:"項目"}},attributes:{tags:["old"]}};
  const next=structuredClone(base);next.translations.en.body="new";
  const op=entityOperation(next,base);
  assert.deepEqual(op.patch,[{path:"/translations/en/body",value:"new"}]);
  assert.equal(op.reviewed_candidate_ids,undefined);
  const created=entityOperation(next);
  assert.ok(!("state" in (created.document as object)));
  assert.ok(!("review_status" in (created.document as object)));
  const restored=entityOperation({...base,title:"Historical"},base,[]);
  assert.deepEqual(restored.patch,[{path:"/title",value:"Historical"}]);
});

test("202 is confirmed queue acceptance and a lost response retains its immutable id", async () => {
  const original=globalThis.fetch;const requests:any[]=[];let lost=true;
  globalThis.fetch=(async (url,opts)=>{
    if(String(url).endsWith("/checkout")) return Response.json({definitions_etag:"definitions"});
    const body=JSON.parse(String(opts?.body));requests.push(body);
    if(lost){lost=false;throw new TypeError("network interrupted")}
    return Response.json({id:body.id,applied:false,review_status:"pending_review",review_decision:"pending",refs:{},items:[]},{status:202});
  }) as typeof fetch;
  try {
    const submit=commitSubmitter();const ops=[entityOperation({...emptyEntity(),title:"Entry"})];
    const sources=[{kind:"publication",citation:"Publisher catalogue"}];
    await assert.rejects(submit(ops,"create",sources));
    const receipt=await submit(ops,"create",sources);
    assert.equal(receipt.applied,false);assert.equal(receipt.review_status,"pending_review");
    assert.deepEqual(requests[0],requests[1]);
    assert.equal(requests[1].sources[0].key,"source1");
    assert.deepEqual(requests[1].operations[0].source_refs,[{source:"source1"}]);
  }finally{globalThis.fetch=original}
});
