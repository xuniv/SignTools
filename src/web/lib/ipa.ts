import { BlobReader, ZipReader, type FileEntry } from "@zip.js/zip.js";

export interface IPAMetadata {
  bundleId: string;
  displayName: string;
  version: string;
  buildVersion: string;
}
const LIMIT = 1 << 20;
const fail = () => new Error("The IPA metadata is invalid or exceeds the preview limits.");
function asMetadata(value: unknown): IPAMetadata {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw fail();
  const dict = value as Record<string, unknown>;
  const text = (key: string) =>
    typeof dict[key] === "string"
      ? (dict[key] as string).replace(/\0/g, "").slice(0, 4096)
      : typeof dict[key] === "number"
        ? String(dict[key])
        : "";
  const bundleId = text("CFBundleIdentifier");
  if (!bundleId) throw new Error("The main Info.plist has no bundle identifier.");
  return {
    bundleId,
    displayName: text("CFBundleDisplayName") || text("CFBundleName"),
    version: text("CFBundleShortVersionString"),
    buildVersion: text("CFBundleVersion"),
  };
}
function entities(value: string) {
  const known: Record<string, string> = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'" };
  const parts: string[] = [];
  let at = 0;
  while (at < value.length) {
    const amp = value.indexOf("&", at);
    if (amp < 0) {
      parts.push(value.slice(at));
      break;
    }
    parts.push(value.slice(at, amp));
    const end = value.indexOf(";", amp + 1);
    if (end < 0 || end - amp > 16) throw fail();
    const entity = value.slice(amp + 1, end);
    if (Object.hasOwn(known, entity)) parts.push(known[entity]!);
    else {
      if (!/^#(?:x[0-9a-f]+|[0-9]+)$/i.test(entity)) throw fail();
      const numeric = entity[1]?.toLowerCase() === "x" ? parseInt(entity.slice(2), 16) : parseInt(entity.slice(1), 10);
      if (!Number.isInteger(numeric) || numeric < 0 || numeric > 0x10ffff || (numeric >= 0xd800 && numeric <= 0xdfff))
        throw fail();
      parts.push(String.fromCodePoint(numeric));
    }
    at = end + 1;
  }
  return parts.join("");
}
function xmlPlist(bytes: Uint8Array): unknown {
  const xml = new TextDecoder("utf-8", { fatal: true }).decode(bytes).replace(/^\uFEFF/, "");
  const tokens: string[] = [];
  let cursor = 0;
  // Scan monotonically: malformed repeated XML starts must not cause quadratic regex backtracking.
  while (cursor < xml.length) {
    if (tokens.length > 40000) throw fail();
    if (xml.startsWith("<!--", cursor)) {
      const end = xml.indexOf("-->", cursor + 4);
      if (end < 0) throw fail();
      cursor = end + 3;
      continue;
    }
    if (xml.startsWith("<?xml", cursor)) {
      const end = xml.indexOf("?>", cursor + 5);
      if (end < 0) throw fail();
      cursor = end + 2;
      continue;
    }
    if (xml.startsWith("<!DOCTYPE", cursor)) {
      const end = xml.indexOf(">", cursor + 9);
      if (end < 0 || xml.slice(cursor, end).includes("[")) throw fail();
      cursor = end + 1;
      continue;
    }
    if (xml.startsWith("<![CDATA[", cursor)) {
      const end = xml.indexOf("]]>", cursor + 9);
      if (end < 0) throw fail();
      tokens.push(xml.slice(cursor, end + 3));
      cursor = end + 3;
      continue;
    }
    if (xml[cursor] === "<") {
      const end = xml.indexOf(">", cursor + 1);
      if (end < 0 || end - cursor > 4096) throw fail();
      tokens.push(xml.slice(cursor, end + 1));
      cursor = end + 1;
    } else {
      const next = xml.indexOf("<", cursor);
      const end = next < 0 ? xml.length : next;
      tokens.push(xml.slice(cursor, end));
      cursor = end;
    }
  }
  let position = 0,
    nodes = 0;
  const skip = () => {
    while (position < tokens.length && /^\s*$/.test(tokens[position]!)) position++;
  };
  function node(depth: number): unknown {
    if (depth > 48 || ++nodes > 20000) throw fail();
    skip();
    const opening = tokens[position++];
    if (!opening) throw fail();
    const tag = /^<(plist|dict|array|key|string|integer|real|true|false|data|date)(?:\s+[^<>/]*?)?\s*(\/?)>$/.exec(
      opening,
    );
    if (!tag) throw fail();
    const name = tag[1]!,
      self = tag[2] === "/";
    if (self) {
      if (name === "true") return true;
      if (name === "false") return false;
      if (name === "dict") return Object.create(null);
      if (name === "array") return [];
      return "";
    }
    const end = `</${name}>`;
    if (name === "plist") {
      const child = node(depth + 1);
      skip();
      if (tokens[position++] !== end) throw fail();
      return child;
    }
    if (name === "dict") {
      const result: Record<string, unknown> = Object.create(null);
      skip();
      while (tokens[position] !== end) {
        if (!/^<key(?:\s[^<>]*)?\s*>$/.test(tokens[position] || "")) throw fail();
        const key = node(depth + 1);
        if (typeof key !== "string") throw fail();
        if (Object.hasOwn(result, key)) throw fail();
        result[key] = node(depth + 1);
        skip();
      }
      position++;
      return result;
    }
    if (name === "array") {
      const values: unknown[] = [];
      skip();
      while (tokens[position] !== end) {
        values.push(node(depth + 1));
        skip();
      }
      position++;
      return values;
    }
    let text = "";
    while (position < tokens.length && tokens[position] !== end) {
      const token = tokens[position++]!;
      if (token.startsWith("<![CDATA[")) text += token.slice(9, -3);
      else if (token.startsWith("<")) throw fail();
      else text += entities(token);
    }
    if (tokens[position++] !== end) throw fail();
    if (name === "true" || name === "false") return name === "true";
    if (name === "integer" || name === "real") return Number(text);
    return text;
  }
  const result = node(0);
  skip();
  if (position !== tokens.length) throw fail();
  return result;
}
function binaryPlist(bytes: Uint8Array): unknown {
  if (bytes.length < 40) throw fail();
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength),
    trailer = bytes.length - 32;
  function number(offset: number, size: number, maximum = Number.MAX_SAFE_INTEGER) {
    if (!Number.isInteger(size) || size < 1 || size > 8 || offset < 0 || offset + size > bytes.length) throw fail();
    let result = 0n;
    for (let i = 0; i < size; i++) result = result * 256n + BigInt(bytes[offset + i]!);
    if (result > BigInt(maximum)) throw fail();
    return Number(result);
  }
  const offsetSize = bytes[trailer + 6]!,
    refSize = bytes[trailer + 7]!,
    count = number(trailer + 8, 8, 20000),
    top = number(trailer + 16, 8),
    table = number(trailer + 24, 8);
  if (
    !count ||
    top >= count ||
    !refSize ||
    refSize > 8 ||
    !offsetSize ||
    offsetSize > 8 ||
    table < 8 ||
    table + count * offsetSize > trailer
  )
    throw fail();
  const offsets = Array.from({ length: count }, (_, index) =>
    number(table + index * offsetSize, offsetSize, table - 1),
  );
  const cache = new Map<number, unknown>(),
    visiting = new Set<number>();
  let parsed = 0;
  function object(index: number, depth: number): unknown {
    if (index >= count || index < 0 || depth > 48 || ++parsed > 40000) throw fail();
    if (cache.has(index)) return cache.get(index);
    if (visiting.has(index)) throw fail();
    visiting.add(index);
    let at = offsets[index]!;
    if (at < 8 || at >= table) throw fail();
    const marker = bytes[at++]!,
      kind = marker >> 4,
      info = marker & 15;
    const range = (length: number) => {
      if (!Number.isSafeInteger(length) || length < 0 || at + length > table) throw fail();
    };
    function length() {
      if (info < 15) return info;
      range(1);
      const sizeMarker = bytes[at++]!;
      if (sizeMarker >> 4 !== 1) throw fail();
      const size = 2 ** (sizeMarker & 15);
      range(size);
      const sizeValue = number(at, size, LIMIT);
      at += size;
      return sizeValue;
    }
    let result: unknown;
    if (kind === 0) {
      if (info === 0) result = null;
      else if (info === 8 || info === 9) result = info === 9;
      else throw fail();
    } else if (kind === 1) {
      const size = 2 ** info;
      range(size);
      result = number(at, size);
    } else if (kind === 2) {
      const size = 2 ** info;
      range(size);
      if (size !== 4 && size !== 8) throw fail();
      result = size === 4 ? view.getFloat32(at) : view.getFloat64(at);
    } else if (kind === 3) {
      range(8);
      result = view.getFloat64(at);
    } else if (kind === 4) {
      const len = length();
      range(len);
      result = null;
    } else if (kind === 5 || kind === 6 || kind === 7) {
      const len = length(),
        size = len * (kind === 6 ? 2 : 1);
      range(size);
      result = new TextDecoder(kind === 6 ? "utf-16be" : "utf-8", { fatal: true }).decode(
        bytes.subarray(at, at + size),
      );
    } else if (kind === 8) {
      range(info + 1);
      result = number(at, info + 1);
    } else if (kind === 10 || kind === 13) {
      const len = length();
      if (len > 20000) throw fail();
      range(len * refSize * (kind === 13 ? 2 : 1));
      if (kind === 10)
        result = Array.from({ length: len }, (_, entry) =>
          object(number(at + entry * refSize, refSize, count - 1), depth + 1),
        );
      else {
        const dict: Record<string, unknown> = Object.create(null);
        for (let entry = 0; entry < len; entry++) {
          const key = object(number(at + entry * refSize, refSize, count - 1), depth + 1);
          if (typeof key !== "string" || Object.hasOwn(dict, key)) throw fail();
          dict[key] = object(number(at + (len + entry) * refSize, refSize, count - 1), depth + 1);
        }
        result = dict;
      }
    } else throw fail();
    visiting.delete(index);
    cache.set(index, result);
    return result;
  }
  return object(top, 0);
}
export function parseInfoPlist(bytes: Uint8Array): IPAMetadata {
  if (!bytes.length || bytes.length > LIMIT) throw fail();
  return asMetadata(
    new TextDecoder().decode(bytes.subarray(0, 8)) === "bplist00" ? binaryPlist(bytes) : xmlPlist(bytes),
  );
}
export async function readIPAMetadata(file: Blob): Promise<IPAMetadata> {
  if (file.size < 22) throw new Error("Choose a valid IPA ZIP archive.");
  // Read the ZIP tail and central directory rather than loading a potentially multi-GB IPA.
  const tail = new Uint8Array(await file.slice(Math.max(0, file.size - 65557)).arrayBuffer()),
    tailView = new DataView(tail.buffer);
  let end = -1;
  for (let at = tail.length - 22; at >= 0; at--)
    if (tailView.getUint32(at, true) === 0x06054b50 && at + 22 + tailView.getUint16(at + 20, true) === tail.length) {
      end = at;
      break;
    }
  if (end < 0) throw new Error("The IPA has no valid ZIP directory.");
  if (tailView.getUint16(end + 10, true) > 25000 || tailView.getUint32(end + 12, true) > 16 << 20)
    throw new Error(
      "This archive exceeds the browser preview limits. You can still upload it and enter metadata manually.",
    );
  const zip = new ZipReader(new BlobReader(file), { useWebWorkers: false });
  try {
    let plist: FileEntry | undefined,
      entries = 0;
    for await (const entry of zip.getEntriesGenerator()) {
      if (++entries > 25000) throw fail();
      if (!entry.directory && /^Payload\/[^/\\\0]+\.app\/Info\.plist$/.test(entry.filename)) {
        if (plist) throw new Error("The IPA contains more than one main app.");
        plist = entry;
      }
    }
    if (!plist) throw new Error("The IPA has no main app Info.plist.");
    if (plist.encrypted || plist.uncompressedSize > LIMIT || plist.compressedSize > LIMIT * 2) throw fail();
    const chunks: Uint8Array[] = [];
    let total = 0;
    await plist.getData(
      new WritableStream<Uint8Array>({
        write(chunk) {
          total += chunk.length;
          if (total > LIMIT) throw fail();
          chunks.push(chunk);
        },
      }),
      { checkSignature: true, useWebWorkers: false },
    );
    const bytes = new Uint8Array(total);
    let at = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, at);
      at += chunk.length;
    }
    return parseInfoPlist(bytes);
  } finally {
    await zip.close();
  }
}
