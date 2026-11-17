// Bundle all assets into the Go binary; the application has no runtime CDN dependency.
import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const root = join(import.meta.dir, ".."),
  assets = join(root, "src/assets");
const jsDir = join(assets, "js"),
  vendorDir = join(assets, "vendor");
rmSync(jsDir, { recursive: true, force: true });
mkdirSync(jsDir, { recursive: true });
rmSync(vendorDir, { recursive: true, force: true });
mkdirSync(vendorDir, { recursive: true });
const css = Bun.spawn(
  [
    process.execPath,
    join(root, "node_modules/@tailwindcss/cli/dist/index.mjs"),
    "--input",
    join(root, "src/web/styles.css"),
    "--output",
    join(vendorDir, "app.css"),
    "--minify",
  ],
  { cwd: root, stdout: "inherit", stderr: "inherit" },
);
if ((await css.exited) !== 0) process.exit(1);
const common = { outdir: jsDir, target: "browser", minify: true, naming: "[name].js", metafile: true } as const;
const results = await Promise.all([
  Bun.build({
    ...common,
    entrypoints: [
      join(root, "src/web/index.tsx"),
      ...["rename", "twofa", "install"].map((name) => join(root, `src/web/${name}.ts`)),
    ],
    format: "esm",
  }),
  Bun.build({ ...common, entrypoints: [join(root, "src/web/theme.ts")], format: "iife" }),
]);
const packages = new Set<string>();
for (const result of results) {
  if (!result.success) {
    for (const log of result.logs) console.error(log);
    process.exit(1);
  }
  for (const input of Object.keys(result.metafile?.inputs ?? {})) {
    const pkg = input.match(/node_modules\/((?:@[^/]+\/)?[^/]+)/)?.[1];
    if (pkg) packages.add(pkg);
  }
}
const notices = [...packages].sort().map((pkg) => {
  const dir = join(root, "node_modules", pkg);
  const { version, license } = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
  const file = readdirSync(dir).find((name) => /^licen[cs]e/i.test(name));
  return `${pkg}@${version}\n${file ? readFileSync(join(dir, file), "utf8") : `License: ${license}`}\n`;
});
notices.push(readFileSync(join(root, "src/web/components/ui/LICENSE.md"), "utf8"));
writeFileSync(join(jsDir, "THIRD_PARTY_LICENSES.txt"), notices.join("\n"));
console.log(`Built app.css and web scripts (${packages.size} bundled packages).`);
