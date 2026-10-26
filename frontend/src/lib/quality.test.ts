import { describe, expect, test } from "bun:test";
import { qualityIssueTab } from "./quality";

describe("qualityIssueTab", () => {
  test("maps diagnostic codes to detail tabs", () => {
    expect(qualityIssueTab("dangling_reference")).toBe("relations");
    expect(qualityIssueTab("missing_required_field")).toBe("overview");
    expect(qualityIssueTab("release_missing_media")).toBe("contents");
    expect(qualityIssueTab("medium_missing_tracks")).toBe("contents");
    expect(qualityIssueTab("track_empty_contents")).toBe("contents");
    expect(qualityIssueTab("track_position_gap")).toBe("contents");
    expect(qualityIssueTab("unknown_code")).toBe("overview");
  });
});
