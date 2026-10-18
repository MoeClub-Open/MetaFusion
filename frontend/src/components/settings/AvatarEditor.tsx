"use client";

import { useEffect, useState } from "react";
import { Camera, Loader2 } from "lucide-react";
import { UserAvatar } from "@/components/UserAvatar";
import { useAuth } from "@/lib/authContext";
import { fetchApi } from "@/lib/api/client";
import { useI18n } from "@/i18n/I18nProvider";

async function avatarImage(file: File): Promise<Blob> {
  if (file.size>10*1024*1024 || !["image/png","image/jpeg","image/webp","image/gif"].includes(file.type)) throw new Error("invalid_avatar");
  const bitmap=await createImageBitmap(file);
  try {
    const canvas=document.createElement("canvas");canvas.width=512;canvas.height=512;
    const context=canvas.getContext("2d");if(!context)throw new Error("invalid_avatar");
    const side=Math.min(bitmap.width,bitmap.height);
    context.drawImage(bitmap,(bitmap.width-side)/2,(bitmap.height-side)/2,side,side,0,0,512,512);
    return await new Promise<Blob>((resolve,reject)=>canvas.toBlob(blob=>blob?resolve(blob):reject(new Error("invalid_avatar")),"image/png"));
  } finally { bitmap.close(); }
}

export function AvatarEditor() {
  const {user,refreshProfile}=useAuth();
  const {t}=useI18n();
  const [image,setImage]=useState<Blob|null>(null);
  const [preview,setPreview]=useState("");
  const [busy,setBusy]=useState(false);
  const [status,setStatus]=useState("");
  const [error,setError]=useState(false);
  useEffect(()=>{if(!image){setPreview("");return;}const url=URL.createObjectURL(image);setPreview(url);return()=>URL.revokeObjectURL(url);},[image]);
  async function save(remove=false){
    if(busy||(!remove&&!image))return;
    setBusy(true);setStatus("");setError(false);
    try {
      const form=new FormData();if(image)form.set("avatar",image,"avatar.png");
      await fetchApi("/auth/avatar",{method:remove?"DELETE":"PUT",...(remove?{}:{body:form})});
      await refreshProfile();setImage(null);setStatus(t("accountUI.avatarSaved"));
    }catch {setError(true);setStatus(t("accountUI.avatarFailed"));}finally{setBusy(false);}
  }
  return <section className="flex flex-wrap items-center gap-5 border-b border-line-subtle pb-5">
    <UserAvatar user={user} src={preview||user?.avatar_url} size="3xl" shape="rounded" />
    <div className="min-w-0 flex-1 space-y-2">
      <h2 className="text-sm font-medium text-text-strong">{t("accountUI.avatar")}</h2>
      <p className="text-xs text-text-muted">{t("accountUI.avatarHint")}</p>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <label className={`inline-flex cursor-pointer items-center gap-1.5 rounded-control border border-line px-3 py-2 ${busy?"pointer-events-none opacity-40":"hover:bg-surfaceSubtle"}`}><Camera className="size-3.5"/>{t("accountUI.chooseAvatar")}<input aria-label={t("accountUI.chooseAvatar")} type="file" accept="image/png,image/jpeg,image/webp,image/gif" className="sr-only" disabled={busy} onChange={async event=>{const file=event.target.files?.[0];event.target.value="";if(!file)return;setBusy(true);setStatus("");try{setImage(await avatarImage(file));setError(false);}catch{setError(true);setStatus(t("accountUI.avatarInvalid"));}finally{setBusy(false);}}}/></label>
        {image&&<button type="button" disabled={busy} onClick={()=>void save()} className="rounded-control bg-primary px-3 py-2 font-medium text-white disabled:opacity-40">{t("accountUI.saveAvatar")}</button>}
        {image&&<button type="button" disabled={busy} onClick={()=>setImage(null)} className="px-2 py-2 text-text-muted">{t("common.cancel")}</button>}
        {user?.avatar_url&&<button type="button" disabled={busy} onClick={()=>void save(true)} className="px-2 py-2 text-text-muted hover:text-danger">{t("accountUI.removeAvatar")}</button>}
        {busy&&<Loader2 className="size-4 animate-spin text-text-muted"/>}
      </div>
      {status&&<p role={error?"alert":"status"} className={`text-xs ${error?"text-danger":"text-success"}`}>{status}</p>}
    </div>
  </section>;
}
