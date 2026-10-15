"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { commitSubmitter } from "@/lib/catalogCommits";
import { useAuth } from "@/lib/authContext";
import { canEditEntity } from "@/lib/permissions";
import { useI18n } from "@/i18n/I18nProvider";
import { effectiveSchemeFields, matchSchemes, useDefinitions } from "@/lib/definitions";
import { api, type Entity, type Source } from "./api";
import { EntityPicker, Evidence, ErrorMessage, GroupFieldInput } from "./Fields";

export function TrackContentEditor({track,onSaved}: {track: Entity; onSaved: (track: Entity) => void}) {
  const {user} = useAuth();
  const {t} = useI18n();
  const {definitions} = useDefinitions();
  const submit = useRef(commitSubmitter());
  const [submission,setSubmission] = useState("");
  const [record,setRecord] = useState<Entity["contents"][number]>();
  const [oldPosition,setOldPosition] = useState<number | null>(null);
  const [note,setNote] = useState("");
  const [sources,setSources] = useState<Source[]>([{kind:"url",citation:"",url:""}]);
  const [error,setError] = useState("");
  const [busy,setBusy] = useState(false);
  const [format,setFormat] = useState("");
  useEffect(() => {
    let active = true;
    setFormat("");
    if (track.medium_id) api<Entity>(`/catalog/entities/${track.medium_id}`).then((medium) => {if (active) setFormat(String(medium.attributes?.format || ""))}).catch(() => {});
    return () => {active = false};
  },[track.medium_id]);
  if (!canEditEntity(user,track)) return null;
  const begin = (content?: Entity["contents"][number]) => {
    setOldPosition(content?.position ?? null);
    setRecord(content ? structuredClone({...content,sources:undefined}) : {
      expression_id:"", position:Math.max(-1,...(track.contents || []).map((item) => item.position))+1, locator:{},attributes:{},
    });
    setError(""); setNote("");
    setSources(content?.sources?.length ? structuredClone(content.sources) : [{kind:"url",citation:"",url:""}]);
  };
  const save = async (remove = false) => {
    if (!record) return;
    setBusy(true);setError("");
    try {
      const result = await submit.current([{target:"entity",action:"content",id:track.id,base_version:track.version,
        content_change:{action:remove ? "delete" : oldPosition===null ? "add":"replace",position:oldPosition ?? 0,
          ...(!remove ? {inclusion:{...record,sources:sources.map(({key:_k,pointers:_p,scope:_s,...s})=>s)}}:{})},
      }],note,sources);
      setRecord(undefined);
      if(result.applied) onSaved(await api<Entity>(`/catalog/entities/${track.id}`));else setSubmission(result.id);
    } catch(e) {setError((e as Error).message)} finally {setBusy(false)}
  };
  return <div className="cv-form rounded border border-line p-3">
    <div className="flex flex-wrap gap-2">
      <button type="button" onClick={() => begin()}>{t("catalog.addContent")}</button>
      {(track.contents || []).map((content) => <button type="button" key={content.position} onClick={() => begin(content)}>
        {t("catalog.editContentPosition",{position:content.position+1})}
      </button>)}
    </div>
    {submission && <Link className="text-primary underline" href={`/submissions/${submission}`}>{t("catalog.review.pending_review")} · {t("catalog.review.submission")}</Link>}
    {record && <form className="space-y-3" onSubmit={(e) => {e.preventDefault();void save()}}>
      <ErrorMessage error={error}/>
      <label>{t("catalog.expression")}<EntityPicker value={record.expression_id} kinds={["expression"]} onChange={(expression_id) => setRecord({...record,expression_id})}/></label>
      <label>{t("catalog.position")}<input type="number" min="0" required value={record.position} onChange={(e) => setRecord({...record,position:Number(e.target.value)})}/></label>
      <fieldset><legend>{t("catalog.locator")}</legend><GroupFieldInput defs={definitions} code="locator" value={record.locator} codes={effectiveSchemeFields(matchSchemes(definitions,"locator","track",format))} onChange={(locator) => setRecord({...record,locator})}/></fieldset>
      <GroupFieldInput defs={definitions} code="inclusion_attributes" value={record.attributes} codes={effectiveSchemeFields(matchSchemes(definitions,"inclusion_attributes","track",format))} onChange={(attributes) => setRecord({...record,attributes})}/>
      <Evidence note={note} setNote={setNote} sources={sources} setSources={setSources}/>
      <div className="flex gap-2">
        <button type="submit" className="cv-primary" disabled={busy}>{t("catalog.save")}</button>
        {oldPosition !== null && <button type="button" disabled={busy} onClick={() => void save(true)}>{t("catalog.remove")}</button>}
        <button type="button" disabled={busy} onClick={() => setRecord(undefined)}>{t("catalog.cancel")}</button>
      </div>
    </form>}
  </div>;
}
