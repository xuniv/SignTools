import { strFromU8, strToU8, zipSync } from "fflate";

export interface StorageLike {
  readonly length: number;
  key(index: number): string | null;
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export type VaultAccount = {
  id: string;
  email: string;
  name: string;
  country: string;
  storefront: string;
  session: string;
};

export type VaultModel = {
  id: string;
  name: string;
  originalFilename?: string;
  typeName?: string;
  selector?: string;
  formatVersion?: number | string;
  supported: boolean;
  sealed: string;
};

type Box = { version: 1; iv: string; data: string };
type RecordBox = { id: string; box: Box };
const PREFIX = "signtools.";
const KEY = PREFIX + "vault.key";
const CLIENT = PREFIX + "vault.client-id";
const ITERATIONS = 600_000;
const LIMIT = 64 * 1024 * 1024;

function currentStorage(): StorageLike {
  if (typeof localStorage === "undefined") throw new Error("Browser storage is unavailable.");
  return localStorage;
}

function subtle(): SubtleCrypto {
  if (!globalThis.crypto?.subtle)
    throw new Error("Encrypted accounts, models and backups require HTTPS or localhost.");
  return globalThis.crypto.subtle;
}

export function toBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 8192)
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
  return btoa(binary);
}

function fromBase64(value: string): Uint8Array<ArrayBuffer> {
  const binary = atob(value);
  const output = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) output[i] = binary.charCodeAt(i);
  return output;
}

async function browserKey(store: StorageLike): Promise<CryptoKey> {
  const api = subtle();
  let encoded = store.getItem(KEY);
  if (!encoded) {
    encoded = toBase64(crypto.getRandomValues(new Uint8Array(32)));
    store.setItem(KEY, encoded);
  }
  const raw = fromBase64(encoded);
  if (raw.length !== 32) throw new Error("The browser encryption key is invalid. Restore a saved backup.");
  return api.importKey("raw", raw, "AES-GCM", false, ["encrypt", "decrypt"]);
}

export function clientId(store: StorageLike = currentStorage()): string {
  let value = store.getItem(CLIENT);
  if (!value) {
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    value = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
    store.setItem(CLIENT, value);
  }
  return value;
}

async function encrypt(value: unknown, purpose: string, store: StorageLike): Promise<Box> {
  const key = await browserKey(store);
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const encrypted = await subtle().encrypt(
    { name: "AES-GCM", iv, additionalData: strToU8("signtools:browser:v1:" + purpose) },
    key,
    strToU8(JSON.stringify(value)),
  );
  return { version: 1, iv: toBase64(iv), data: toBase64(new Uint8Array(encrypted)) };
}

async function decrypt<T>(box: Box, purpose: string, store: StorageLike): Promise<T> {
  if (box.version !== 1) throw new Error("This browser vault format is not supported.");
  try {
    const plain = await subtle().decrypt(
      {
        name: "AES-GCM",
        iv: fromBase64(box.iv),
        additionalData: strToU8("signtools:browser:v1:" + purpose),
      },
      await browserKey(store),
      fromBase64(box.data),
    );
    return JSON.parse(strFromU8(new Uint8Array(plain))) as T;
  } catch {
    throw new Error("This browser vault could not be unlocked. Restore its matching backup.");
  }
}

function records(kind: "accounts" | "models", store: StorageLike): RecordBox[] {
  const value = store.getItem(PREFIX + "vault." + kind);
  if (!value) return [];
  const parsed: unknown = JSON.parse(value);
  if (!Array.isArray(parsed)) throw new Error("The saved browser vault is damaged.");
  return parsed as RecordBox[];
}

async function save<T extends { id: string }>(
  kind: "accounts" | "models",
  value: T,
  store: StorageLike,
): Promise<void> {
  const box = await encrypt(value, kind, store);
  // Crypto yields to other requests. Read the latest collection only after it
  // completes, then perform the localStorage update without an intervening await.
  const all = records(kind, store).filter((record) => record.id !== value.id);
  all.push({ id: value.id, box });
  try {
    store.setItem(PREFIX + "vault." + kind, JSON.stringify(all));
  } catch {
    throw new Error("Browser storage is full. Remove unused models or export a backup before continuing.");
  }
}

