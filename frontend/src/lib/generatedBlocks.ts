export type GeneratedBlock =
  | { type: "markdown"; title?: string; markdown: string }
  | { type: "table"; title?: string; columns: string[]; rows: string[][] }
  | { type: "timeline"; title?: string; events: { date: string; title: string; description?: string; entity_id?: string }[] }
  | { type: "chart"; title?: string; chart_type: "bar" | "line"; labels: string[]; series: { name: string; values: number[] }[] };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const record = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);
const text = (value: unknown, limit: number): value is string => typeof value === "string" && value.length <= limit;
const optionalText = (value: unknown, limit: number): value is string | undefined => value === undefined || text(value, limit);
const strings = (value: unknown, maxItems: number, maxLength: number): value is string[] =>
  Array.isArray(value) && value.length <= maxItems && value.every((item) => text(item, maxLength));

/** Only these data shapes reach the renderer. Unknown properties never become DOM attributes. */
export function parseGeneratedBlocks(value: unknown): GeneratedBlock[] {
  if (!Array.isArray(value)) return [];
  const blocks: GeneratedBlock[] = [];
  let remainingCharacters = 200_000;
  for (const item of value.slice(0, 32)) {
    if (!record(item) || !optionalText(item.title, 200)) continue;
    const previousCount = blocks.length;
    const title = item.title === undefined ? {} : { title: item.title };
    switch (item.type) {
      case "markdown":
        if (text(item.markdown, 40_000)) blocks.push({ type: "markdown", ...title, markdown: item.markdown });
        break;
      case "table":
        if (!strings(item.columns, 16, 200) || item.columns.length === 0 ||
            !Array.isArray(item.rows) || item.rows.length > 200) break;
        const columnCount = item.columns.length;
        if (item.rows.every((row) => strings(row, columnCount, 1_000) && row.length === columnCount)) {
          blocks.push({ type: "table", ...title, columns: [...item.columns], rows: item.rows.map((row: string[]) => [...row]) });
        }
        break;
      case "timeline": {
        if (!Array.isArray(item.events) || item.events.length > 100) break;
        const events: Extract<GeneratedBlock, { type: "timeline" }>["events"] = [];
        for (const event of item.events) {
          if (!record(event) || !text(event.date, 64) || !text(event.title, 200) ||
              !optionalText(event.description, 2_000) ||
              (event.entity_id !== undefined && (typeof event.entity_id !== "string" || !UUID.test(event.entity_id)))) break;
          events.push({ date: event.date, title: event.title,
            ...(event.description === undefined ? {} : { description: event.description }),
            ...(event.entity_id === undefined ? {} : { entity_id: event.entity_id }) });
        }
        if (events.length === item.events.length) blocks.push({ type: "timeline", ...title, events });
        break;
      }
      case "chart": {
        if ((item.chart_type !== "bar" && item.chart_type !== "line") ||
            !strings(item.labels, 100, 200) || item.labels.length === 0 ||
            !Array.isArray(item.series) || item.series.length === 0 || item.series.length > 8) break;
        const series: Extract<GeneratedBlock, { type: "chart" }>["series"] = [];
        for (const entry of item.series) {
          if (!record(entry) || !text(entry.name, 200) || !Array.isArray(entry.values) ||
              entry.values.length !== item.labels.length || !entry.values.every((number) => typeof number === "number" && Number.isFinite(number))) break;
          series.push({ name: entry.name, values: [...entry.values] });
        }
        if (series.length === item.series.length) blocks.push({ type: "chart", ...title, chart_type: item.chart_type, labels: [...item.labels], series });
        break;
      }
    }
    if (blocks.length > previousCount) {
      const block = blocks[blocks.length - 1];
      let length = block.title?.length || 0;
      if (block.type === "markdown") length += block.markdown.length;
      if (block.type === "table") length += [...block.columns, ...block.rows.flat()].reduce((sum, cell) => sum + cell.length, 0);
      if (block.type === "timeline") length += block.events.reduce((sum, event) => sum + event.date.length + event.title.length + (event.description?.length || 0), 0);
      if (block.type === "chart") length += block.labels.reduce((sum, label) => sum + label.length, 0) + block.series.reduce((sum, series) => sum + series.name.length, 0);
      if (length > remainingCharacters) blocks.pop();
      else remainingCharacters -= length;
    }
  }
  return blocks;
}
