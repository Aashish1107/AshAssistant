import { describe, expect, it } from "vitest";
import { parseRoute } from "./route";

describe("routes", () => {
  it("reads the address", () => {
    expect(parseRoute("")).toEqual({ view: "folder", path: "/" });
    expect(parseRoute("#/")).toEqual({ view: "folder", path: "/" });
    expect(parseRoute("#/Photos/2024/")).toEqual({ view: "folder", path: "/Photos/2024" });
    expect(parseRoute("#/My%20%231")).toEqual({ view: "folder", path: "/My #1" });
    expect(parseRoute("#play=/Videos/a%20b.mp4")).toEqual({ view: "player", path: "/Videos/a b.mp4" });
    expect(parseRoute("#view=/x.jpg")).toEqual({ view: "viewer", path: "/x.jpg" });
    expect(parseRoute("#share")).toEqual({ view: "share" });
    expect(parseRoute("#%E0%A4%A")).toEqual({ view: "folder", path: "/" });
  });
});
