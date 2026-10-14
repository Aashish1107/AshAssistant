import { describe, expect, it } from "vitest";
import { baseName, crumbs, encodePath, formatClock, formatDate, formatDuration, formatSize, joinPath, kindOf, nameProblem, parentOf } from "./paths";

describe("paths", () => {
  it("joins, splits and encodes", () => {
    expect(joinPath("/", "a.jpg")).toBe("/a.jpg");
    expect(joinPath("/Photos", "a.jpg")).toBe("/Photos/a.jpg");
    expect(parentOf("/Photos/a.jpg")).toBe("/Photos");
    expect(parentOf("/a.jpg")).toBe("/");
    expect(baseName("/Photos/a b.jpg")).toBe("a b.jpg");
    expect(encodePath("/My #1/100%.jpg")).toBe("/My%20%231/100%25.jpg");
    expect(crumbs("/Photos/2024").map((c) => c.path)).toEqual(["/", "/Photos", "/Photos/2024"]);
    expect(crumbs("/")).toHaveLength(1);
  });

  it("knows what a file is", () => {
    expect(kindOf({ type: "folder" })).toBe("folder");
    expect(kindOf({ type: "file", content_type: "image/jpeg" })).toBe("image");
    expect(kindOf({ type: "file", content_type: "video/mp4" })).toBe("video");
    expect(kindOf({ type: "file", content_type: "application/pdf" })).toBe("pdf");
    expect(kindOf({ type: "file", content_type: "application/zip" })).toBe("archive");
    expect(kindOf({ type: "file", content_type: "application/octet-stream" })).toBe("file");
  });

  it("checks names the way Windows does", () => {
    expect(nameProblem("Holiday 2026")).toBeNull();
    expect(nameProblem("a:b")).not.toBeNull();
    expect(nameProblem("trailing.")).not.toBeNull();
    expect(nameProblem("  ")).not.toBeNull();
  });
});

describe("formatting", () => {
  it("formats sizes", () => {
    expect(formatSize(0)).toBe("0 B");
    expect(formatSize(1536)).toBe("1.5 KB");
    expect(formatSize(3_000_000)).toBe("2.9 MB");
    expect(formatSize(150 * 1024 * 1024)).toBe("150 MB");
  });

  it("formats dates relative to today", () => {
    const now = new Date(2026, 9, 14, 12, 0);
    expect(formatDate(new Date(2026, 9, 14, 9, 30).toISOString(), now)).toMatch(/^Today /);
    expect(formatDate(new Date(2026, 9, 13, 23, 0).toISOString(), now)).toBe("Yesterday");
    expect(formatDate(new Date(2026, 2, 1).toISOString(), now)).not.toMatch(/2026/);
    expect(formatDate(new Date(2024, 2, 1).toISOString(), now)).toMatch(/2024/);
    expect(formatDate("nonsense", now)).toBe("");
  });

  it("formats durations and the video clock", () => {
    expect(formatDuration(12.2)).toBe("13 s");
    expect(formatDuration(150)).toBe("3 min");
    expect(formatDuration(5400)).toBe("1.5 h");
    expect(formatClock(65)).toBe("1:05");
    expect(formatClock(3725)).toBe("1:02:05");
    expect(formatClock(NaN)).toBe("0:00");
  });
});
