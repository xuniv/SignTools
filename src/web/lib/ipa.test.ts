import { expect, test } from "bun:test";
import { zipSync } from "fflate";
import { parseInfoPlist, readIPAMetadata } from "./ipa";
const xml = new Uint8Array(await Bun.file(new URL("./fixtures/Info.xml.plist", import.meta.url)).arrayBuffer());
const binary = new Uint8Array(await Bun.file(new URL("./fixtures/Info.binary.plist", import.meta.url)).arrayBuffer());
const expected = {
  bundleId: "com.example.ipa",
  displayName: "Example & Test 앱",
  version: "2.10.3",
  buildVersion: "420",
};
test("reads real XML and binary Info.plists identically", () => {
  expect(parseInfoPlist(xml)).toEqual(expected);
  expect(parseInfoPlist(binary)).toEqual(expected);
});
test("reads only the main app plist via ZIP slices, never the full IPA", async () => {
  const bytes = zipSync(
    {
      "Payload/Example.app/Info.plist": binary,
      "Payload/Example.app/PlugIns/Widget.appex/Info.plist": xml,
      "Payload/Example.app/large.bin": new Uint8Array(2 << 20),
    },
    { level: 0 },
  );
  class SlicedBlob extends Blob {
    override arrayBuffer(): Promise<ArrayBuffer> {
      throw new Error("The full IPA must not be buffered.");
    }
  }
  expect(await readIPAMetadata(new SlicedBlob([bytes]))).toEqual(expected);
});
test("rejects ambiguous main apps and plist XML entities", async () => {
  const bytes = zipSync({ "Payload/One.app/Info.plist": xml, "Payload/Two.app/Info.plist": binary });
  await expect(readIPAMetadata(new Blob([bytes]))).rejects.toThrow("more than one");
  const unsafe = new TextEncoder().encode(
    '<!DOCTYPE plist [<!ENTITY e "boom">]><plist><dict><key>CFBundleIdentifier</key><string>&e;</string></dict></plist>',
  );
  expect(() => parseInfoPlist(unsafe)).toThrow();
});
test("rejects malformed binary offsets and oversized metadata before extraction", async () => {
  const bad = binary.slice();
  bad.fill(255, bad.length - 24, bad.length - 16);
  expect(() => parseInfoPlist(bad)).toThrow();
  const bytes = zipSync({ "Payload/Example.app/Info.plist": new Uint8Array((1 << 20) + 1) });
  await expect(readIPAMetadata(new Blob([bytes]))).rejects.toThrow();
});
