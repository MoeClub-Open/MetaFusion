import { diffArrays } from "diff";
import { revisionContent } from "./revisionData";

export type DiffKind = "context" | "add" | "remove";

export interface DiffLine {
  kind: DiffKind;
  content: string;
  oldLine: number | null;
  newLine: number | null;
  noNewline?: boolean;
}

export interface DiffHunk {
  header: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines: DiffLine[];
}

export interface RevisionDiff {
  hunks: DiffHunk[];
  unified: string;
  additions: number;
  deletions: number;
  /** The changed middle was kept complete when a fine-grained search hit its budget. */
  coarse: boolean;
}

export interface TextDiffPart {
  kind: DiffKind;
  text: string;
}

interface SequencePart {
  kind: DiffKind;
  values: string[];
}

// Myers search is bounded in both edit distance and time. A large replacement
// still displays every removed/added token; exhausting the budget never means
// "unchanged". Common boundaries are preserved even on this fallback path.
function sequenceDiff(before: string[], after: string[]): { parts: SequencePart[]; coarse: boolean } {
  let prefix = 0;
  while (prefix < before.length && prefix < after.length && before[prefix] === after[prefix]) prefix++;
  let suffix = 0;
  while (suffix < before.length - prefix && suffix < after.length - prefix &&
      before[before.length - 1 - suffix] === after[after.length - 1 - suffix]) suffix++;
  const left = before.slice(prefix, before.length - suffix);
  const right = after.slice(prefix, after.length - suffix);
  const parts: SequencePart[] = [];
  if (prefix) parts.push({ kind: "context", values: before.slice(0, prefix) });
  let coarse = false;
  if (!left.length) {
    if (right.length) parts.push({ kind: "add", values: right });
  } else if (!right.length) {
    parts.push({ kind: "remove", values: left });
  } else {
    const changes = left.length + right.length <= 20_000
      ? diffArrays(left, right, { maxEditLength: 256, timeout: 25 }) : undefined;
    if (changes) {
      parts.push(...changes.map((change) => ({
        kind: change.added ? "add" as const : change.removed ? "remove" as const : "context" as const,
        values: change.value,
      })));
    } else {
      coarse = true;
      parts.push({ kind: "remove", values: left }, { kind: "add", values: right });
    }
  }
  if (suffix) parts.push({ kind: "context", values: before.slice(before.length - suffix) });
  return { parts, coarse };
}

function textLines(text: string): string[] {
  return text.match(/[^\n]*\n|[^\n]+$/g) || [];
}

function headerLabel(label: string): string {
  // A title/actor supplied by a user must not inject extra patch headers.
  return label.replace(/[\r\n\t]/g, " ");
}

/** Build a valid unified patch and the same hunks with explicit old/new line numbers. */
export function buildTextDiff(before: string, after: string, oldLabel: string, newLabel: string, context = 3): RevisionDiff {
  const { parts, coarse } = sequenceDiff(textLines(before), textLines(after));
  const lines: DiffLine[] = [];
  let oldLine = 1;
  let newLine = 1;
  let additions = 0;
  let deletions = 0;
  for (const part of parts) {
    for (const value of part.values) {
      const noNewline = !value.endsWith("\n");
      lines.push({
        kind: part.kind,
        content: noNewline ? value : value.slice(0, -1),
        oldLine: part.kind === "add" ? null : oldLine++,
        newLine: part.kind === "remove" ? null : newLine++,
        ...(noNewline ? { noNewline: true } : {}),
      });
      if (part.kind === "add") additions++;
      if (part.kind === "remove") deletions++;
    }
  }
  const padding = Number.isFinite(context) ? Math.max(0, Math.floor(context)) : 3;
  const ranges: { start: number; end: number }[] = [];
  lines.forEach((line, index) => {
    if (line.kind === "context") return;
    const start = Math.max(0, index - padding);
    const end = Math.min(lines.length, index + padding + 1);
    const previous = ranges[ranges.length - 1];
    if (previous && start <= previous.end) previous.end = end;
    else ranges.push({ start, end });
  });

  let cursor = 0;
  let oldCursor = 1;
  let newCursor = 1;
  const hunks = ranges.map(({ start, end }): DiffHunk => {
    for (; cursor < start; cursor++) {
      if (lines[cursor].kind !== "add") oldCursor++;
      if (lines[cursor].kind !== "remove") newCursor++;
    }
    const hunkLines = lines.slice(start, end);
    const oldLines = hunkLines.filter((line) => line.kind !== "add").length;
    const newLines = hunkLines.filter((line) => line.kind !== "remove").length;
    const oldStart = oldCursor - (oldLines ? 0 : 1);
    const newStart = newCursor - (newLines ? 0 : 1);
    oldCursor += oldLines;
    newCursor += newLines;
    cursor = end;
    return {
      header: `@@ -${oldStart},${oldLines} +${newStart},${newLines} @@`,
      oldStart, oldLines, newStart, newLines, lines: hunkLines,
    };
  });
  const patch = [`--- ${headerLabel(oldLabel)}`, `+++ ${headerLabel(newLabel)}`];
  for (const hunk of hunks) {
    patch.push(hunk.header);
    for (const line of hunk.lines) {
      patch.push((line.kind === "add" ? "+" : line.kind === "remove" ? "-" : " ") + line.content);
      if (line.noNewline) patch.push("\\ No newline at end of file");
    }
  }
  return { hunks, unified: patch.join("\n") + "\n", additions, deletions, coarse };
}

/** A supplied empty object is valid for a creation/deletion preview; absent data is not. */
export function isRevisionSnapshot(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

export function serializeRevisionContent(snapshot: unknown): string {
  if (!isRevisionSnapshot(snapshot)) throw new Error("revision_snapshot_unavailable");
  return JSON.stringify(revisionContent(snapshot), null, 2) + "\n";
}

/** Null on the left is an intentional initial-version baseline, never missing data. */
export function buildRevisionDiff(before: unknown, after: unknown, oldLabel: string, newLabel: string, context = 3): RevisionDiff {
  return buildTextDiff(before === null ? "" : serializeRevisionContent(before), serializeRevisionContent(after), oldLabel, newLabel, context);
}

/** Exact inline highlights: whitespace is retained and CJK changes do not color an entire paragraph. */
export function diffText(before: string, after: string): TextDiffPart[] {
  const tokenize = (text: string) => text.match(/\s+|[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]|(?:(?![\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}])[\p{L}\p{M}\p{N}_])+|[^\s]/gu) || [];
  return sequenceDiff(tokenize(before), tokenize(after)).parts.map((part) => ({ kind: part.kind, text: part.values.join("") }));
}