function remove(kind: "accounts" | "models", id: string, store: StorageLike): void {
  store.setItem(PREFIX + "vault." + kind, JSON.stringify(records(kind, store).filter((record) => record.id !== id)));
}

export const loadAccounts = async (store: StorageLike = currentStorage()): Promise<VaultAccount[]> =>
  Promise.all(records("accounts", store).map((record) => decrypt<VaultAccount>(record.box, "accounts", store)));
export const loadModels = async (store: StorageLike = currentStorage()): Promise<VaultModel[]> =>
  Promise.all(records("models", store).map((record) => decrypt<VaultModel>(record.box, "models", store)));
export const saveAccount = (value: VaultAccount, store: StorageLike = currentStorage()) =>
  save("accounts", value, store);
export const saveModel = (value: VaultModel, store: StorageLike = currentStorage()) => save("models", value, store);
export const removeAccount = (id: string, store: StorageLike = currentStorage()) => remove("accounts", id, store);
export const removeModel = (id: string, store: StorageLike = currentStorage()) => remove("models", id, store);

function entries(store: StorageLike): Record<string, string> {
  const all: Record<string, string> = Object.create(null);
  for (let index = 0; index < store.length; index++) {
    const key = store.key(index);
    if (key?.startsWith(PREFIX)) {
      const value = store.getItem(key);
      if (value !== null) all[key] = value;
    }
  }
  return all;
}

async function backupKey(passphrase: string, salt: Uint8Array<ArrayBuffer>): Promise<CryptoKey> {
  if (passphrase.length < 10) throw new Error("Use a backup passphrase with at least 10 characters.");
  const material = await subtle().importKey("raw", strToU8(passphrase), "PBKDF2", false, ["deriveKey"]);
  return subtle().deriveKey(
    { name: "PBKDF2", hash: "SHA-256", salt, iterations: ITERATIONS },
    material,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"],
  );
}

export async function createBackup(passphrase: string, store: StorageLike = currentStorage()): Promise<Uint8Array> {
  await browserKey(store);
  clientId(store);
  const salt = crypto.getRandomValues(new Uint8Array(32));
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const body = strToU8(JSON.stringify({ schema: "signtools-browser-vault", version: 1, entries: entries(store) }));
  if (body.length > LIMIT) throw new Error("The browser vault is too large to export.");
  const data = await subtle().encrypt(
    { name: "AES-GCM", iv, additionalData: strToU8("signtools:backup:v1") },
    await backupKey(passphrase, salt),
    body,
  );
  const envelope = {
    schema: "signtools-encrypted-backup",
    version: 1,
    kdf: "PBKDF2-SHA256",
    iterations: ITERATIONS,
    salt: toBase64(salt),
    iv: toBase64(iv),
    ciphertext: toBase64(new Uint8Array(data)),
  };
  return zipSync({ "signtools.encrypted.json": strToU8(JSON.stringify(envelope)) }, { level: 0 });
}

function storedBackupPayload(bytes: Uint8Array): Uint8Array {
  // Our exporter writes one uncompressed ZIP member. Validate both directory
  // records before reading it, so an unauthenticated backup never invokes a
  // decompressor or trusts an attacker-supplied expanded-size declaration.
  if (bytes.length < 100 || bytes.length > LIMIT * 2) throw new Error("Invalid backup archive");
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const end = bytes.length - 22;
  if (
    view.getUint32(end, true) !== 0x06054b50 ||
    view.getUint16(end + 4, true) !== 0 ||
    view.getUint16(end + 6, true) !== 0 ||
    view.getUint16(end + 8, true) !== 1 ||
    view.getUint16(end + 10, true) !== 1 ||
    view.getUint16(end + 20, true) !== 0
  )
    throw new Error("Invalid backup directory");
  const centralSize = view.getUint32(end + 12, true);
  const central = view.getUint32(end + 16, true);
  if (central + centralSize !== end || centralSize < 46 || view.getUint32(central, true) !== 0x02014b50)
    throw new Error("Invalid backup directory");
  const flags = view.getUint16(central + 8, true);
  const size = view.getUint32(central + 20, true);
  const originalSize = view.getUint32(central + 24, true);
  const nameLength = view.getUint16(central + 28, true);
  const extraLength = view.getUint16(central + 30, true);
  const commentLength = view.getUint16(central + 32, true);
  if (
    (flags & 9) !== 0 ||
    view.getUint16(central + 10, true) !== 0 ||
    size !== originalSize ||
    size > LIMIT * 2 ||
    46 + nameLength + extraLength + commentLength !== centralSize ||
    view.getUint32(central + 42, true) !== 0 ||
    strFromU8(bytes.subarray(central + 46, central + 46 + nameLength)) !== "signtools.encrypted.json"
  )
    throw new Error("Unsupported backup archive");
  if (
    view.getUint32(0, true) !== 0x04034b50 ||
    view.getUint16(6, true) !== flags ||
    view.getUint16(8, true) !== 0 ||
    view.getUint32(18, true) !== size ||
    view.getUint32(22, true) !== size
  )
    throw new Error("Invalid backup member");
  const localNameLength = view.getUint16(26, true);
  const localExtraLength = view.getUint16(28, true);
  const start = 30 + localNameLength + localExtraLength;
  if (strFromU8(bytes.subarray(30, 30 + localNameLength)) !== "signtools.encrypted.json" || start + size !== central)
    throw new Error("Invalid backup member");
  return bytes.subarray(start, start + size);
}

