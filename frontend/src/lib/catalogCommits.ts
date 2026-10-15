import { api, type Entity, type Source } from "@/components/catalog/api";

export type CommitOperation = {
  target: "entity" | "relation";
  action: "create" | "update" | "delete" | "content";
  content_change?: {action:"add"|"replace"|"delete";position:number;inclusion?:Entity["contents"][number]};
  ref?: string; id?: string; base_version?: number;
  document?: unknown;
  patch?: {path: string; value?: unknown; remove?: boolean}[];
  reviewed_candidate_ids?: string[];
};
export type CommitReceipt = {
  id: string; applied: boolean; refs: Record<string,string>;
  review_status: "pending_review" | "reviewed";
  review_decision: "pending" | "approved" | "rejected" | "exempt";
  reviewed_by?: string; reviewed_at?: string; review_note?: string;
  preview?: {target: string; action: string; id: string; before: any; after: any}[];
};
export type CommitSubmission = {
  request: {id:string; edit_note: string; sources: Source[]; operations: CommitOperation[]};
  receipt: CommitReceipt; author_id: string; author_name: string; created_at: string;
};

// Retain the exact request after a timeout. A successful pending receipt is final
// acceptance into the queue and must never be replayed under a different id.
export function commitSubmitter() {
  let previous: {key:string; request:unknown} | undefined;
  return async (operations: CommitOperation[], note: string, sources: Source[]): Promise<CommitReceipt> => {
    const keyed = sources.map(({key: _key, pointers: _p, scope: _scope, ...source},i) => ({...source,key:`source${i+1}`}));
    const key = JSON.stringify({operations,note,sources:keyed});
    if (!previous || previous.key !== key) {
      const checkout = await api<{definitions_etag:string}>("/catalog/checkout","POST",{entity_ids:[],relation_ids:[]});
      previous = {key,request:{id:crypto.randomUUID(),definitions_etag:checkout.definitions_etag,
        edit_note:note,sources:keyed,operations:operations.map(op => ({...op,source_refs:keyed.map(s => ({source:s.key}))}))}};
    }
    return api<CommitReceipt>("/catalog/commits","POST",previous.request);
  };
}

export const entityFactFields = ["title","original_language","translations","attributes","external_links","pictures",
  "content_unit_id","parent_id","position","number","contents","subjects"] as const;

export function entityOperation(next: Entity, base?: Entity, reviewed: string[] = []): CommitOperation {
  if (!base?.id) {
    const document: Record<string,unknown> = {kind:next.kind};
    for (const field of [...entityFactFields,"work_id","release_id","medium_id"] as const) {
      if (next[field] !== undefined) document[field] = next[field];
    }
    return {target:"entity",action:"create",ref:"entity",document,reviewed_candidate_ids:reviewed};
  }
  const patch: NonNullable<CommitOperation["patch"]> = [];
  const escape = (key:string) => key.replace(/~/g,"~0").replace(/\//g,"~1");
  const put = (path:string,value:unknown) => patch.push(value===undefined ? {path,remove:true} : {path,value});
  for (const key of entityFactFields) {
    if(JSON.stringify(next[key])===JSON.stringify(base[key]))continue;
    if(key==="attributes" || key==="translations") {
      const before=base[key] || {};const after=next[key] || {};
      for (const name of Object.keys({...before,...after})) {
        const a=(before as Record<string,any>)[name];const b=(after as Record<string,any>)[name];
        const path=`/${key}/${escape(name)}`;
        if(JSON.stringify(a)===JSON.stringify(b))continue;
        if(key==="translations" && a && b) {
          for(const field of Object.keys({...a,...b})) if(JSON.stringify(a[field])!==JSON.stringify(b[field])) put(`${path}/${escape(field)}`,b[field]);
        }else put(path,b);
      }
    }else put(`/${key}`,next[key]);
  }
  const identity = patch.some(p => {
    const parts=p.path.slice(1).split("/");
    return parts[0]==="title" || parts[0]==="external_links" || parts[0]==="translations" && (parts.length<=2 || parts[2]==="title");
  });
  return {target:"entity",action:"update",id:base.id,base_version:base.version,patch,
    ...(identity ? {reviewed_candidate_ids:reviewed} : {})};
}
