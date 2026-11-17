import { expect, test } from "bun:test";
import { compactDate, decryptionModels, modelTitle, storePrice, storeTitle } from "./store";
import type { VaultModel } from "./vault";
test("purchase rows have readable fallbacks and never infer unknown prices", () => {
  expect(storeTitle({ id: "123456", name: "", bundleId: "" })).toBe("App #123456");
  expect(storeTitle({ id: "1", name: "  ", bundleId: "com.example.app" })).toBe("com.example.app");
  expect(storePrice({ price: 0, priceKnown: false })).toBe("");
  expect(storePrice({ price: 0 })).toBe("");
  expect(storePrice({ price: 0, priceKnown: true })).toBe("Free");
  const date = compactDate("2026-11-17T12:34:56.123456Z");
  expect(date).not.toContain("T12:");
  expect(date.length).toBeGreaterThan(0);
  expect(compactDate("not-a-date")).toBe("");
});
test("automatic decryption sends every saved model regardless of stale support labels", () => {
  const models: VaultModel[] = [
    {
      id: "a",
      name: "Overwritten type",
      originalFilename: "South Korea original.model",
      typeName: "Type metadata",
      supported: false,
      sealed: "sealed-a",
    },
    { id: "b12345678", name: "Generic type lost filename", supported: true, sealed: "sealed-b" },
  ];
  expect(modelTitle(models[0]!)).toBe("South Korea original.model");
  expect(modelTitle(models[1]!)).toBe("Model b1234567");
  expect(decryptionModels(models, "auto").map((model) => model.id)).toEqual(["a", "b12345678"]);
  expect(decryptionModels(models, "auto")[0]?.name).toBe("South Korea original.model");
  expect(decryptionModels(models, "b12345678")).toHaveLength(1);
});
