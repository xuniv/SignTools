import { describe, expect, test } from "bun:test";
import { compareVersions, groupApps, preferredId, type AppItem } from "./data";
const app = (partial: Partial<AppItem>) =>
  ({
    Id: "1",
    Name: "Example",
    BundleId: "com.signed.example",
    Version: "1.0",
    ModTime: "2026-11-17T00:00:00Z",
    ...partial,
  }) as AppItem;
describe("app library", () => {
  test("groups original identities across profiles and sorts numeric versions", () => {
    const groups = groupApps([
      app({ Id: "a", GroupId: "com.example", Version: "2.9", ProfileName: "One" }),
      app({ Id: "b", GroupId: "com.example", BundleId: "other.signed", Version: "2.10", ProfileName: "Two" }),
      app({ Id: "c", GroupId: "com.example", Version: "2.10" }),
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0]?.versions.map((v) => v.version)).toEqual(["2.10", "2.9"]);
    expect(groups[0]?.versions[0]?.builds).toHaveLength(2);
  });
  test("stable releases sort after prereleases", () => {
    expect(compareVersions("1.0", "1.0-beta")).toBeGreaterThan(0);
    expect(compareVersions("1.0-beta.10", "1.0-beta.2")).toBeGreaterThan(0);
    expect(compareVersions("1.0", "1.0.0")).toBe(0);
    expect(compareVersions("1.0+build1", "1.0+build2")).toBe(0);
    expect(compareVersions("1.0-1", "1.0-alpha")).toBeLessThan(0);
  });
  test("missing source identities stay separate by filename", () => {
    expect(
      groupApps([app({ BundleId: "", Name: "One.ipa" }), app({ Id: "2", BundleId: "", Name: "Two.ipa" })]),
    ).toHaveLength(2);
  });
  test("uses valid browser choice then server name/id then first item", () => {
    const items = [
      { Id: "a", Name: "Fast" },
      { Id: "b", Name: "Remote" },
    ];
    expect(preferredId(items, "b", "a")).toBe("b");
    expect(preferredId(items, "gone", "Remote")).toBe("b");
    expect(preferredId(items, null, "unknown")).toBe("a");
    expect(preferredId([], null, "")).toBe("");
  });
});
