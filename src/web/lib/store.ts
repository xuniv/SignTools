import type { VaultModel } from "./vault";
export interface StoreApp {
  id: string;
  bundleId: string;
  name: string;
  version?: string;
  price?: string | number;
  priceKnown?: boolean;
  owned?: boolean;
  artworkUrl?: string;
  metadataError?: string;
  platforms?: string[];
  purchaseDate?: string;
}
export interface StoreVersion {
  id: string;
  version?: string;
  name?: string;
  date?: string;
  error?: string;
}
export function storeTitle(app: Pick<StoreApp, "id" | "name" | "bundleId">) {
  return app.name?.trim() || app.bundleId?.trim() || `App #${app.id}`;
}
export function compactDate(value?: string) {
  if (!value) return "";
  const date = new Date(value);
  return Number.isFinite(date.getTime())
    ? date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })
    : "";
}
export function storePrice(app: Pick<StoreApp, "price" | "priceKnown">) {
  if (app.priceKnown !== true || app.price === undefined || app.price === null || app.price === "") return "";
  return Number(app.price) === 0 ? "Free" : String(app.price);
}
export function modelTitle(model: Pick<VaultModel, "id" | "originalFilename">) {
  return model.originalFilename?.trim() || `Model ${model.id.slice(0, 8)}`;
}
export function decryptionModels(models: VaultModel[], selection: string) {
  return (selection === "auto" ? models : models.filter((model) => model.id === selection)).map((model) => ({
    id: model.id,
    name: modelTitle(model),
    sealed: model.sealed,
  }));
}
