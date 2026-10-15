"use client";

import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { EditorContent, useEditor, useEditorState } from "@tiptap/react";
import { Bold, Italic, Strikethrough, List, ListOrdered, Quote, Code, Link2, ImagePlus, Table2, Undo2, Redo2, Sigma, Superscript, Minus, Unlink } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { Modal } from "@/components/ui/Modal";
import { articleRichExtensions, articleRoundTripSafe, articleInsertionURL, articleFootnoteExists } from "@/lib/articleRichText";
import "katex/dist/katex.min.css";

type Insert = { type: "link" | "image" | "footnote" | "math"; primary: string; secondary: string; block?: boolean; position?: number; existing?: boolean };

export default function ArticleRichEditor({ value, onChange, onUnsupported, locale, labelId }: {
  value: string; onChange: (markdown: string) => void; onUnsupported: () => void; locale: string; labelId: string;
}) {
  const { t } = useI18n();
  const latest = useRef({ value, onChange, onUnsupported });
  latest.current = { value, onChange, onUnsupported };
  const emitted = useRef(value);
  const ready = useRef(false);
  const [insert, setInsert] = useState<Insert | null>(null);
  const [error, setError] = useState("");
  const editor = useEditor({
    extensions: articleRichExtensions((latex, position, block) => { setError(""); setInsert({ type: "math", primary: latex, secondary: "", position, block, existing: true }); },
      (raw) => { const match = /^\[\^([^\]]+)\]:\s*([\s\S]*)$/.exec(raw); if (match) { setError(""); setInsert({ type: "footnote", primary: match[1], secondary: match[2].replace(/\n {4}/g, "\n"), existing: true }); } }),
    content: value, contentType: "markdown", immediatelyRender: false,
    editorProps: { attributes: { role: "textbox", "aria-multiline": "true", "aria-labelledby": labelId, lang: locale, class: "article-rich-content", spellcheck: "true" } },
    onCreate: ({ editor: current }) => {
      ready.current = articleRoundTripSafe(latest.current.value, current.getMarkdown());
      if (!ready.current) latest.current.onUnsupported();
    },
    onUpdate: ({ editor: current }) => {
      if (!ready.current) return;
      emitted.current = current.getMarkdown();
      latest.current.onChange(emitted.current);
    },
  }, []);

  useEffect(() => {
    if (!editor || value === emitted.current) return;
    ready.current = false;
    editor.commands.setContent(value, { contentType: "markdown", emitUpdate: false });
    emitted.current = value;
    ready.current = articleRoundTripSafe(value, editor.getMarkdown());
    if (!ready.current) latest.current.onUnsupported();
  }, [editor, value]);

  const state = useEditorState({ editor, selector: ({ editor: current }) => ({
    bold: current?.isActive("bold"), italic: current?.isActive("italic"), strike: current?.isActive("strike"),
    bullet: current?.isActive("bulletList"), ordered: current?.isActive("orderedList"), quote: current?.isActive("blockquote"),
    code: current?.isActive("codeBlock"), link: current?.isActive("link"), table: current?.isActive("table"),
    heading: current?.isActive("heading") ? String(current.getAttributes("heading").level) : "0",
    undo: current?.can().undo(), redo: current?.can().redo(),
  }) });

  const tool = (key: string, icon: ReactNode, action: () => unknown, active?: boolean, disabled = false) => <button
    key={key} type="button" title={t(`catalog.article.tools.${key}`)} aria-label={t(`catalog.article.tools.${key}`)}
    aria-pressed={active} disabled={!editor || disabled} onMouseDown={(event) => event.preventDefault()} onClick={action}
    className={`article-tool ${active ? "is-active" : ""}`}>{icon}</button>;

  const openInsert = (type: Insert["type"]) => {
    if (!editor) return;
    setError("");
    if (type === "link") {
      if (editor.isActive("link")) editor.commands.extendMarkRange("link");
      const { from, to } = editor.state.selection;
      setInsert({ type, primary: editor.getAttributes("link").href || "", secondary: editor.state.doc.textBetween(from, to, " ") });
    } else if (type === "footnote") {
      const reference = editor.getAttributes("articleFootnoteReference").label;
      if (reference) editor.state.doc.descendants((node, position) => {
        if (node.type.name === "articleFootnoteDefinition" && String(node.attrs.raw).startsWith(`[^${reference}]:`)) editor.commands.setNodeSelection(position);
      });
      const raw = editor.getAttributes("articleFootnoteDefinition").raw;
      const match = typeof raw === "string" ? /^\[\^([^\]]+)\]:\s*([\s\S]*)$/.exec(raw) : null;
      let index = 1;
      while (latest.current.value.toLocaleLowerCase().includes(`[^ref${index}]`)) index++;
      setInsert({ type, primary: match?.[1] || `ref${index}`, secondary: match?.[2]?.replace(/\n {4}/g, "\n") || "", existing: !!match });
    } else setInsert({ type, primary: "", secondary: "", block: false });
  };

  const applyInsert = (event: FormEvent) => {
    event.preventDefault();
    event.stopPropagation();
    if (!insert || !editor) return;
    if (insert.type === "link" || insert.type === "image") {
      const url = articleInsertionURL(insert.primary, insert.type === "image");
      if (!url) { setError(t("catalog.article.invalidURL")); return; }
      if (insert.type === "link") editor.chain().focus().insertContent({ type: "text", text: insert.secondary.trim() || url, marks: [{ type: "link", attrs: { href: url } }] }).run();
      else editor.chain().focus().setImage({ src: url, alt: insert.secondary.trim() }).run();
    } else if (insert.type === "footnote") {
      const label = insert.existing ? insert.primary : insert.primary.trim();
      if (!label || (!insert.existing && /[\[\]\s]/.test(label)) || !insert.secondary.trim() || (!insert.existing && articleFootnoteExists(latest.current.value, label))) {
        setError(t("catalog.article.invalidFootnote")); return;
      }
      const raw = `[^${label}]: ${insert.secondary.trim().replace(/\n/g, "\n    ")}`;
      if (insert.existing) editor.chain().focus().updateAttributes("articleFootnoteDefinition", { raw }).run();
      else {
        editor.chain().focus().insertContent({ type: "articleFootnoteReference", attrs: { label } }).run();
        editor.commands.insertContentAt(editor.state.doc.content.size, { type: "articleFootnoteDefinition", attrs: { raw } });
      }
    } else {
      const latex = insert.primary.trim();
      if (!latex) return;
      const type = insert.block ? "blockMath" : "inlineMath";
      if (insert.existing && insert.position != null) editor.chain().focus().setNodeSelection(insert.position).updateAttributes(type, { latex }).run();
      else editor.chain().focus().insertContent({ type, attrs: { latex } }).run();
    }
    setInsert(null);
  };
  const size = "h-4 w-4";

  return <div className="article-rich-editor min-w-0 rounded-lg border border-line bg-surface">
    <div role="toolbar" aria-label={t("catalog.article.toolbar")} className="article-toolbar">
      <label><span className="sr-only">{t("catalog.article.tools.block")}</span><select value={state?.heading || "0"} disabled={!editor}
        onChange={(event) => { const level = Number(event.target.value); if (level) editor?.chain().focus().setHeading({ level: level as 1 | 2 | 3 | 4 | 5 | 6 }).run(); else editor?.chain().focus().setParagraph().run(); }}>
        <option value="0">{t("catalog.article.paragraph")}</option>{[1, 2, 3, 4, 5, 6].map((level) => <option key={level} value={level}>{t("catalog.article.heading", { level })}</option>)}
      </select></label>
      {tool("bold", <Bold className={size} />, () => editor?.chain().focus().toggleBold().run(), state?.bold)}
      {tool("italic", <Italic className={size} />, () => editor?.chain().focus().toggleItalic().run(), state?.italic)}
      {tool("strike", <Strikethrough className={size} />, () => editor?.chain().focus().toggleStrike().run(), state?.strike)}
      {tool("bullet", <List className={size} />, () => editor?.chain().focus().toggleBulletList().run(), state?.bullet)}
      {tool("ordered", <ListOrdered className={size} />, () => editor?.chain().focus().toggleOrderedList().run(), state?.ordered)}
      {tool("quote", <Quote className={size} />, () => editor?.chain().focus().toggleBlockquote().run(), state?.quote)}
      {tool("code", <Code className={size} />, () => editor?.chain().focus().toggleCodeBlock().run(), state?.code)}
      {tool("link", <Link2 className={size} />, () => openInsert("link"), state?.link)}
      {state?.link && tool("unlink", <Unlink className={size} />, () => editor?.chain().focus().extendMarkRange("link").unsetLink().run())}
      {tool("image", <ImagePlus className={size} />, () => openInsert("image"))}
      {tool("table", <Table2 className={size} />, () => editor?.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run(), state?.table)}
      {tool("footnote", <Superscript className={size} />, () => openInsert("footnote"))}
      {tool("math", <Sigma className={size} />, () => openInsert("math"))}
      {tool("rule", <Minus className={size} />, () => editor?.chain().focus().setHorizontalRule().run())}
      {tool("undo", <Undo2 className={size} />, () => editor?.chain().focus().undo().run(), undefined, !state?.undo)}
      {tool("redo", <Redo2 className={size} />, () => editor?.chain().focus().redo().run(), undefined, !state?.redo)}
    </div>
    {state?.table && <div className="article-toolbar" role="toolbar" aria-label={t("catalog.article.tools.table")}>
      {(["addRowAfter", "addColumnAfter", "deleteRow", "deleteColumn", "deleteTable"] as const).map((command) => <button key={command} type="button"
        onMouseDown={(event) => event.preventDefault()} onClick={() => editor?.chain().focus()[command]().run()}>{t(`catalog.article.tools.${command}`)}</button>)}
    </div>}
    <EditorContent editor={editor} />
    <Modal open={!!insert} title={insert ? t(`catalog.article.tools.${insert.type}`) : ""} onClose={() => setInsert(null)} initialFocus="input:not(:disabled),textarea">
      <div className="space-y-3">
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        {insert && <form className="cv-form space-y-4" onSubmit={applyInsert}>
          <label className="block space-y-1 text-xs text-text-muted"><span>{t(`catalog.article.insert.${insert.type === "link" || insert.type === "image" ? "url" : insert.type === "footnote" ? "identifier" : "formula"}`)}</span>
            <input autoComplete="off" required disabled={insert.type === "footnote" && insert.existing} value={insert.primary} onChange={(event) => setInsert({ ...insert, primary: event.target.value })} className="w-full rounded-lg border border-line bg-surfaceSubtle p-2 text-sm text-text-body" /></label>
          {insert.type !== "math" && <label className="block space-y-1 text-xs text-text-muted"><span>{t(`catalog.article.insert.${insert.type === "footnote" ? "reference" : insert.type === "image" ? "alt" : "label"}`)}</span>
            <textarea value={insert.secondary} required={insert.type === "footnote"} rows={insert.type === "footnote" ? 5 : 2} onChange={(event) => setInsert({ ...insert, secondary: event.target.value })} className="w-full rounded-lg border border-line bg-surfaceSubtle p-2 text-sm text-text-body" /></label>}
          {insert.type === "math" && !insert.existing && <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={insert.block} onChange={(event) => setInsert({ ...insert, block: event.target.checked })} />{t("catalog.article.insert.blockMath")}</label>}
          <div className="flex justify-end gap-2"><button type="button" onClick={() => setInsert(null)}>{t("common.cancel")}</button><button type="submit" className="cv-primary">{t("catalog.article.insert.apply")}</button></div>
        </form>}
      </div>
    </Modal>
  </div>;
}
