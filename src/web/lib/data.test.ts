import { describe, expect, test } from "bun:test";
import { compareVersions, groupApps, preferredId, prefillOutput, sourceFromApp, type AppItem } from "./data";
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
test("keeps original version identity and separates signed bundle variants", () => {
  const groups = groupApps([
    app({
      Id: "one",
      GroupId: "com.original",
      OriginalVersion: "1.0",
      Version: "1.0",
      SignedBundleId: "com.variant.one",
      SignedDisplayName: "One",
    }),
    app({
      Id: "two",
      GroupId: "com.original",
      OriginalVersion: "1.0",
      Version: "9.0",
      SignedBundleId: "com.variant.two",
      SignedDisplayName: "Two",
    }),
    app({
      Id: "three",
      GroupId: "com.original",
      OriginalVersion: "1.0",
      SignedBundleId: "com.variant.one",
      ProfileName: "Other profile",
    }),
  ]);
  expect(groups).toHaveLength(1);
  expect(groups[0]?.versions).toHaveLength(1);
  expect(groups[0]?.versions[0]?.variants).toHaveLength(2);
  expect(
    groups[0]?.versions[0]?.variants.find((variant) => variant.bundleId === "com.variant.one")?.builds,
  ).toHaveLength(2);
});

test("async IPA prefill preserves manually edited output ID and name", () => {
  const original = { bundleId: "com.original", displayName: "Original", version: "1.2", buildVersion: "3" };
  expect(
    prefillOutput(
      { bundleId: "com.custom", displayName: "Custom", bundleIdEdited: true, displayNameEdited: true },
      original,
    ),
  ).toEqual({ bundleId: "com.custom", displayName: "Custom", bundleIdEdited: true, displayNameEdited: true });
  expect(
    prefillOutput({ bundleId: "", displayName: "", bundleIdEdited: false, displayNameEdited: false }, original)
      .displayName,
  ).toBe("Original");
  expect(
    sourceFromApp(
      app({
        GroupId: "com.original",
        OriginalDisplayName: "Original",
        SignedDisplayName: "Edited",
        OriginalVersion: "1.2",
        Version: "9.0",
        OriginalBuildVersion: "3",
      }),
    ).metadata,
  ).toMatchObject(original);
});