export async function restoreBackup(
  bytes: Uint8Array,
  passphrase: string,
  store: StorageLike = currentStorage(),
): Promise<void> {
  if (bytes.length > LIMIT * 2) throw new Error("The backup is too large.");
  let restored: Record<string, string>;
  try {
    const file = storedBackupPayload(bytes);
    const envelope = JSON.parse(strFromU8(file)) as {
      schema: string;
      version: number;
      kdf: string;
      iterations: number;
      salt: string;
      iv: string;
      ciphertext: string;
    };
    if (
      envelope.schema !== "signtools-encrypted-backup" ||
      envelope.version !== 1 ||
      envelope.kdf !== "PBKDF2-SHA256" ||
      envelope.iterations !== ITERATIONS
    )
      throw new Error("Unsupported backup format");
    const plain = await subtle().decrypt(
      { name: "AES-GCM", iv: fromBase64(envelope.iv), additionalData: strToU8("signtools:backup:v1") },
      await backupKey(passphrase, fromBase64(envelope.salt)),
      fromBase64(envelope.ciphertext),
    );
    if (plain.byteLength > LIMIT) throw new Error("Backup payload too large");
    const body = JSON.parse(strFromU8(new Uint8Array(plain))) as {
      schema: string;
      version: number;
      entries: Record<string, unknown>;
    };
    if (
      body.schema !== "signtools-browser-vault" ||
      body.version !== 1 ||
      !body.entries ||
      Array.isArray(body.entries)
    )
      throw new Error("Unsupported browser vault");
    restored = Object.create(null) as Record<string, string>;
    if (Object.keys(body.entries).length > 1024) throw new Error("Too many settings");
    for (const [key, value] of Object.entries(body.entries)) {
      if (!key.startsWith(PREFIX) || key.length > 256 || typeof value !== "string")
        throw new Error("Invalid browser setting");
      restored[key] = value;
    }
    if (!restored[KEY] || fromBase64(restored[KEY]).length !== 32) throw new Error("Missing browser key");
    // Authenticate the saved records before changing any current browser state.
    const temporary: StorageLike = {
      length: Object.keys(restored).length,
      key: (index) => Object.keys(restored)[index] ?? null,
      getItem: (key) => restored[key] ?? null,
      setItem: (key, value) => {
        restored[key] = value;
      },
      removeItem: (key) => {
        delete restored[key];
      },
    };
    await loadAccounts(temporary);
    await loadModels(temporary);
  } catch {
    throw new Error("Incorrect passphrase or damaged backup. Current browser data was kept.");
  }
  const previous = entries(store);
  const replace = (values: Record<string, string>) => {
    for (const key of Object.keys(entries(store))) store.removeItem(key);
    for (const [key, value] of Object.entries(values)) store.setItem(key, value);
  };
  try {
    replace(restored);
  } catch {
    replace(previous);
    throw new Error("Not enough browser storage to restore this backup. Current data was kept.");
  }
}
