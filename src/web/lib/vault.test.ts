import { describe, expect, test } from "bun:test";
import { strFromU8, unzipSync, zipSync } from "fflate";
import {
  clientId,
  createBackup,
  loadAccounts,
  loadModels,
  restoreBackup,
  saveAccount,
  saveModel,
  type StorageLike,
} from "./vault";

class MemoryStorage implements StorageLike {
  values = new Map<string, string>();
  get length() {
    return this.values.size;
  }
  key(index: number) {
    return [...this.values.keys()][index] ?? null;
  }
  getItem(key: string) {
    return this.values.get(key) ?? null;
  }
  setItem(key: string, value: string) {
    this.values.set(key, value);
  }
  removeItem(key: string) {
    this.values.delete(key);
  }
}

describe("browser account and model vault", () => {
  test("concurrent account and model saves preserve every record", async () => {
    const store = new MemoryStorage();
    await Promise.all(
      Array.from({ length: 16 }, (_, index) =>
        saveAccount(
          {
            id: "account-" + index,
            email: "account-" + index + "@example.com",
            name: "Account",
            country: "US",
            storefront: "143441",
            session: "st1.session-" + index,
          },
          store,
        ),
      ),
    );
    await Promise.all(
      Array.from({ length: 16 }, (_, index) =>
        saveModel(
          { id: "model-" + index, name: "Model " + index, supported: true, sealed: "st1.model-" + index },
          store,
        ),
      ),
    );
    expect((await loadAccounts(store)).length).toBe(16);
    expect((await loadModels(store)).length).toBe(16);
  });
  test("encrypts all account/model metadata and keeps a stable browser identity", async () => {
    const store = new MemoryStorage();
    const owner = clientId(store);
    expect(clientId(store)).toBe(owner);
    await saveAccount(
      {
        id: "local-one",
        email: "private@example.com",
        name: "Private Name",
        country: "KR",
        storefront: "143466",
        session: "st1.private-session",
      },
      store,
    );
    await saveModel(
      {
        id: "local-two",
        name: "private-model.bin",
        selector: "private-selector",
        supported: true,
        sealed: "st1.private-model",
      },
      store,
    );
    const persisted = JSON.stringify([...store.values]);
    expect(persisted).not.toContain("private@example.com");
    expect(persisted).not.toContain("Private Name");
    expect(persisted).not.toContain("private-model");
    expect((await loadAccounts(store))[0]?.session).toBe("st1.private-session");
    expect((await loadModels(store))[0]?.selector).toBe("private-selector");
  });

  test("encrypted ZIP restores the browser key, accounts, models, repos and preferences", async () => {
    const source = new MemoryStorage();
    source.setItem("signtools.repositories", '[{"url":"https://private.example/repo"}]');
    source.setItem("signtools.theme", "dark");
    source.setItem("unrelated.setting", "must-stay-local");
    await saveAccount(
      {
        id: "one",
        email: "secret@example.com",
        name: "Secret",
        country: "US",
        storefront: "143441",
        session: "st1.opaque",
      },
      source,
    );
    await saveModel(
      { id: "two", name: "matching.bin", selector: "selector", supported: true, sealed: "st1.model" },
      source,
    );
    const backup = await createBackup("correct passphrase 123", source);
    const container = strFromU8(unzipSync(backup)["signtools.encrypted.json"]!);
    expect(container).not.toContain("secret@example.com");
    expect(container).not.toContain(source.getItem("signtools.vault.key")!);
    expect(container).not.toContain("private.example");
    const destination = new MemoryStorage();
    destination.setItem("unrelated.setting", "keep");
    destination.setItem("signtools.theme", "light");
    await restoreBackup(backup, "correct passphrase 123", destination);
    expect((await loadAccounts(destination))[0]?.email).toBe("secret@example.com");
    expect((await loadModels(destination))[0]?.sealed).toBe("st1.model");
    expect(destination.getItem("signtools.theme")).toBe("dark");
    expect(destination.getItem("signtools.repositories")).toBe(source.getItem("signtools.repositories"));
    expect(destination.getItem("unrelated.setting")).toBe("keep");
    expect(destination.getItem("signtools.vault.key")).toBe(source.getItem("signtools.vault.key"));
  });

  test("a wrong passphrase never changes existing browser data", async () => {
    const source = new MemoryStorage();
    const backup = await createBackup("correct passphrase 123", source);
    const destination = new MemoryStorage();
    destination.setItem("signtools.theme", "existing");
    await expect(restoreBackup(backup, "incorrect passphrase", destination)).rejects.toThrow(
      "Current browser data was kept",
    );
    expect(destination.getItem("signtools.theme")).toBe("existing");
    expect(destination.length).toBe(1);
  });

  test("rejects compressed and multi-member archives before decryption", async () => {
    const source = new MemoryStorage();
    const backup = await createBackup("correct passphrase 123", source);
    const contents = unzipSync(backup);
    const compressed = zipSync(contents, { level: 9 });
    const extra = zipSync({ ...contents, "extra.bin": new Uint8Array(1) }, { level: 0 });
    const destination = new MemoryStorage();
    destination.setItem("signtools.theme", "existing");
    for (const archive of [compressed, extra])
      await expect(restoreBackup(archive, "correct passphrase 123", destination)).rejects.toThrow(
        "Current browser data was kept",
      );
    expect(destination.getItem("signtools.theme")).toBe("existing");
  });
});
test("original model filename and parsed type are encrypted separately and survive backup", async () => {
  const store = new MemoryStorage();
  const filename = "KR device enrollment original.filename.model";
  await saveModel(
    {
      id: "identity",
      name: filename,
      originalFilename: filename,
      typeName: "Parsed FairPlay model",
      supported: false,
      sealed: "sealed-body",
    },
    store,
  );
  expect((await loadModels(store))[0]).toMatchObject({
    originalFilename: filename,
    typeName: "Parsed FairPlay model",
  });
  expect([...store.values.values()].join("")).not.toContain(filename);
  const backup = await createBackup("valid backup passphrase", store);
  const restored = new MemoryStorage();
  await restoreBackup(backup, "valid backup passphrase", restored);
  expect((await loadModels(restored))[0]?.originalFilename).toBe(filename);
});
