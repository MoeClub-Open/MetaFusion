import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildDisplayGraph,
  computeGraphLayout,
  withAncestorBridgeLinks,
} from "../src/lib/relationGraphLayout";
import type { GraphLink, GraphNode } from "../src/lib/api/catalog";

test("multi-level hierarchy layout orders skeleton tiers vertically without overlap", () => {
  const nodes: GraphNode[] = [
    { id: "a1", name: "Composer", type: "agent", category: "agent", level: 1 },
    { id: "w1", name: "Main Work", type: "work", category: "work", level: 1 },
    { id: "r1", name: "First Edition", type: "release", category: "release", level: 0 },
    { id: "m1", name: "Disc 1", type: "medium", category: "medium", level: 1, order: 1 },
    { id: "m2", name: "Disc 2", type: "medium", category: "medium", level: 1, order: 2 },
    { id: "t1", name: "Track 1", type: "track", category: "track", level: 2, order: 1 },
    { id: "t2", name: "Track 2", type: "track", category: "track", level: 2, order: 2 },
    { id: "t3", name: "Track 3", type: "track", category: "track", level: 2, order: 1 },
    { id: "e1", name: "Recording 1", type: "expression", category: "expression", level: 3 },
  ];
  const links: GraphLink[] = [
    { source: "a1", target: "r1", source_type: "agent", target_type: "release", type: "composer", label: "Composer" },
    { source: "w1", target: "r1", source_type: "work", target_type: "release", type: "subject:release:work", label: "Work", is_hierarchical: true },
    { source: "r1", target: "m1", source_type: "release", target_type: "medium", type: "release_id", label: "Medium", is_hierarchical: true },
    { source: "r1", target: "m2", source_type: "release", target_type: "medium", type: "release_id", label: "Medium", is_hierarchical: true },
    { source: "m1", target: "t1", source_type: "medium", target_type: "track", type: "medium_id", label: "Track", is_hierarchical: true },
    { source: "m1", target: "t2", source_type: "medium", target_type: "track", type: "medium_id", label: "Track", is_hierarchical: true },
    { source: "m2", target: "t3", source_type: "medium", target_type: "track", type: "medium_id", label: "Track", is_hierarchical: true },
    { source: "t1", target: "e1", source_type: "track", target_type: "expression", type: "contents", label: "Expression", is_hierarchical: true },
  ];

  const pos = computeGraphLayout("r1", "release", nodes, links, "hierarchy");
  assert.equal(pos.get("r1")!.y, 0);
  assert.ok(pos.get("a1")!.y < pos.get("r1")!.y);
  assert.ok(pos.get("w1")!.y < pos.get("r1")!.y);
  assert.ok(pos.get("r1")!.y < pos.get("m1")!.y);
  assert.ok(pos.get("m1")!.y < pos.get("t1")!.y);
  assert.ok(pos.get("t1")!.y < pos.get("e1")!.y);
  assert.ok(pos.get("m1")!.x < pos.get("m2")!.x);
  assert.ok(Math.abs(pos.get("t2")!.x - pos.get("t1")!.x) >= 200);
  assert.ok(Math.abs(pos.get("t3")!.x - pos.get("t2")!.x) >= 200);
});

test("withAncestorBridgeLinks keeps parent path back to center when filtering deep edges", () => {
  const allLinks: GraphLink[] = [
    { id: "e-rm", source: "r1", target: "m1", type: "release_id", label: "Medium", is_hierarchical: true },
    { id: "e-mt", source: "m1", target: "t1", type: "medium_id", label: "Track", is_hierarchical: true },
    { id: "e-ta", source: "a1", target: "t1", type: "vocal", label: "Vocal" },
  ];
  const bridged = withAncestorBridgeLinks("r1", allLinks, [allLinks[2]]);
  assert.deepEqual(bridged.map((l) => l.id), ["e-rm", "e-mt", "e-ta"]);
});

test("buildDisplayGraph folds deep leaf fan-outs while preserving intermediate hierarchy", () => {
  const nodes: GraphNode[] = [
    { id: "r1", name: "Album", type: "release", category: "release", level: 0 },
    { id: "m1", name: "Disc 1", type: "medium", category: "medium", level: 1 },
  ];
  const links: GraphLink[] = [
    { id: "rm", source: "r1", target: "m1", type: "release_id", label: "Disc", is_hierarchical: true },
  ];
  for (let i = 1; i <= 7; i++) {
    nodes.push({ id: `t${i}`, name: `Track ${i}`, type: "track", category: "track", level: 2, order: i });
    links.push({ id: `mt${i}`, source: "m1", target: `t${i}`, type: "medium_id", label: "Track", is_hierarchical: true });
  }

  const folded = buildDisplayGraph("r1", nodes, links, true, new Set(), 6);
  assert.equal(folded.canGroup, true);
  assert.ok(folded.nodes.some((n) => n.id === "m1"));
  assert.ok(folded.nodes.some((n) => n.id === "__hub:m1::medium_id"));
  assert.ok(!folded.nodes.some((n) => n.id === "t1"));

  const expanded = buildDisplayGraph("r1", nodes, links, true, new Set(["m1::medium_id"]), 6);
  assert.ok(expanded.nodes.some((n) => n.id === "t1"));
  assert.ok(expanded.nodes.some((n) => n.id === "t7"));
});
