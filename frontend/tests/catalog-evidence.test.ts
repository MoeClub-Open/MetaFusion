import { test } from "node:test";
import assert from "node:assert/strict";
import { catalogEvidenceError } from "../src/lib/catalogEvidence";

test("metadata changes require user-supplied eligible sources", () => {
  assert.equal(catalogEvidenceError("reason", []), "evidence_required");
  assert.equal(catalogEvidenceError("reason", [{ kind: "self", citation: "reason" }]), "source_not_eligible");
  assert.equal(catalogEvidenceError("reason", [{ kind: "url", citation: "official page", url: "https://instance.test/catalog/id" }], "https://instance.test"), "source_not_eligible");
  assert.equal(catalogEvidenceError("reason", [{ kind: "publication", citation: "book", url: "https://instance.test/reference" }], "https://instance.test"), "source_not_eligible");
  assert.equal(catalogEvidenceError("reason", [{ kind: "url", citation: "official page", url: "file:///reference" }]), "invalid_source");
});

test("external URLs and explicit publication citations remain eligible", () => {
  assert.equal(catalogEvidenceError("reason", [{ kind: "url", citation: "official page", url: "https://publisher.test/work" }], "https://instance.test"), null);
  assert.equal(catalogEvidenceError("reason", [{ kind: "publication", citation: "Author, title, publisher, 2026, p. 12" }]), null);
  assert.equal(catalogEvidenceError("", [{ kind: "publication", citation: "book" }]), "evidence_required");
  assert.equal(catalogEvidenceError("reason", [{ kind: "url", citation: "page" }]), "invalid_source");
});
