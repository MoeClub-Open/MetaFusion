"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { ensureEditingWorkspace, stageEditingWorkspace } from "@/lib/catalogCommits";
import { checkoutWorkspace, type Workspace, type WorkspaceOperation } from "@/lib/api/collaboration";
import { useAuth } from "@/lib/authContext";
import { canEditEntity } from "@/lib/permissions";
import { useI18n } from "@/i18n/I18nProvider";
import { effectiveSchemeFields, matchSchemes, useDefinitions } from "@/lib/definitions";
import { api, type Entity } from "./api";
import { EntityPicker, ErrorMessage, GroupFieldInput, WorkspaceEntityPickerProvider } from "./Fields";

type Inclusion = Entity["contents"][number];

export function trackContentOperation(trackId: string, record: Inclusion, oldPosition: number | null, remove: boolean): WorkspaceOperation {
  return {
    target: "entity", action: "content", id: trackId,
    content_change: {
      action: remove ? "delete" : oldPosition === null ? "add" : "replace",
      position: oldPosition ?? 0,
      ...(!remove ? { inclusion: structuredClone(record) } : {}),
    },
  };
}

export function TrackContentEditor({ track, workspaceId }: { track: Entity; workspaceId?: string }) {
  const { user } = useAuth();
  const { t } = useI18n();
  const { definitions } = useDefinitions();
  const scope = `${user?.id || ""}:${track.id || ""}:${workspaceId || ""}`;
  const workspace = useRef<Workspace | null>(null);
  const scopeRequests = useRef(new AbortController());
  const generation = useRef(0);
  const [loadedScope, setLoadedScope] = useState(scope);
  const [workingTrack, setWorkingTrack] = useState(track);
  const [savedWorkspaceId, setSavedWorkspaceId] = useState("");
  const [record, setRecord] = useState<Inclusion>();
  const [oldPosition, setOldPosition] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [format, setFormat] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    scopeRequests.current = controller;
    generation.current += 1;
    workspace.current = null;
    setLoadedScope(scope);
    setWorkingTrack(track);
    setRecord(undefined);
    setSavedWorkspaceId("");
    setError("");
    setBusy(false);
    return () => { controller.abort(); generation.current += 1; };
  }, [scope]);

  useEffect(() => {
    if (!workspace.current) setWorkingTrack(track);
  }, [track]);

  useEffect(() => {
    let active = true;
    setFormat("");
    const medium = workspace.current?.objects.find((object) => object.target === "entity" && object.id === workingTrack.medium_id)?.document;
    if (medium) setFormat(String((medium.attributes as Record<string, unknown> | undefined)?.format || ""));
    else if (workingTrack.medium_id) api<Entity>(`/catalog/entities/${workingTrack.medium_id}`)
      .then((value) => { if (active) setFormat(String(value.attributes?.format || "")); }).catch(() => {});
    return () => { active = false; };
  }, [workingTrack]);

  if (!canEditEntity(user, track)) return null;
  const visibleTrack = loadedScope === scope ? workingTrack : track;
  const visibleRecord = loadedScope === scope ? record : undefined;

  const friendly = (message: string) => message === "workspace_conflict"
    ? t("collaboration.editor.conflict") : message;

  const begin = async (position: number | null = null) => {
    if (!user || !track.id) return;
    const requestGeneration = generation.current;
    const signal = scopeRequests.current.signal;
    setBusy(true);
    setError("");
    try {
      const requested = workspaceId || new URLSearchParams(window.location.search).get("workspace");
      let current = await ensureEditingWorkspace(user.id, requested, signal);
      let object = current.objects.find((item) => item.target === "entity" && item.id === track.id);
      if (!object) {
        current = await checkoutWorkspace(current.id, { expected_version: current.version, entity_ids: [track.id], relation_ids: [] }, signal);
        object = current.objects.find((item) => item.target === "entity" && item.id === track.id);
      }
      if (!object?.document) throw new Error(t("collaboration.editor.conflict"));
      const nextTrack = { ...object.document, id: object.id, version: object.base_version } as Entity;
      const inclusion = position === null ? undefined : nextTrack.contents?.find((item) => item.position === position);
      if (position !== null && !inclusion) throw new Error(t("collaboration.editor.conflict"));
      if (generation.current !== requestGeneration || signal.aborted) return;
      workspace.current = current;
      setWorkingTrack(nextTrack);
      setOldPosition(position);
      setRecord(inclusion ? structuredClone(inclusion) : {
        expression_id: "", position: Math.max(-1, ...(nextTrack.contents || []).map((item) => item.position)) + 1, locator: {}, attributes: {},
      });
    } catch (reason) {
      if (generation.current === requestGeneration) setError(friendly((reason as Error).message));
    } finally {
      if (generation.current === requestGeneration) setBusy(false);
    }
  };

  const save = async (remove = false) => {
    if (!record || !workspace.current || !track.id) return;
    const requestGeneration = generation.current;
    const signal = scopeRequests.current.signal;
    setBusy(true);
    setError("");
    try {
      const result = await stageEditingWorkspace(workspace.current, [trackContentOperation(track.id, record, oldPosition, remove)], signal);
      if (generation.current !== requestGeneration || signal.aborted) return;
      const object = result.workspace.objects.find((item) => item.target === "entity" && item.id === track.id);
      if (!object?.document) throw new Error(t("collaboration.editor.conflict"));
      workspace.current = result.workspace;
      setWorkingTrack({ ...object.document, id: object.id, version: object.base_version } as Entity);
      setRecord(undefined);
      setSavedWorkspaceId(result.workspace.id);
    } catch (reason) {
      if (generation.current === requestGeneration) setError(friendly((reason as Error).message));
    } finally {
      if (generation.current === requestGeneration) setBusy(false);
    }
  };

  return <WorkspaceEntityPickerProvider value={loadedScope === scope ? workspace.current : null}><div className="cv-form rounded border border-line p-3">
    <div className="flex flex-wrap gap-2">
      <button type="button" disabled={busy} onClick={() => void begin()}>{t("catalog.addContent")}</button>
      {(visibleTrack.contents || []).map((content) => <button type="button" disabled={busy} key={content.position} onClick={() => void begin(content.position)}>
        {t("catalog.editContentPosition", { position: content.position + 1 })}
      </button>)}
    </div>
    <ErrorMessage error={loadedScope === scope ? error : ""} />
    {loadedScope === scope && savedWorkspaceId && <p className="text-sm text-text-muted">{t("collaboration.editor.saved")} <Link className="text-primary underline" href={`/workspaces/${savedWorkspaceId}`}>{t("collaboration.editor.openWorkspace")}</Link></p>}
    {visibleRecord && <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <label>{t("catalog.expression")}<EntityPicker value={visibleRecord.expression_id} kinds={["expression"]} onChange={(expression_id) => setRecord({ ...visibleRecord, expression_id })} /></label>
      <label>{t("catalog.position")}<input type="number" min="0" required value={visibleRecord.position} onChange={(event) => setRecord({ ...visibleRecord, position: Number(event.target.value) })} /></label>
      <fieldset><legend>{t("catalog.locator")}</legend><GroupFieldInput defs={definitions} code="locator" value={visibleRecord.locator} codes={effectiveSchemeFields(matchSchemes(definitions, "locator", "track", format))} onChange={(locator) => setRecord({ ...visibleRecord, locator })} /></fieldset>
      <GroupFieldInput defs={definitions} code="inclusion_attributes" value={visibleRecord.attributes} codes={effectiveSchemeFields(matchSchemes(definitions, "inclusion_attributes", "track", format))} onChange={(attributes) => setRecord({ ...visibleRecord, attributes })} />
      <div className="flex gap-2">
        <button type="submit" className="cv-primary" disabled={busy || !visibleRecord.expression_id}>{t("catalog.save")}</button>
        {oldPosition !== null && <button type="button" disabled={busy} onClick={() => void save(true)}>{t("catalog.remove")}</button>}
        <button type="button" disabled={busy} onClick={() => setRecord(undefined)}>{t("catalog.cancel")}</button>
      </div>
    </form>}
  </div></WorkspaceEntityPickerProvider>;
}
