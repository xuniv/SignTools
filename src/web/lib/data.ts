export interface AppItem {
  Id: string;
  Status: number;
  Name: string;
  GroupId?: string;
  BundleId: string;
  Version: string;
  ModTime: string;
  WorkflowUrl: string;
  InstallUrl: string;
  DownloadSignedUrl: string;
  DownloadUnsignedUrl: string;
  DownloadTweaksUrl: string;
  TwoFactorUrl: string;
  ResignUrl: string;
  DeleteUrl: string;
  RenameUrl: string;
  IconUrl: string;
  ProfileName: string;
  IsMac: boolean;
  TweakCount: number;
  TweakNames: string;
}
export interface Profile {
  Id: string;
  Name: string;
  IsAccount: boolean;
}
export interface Builder {
  Id: string;
  Name: string;
  Type?: string;
  Managed?: boolean;
}
export interface Tweak {
  Id: string;
  Name: string;
  Size: string;
  ModTime: string;
  DownloadUrl: string;
  DeleteUrl: string;
  RenameUrl: string;
}
export interface State {
  Apps: AppItem[];
  Profiles: Profile[];
  Builders: Builder[];
  Tweaks: Tweak[];
}
export interface Repository {
  name: string;
  url: string;
}
export interface Settings {
  builders: Builder[];
  profiles: Profile[];
  defaultBuilder: string;
  defaultProfile: string;
  repositories: Repository[];
  loginPage: boolean;
  pushEnabled: boolean;
}
export interface RepoVersion {
  version: string;
  date?: string;
  downloadURL: string;
  size?: number;
}
export interface RepoApp {
  name: string;
  bundleIdentifier: string;
  developerName?: string;
  localizedDescription?: string;
  iconURL?: string;
  versions: RepoVersion[];
}
export interface RepoData {
  name: string;
  apps: RepoApp[];
}
export interface AppGroup {
  id: string;
  name: string;
  icon: string;
  bundleId: string;
  versions: { version: string; builds: AppItem[] }[];
  apps: AppItem[];
}

// Numeric app versions and SemVer prereleases sort naturally, with stable releases first.
export function compareVersions(a: string, b: string): number {
  const parse = (value: string) => /^(?:v)?(\d+(?:\.\d+)*)(?:-([^+]+))?(?:\+.*)?$/i.exec(value);
  const aa = parse(a),
    bb = parse(b);
  if (!aa || !bb) return a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" });
  const numbersA = aa[1]!.split(".").map(Number),
    numbersB = bb[1]!.split(".").map(Number);
  for (let i = 0; i < Math.max(numbersA.length, numbersB.length); i++) {
    const difference = (numbersA[i] || 0) - (numbersB[i] || 0);
    if (difference) return difference;
  }
  if (!aa[2] && !bb[2]) return 0;
  if (!aa[2]) return 1;
  if (!bb[2]) return -1;
  const preA = aa[2].split("."),
    preB = bb[2].split(".");
  for (let i = 0; i < Math.max(preA.length, preB.length); i++) {
    const x = preA[i],
      y = preB[i];
    if (x === y) continue;
    if (x === undefined) return -1;
    if (y === undefined) return 1;
    const numericA = /^\d+$/.test(x),
      numericB = /^\d+$/.test(y);
    if (numericA && numericB) {
      const difference = Number(x) - Number(y);
      if (difference) return difference;
    } else if (numericA !== numericB) return numericA ? -1 : 1;
    else {
      const difference = x.localeCompare(y, undefined, { numeric: true, sensitivity: "base" });
      if (difference) return difference;
    }
  }
  return 0;
}
export function groupApps(apps: AppItem[]): AppGroup[] {
  const groups = new Map<string, AppItem[]>();
  for (const app of apps) {
    const id = app.GroupId || app.BundleId || app.Name.replace(/\.ipa$/i, "").toLowerCase();
    groups.set(id, [...(groups.get(id) || []), app]);
  }
  return [...groups]
    .map(([id, items]) => {
      items.sort(
        (a, b) =>
          compareVersions(b.Version, a.Version) ||
          Date.parse(b.ModTime) - Date.parse(a.ModTime) ||
          a.Id.localeCompare(b.Id),
      );
      const versions = new Map<string, AppItem[]>();
      for (const item of items) versions.set(item.Version, [...(versions.get(item.Version) || []), item]);
      return {
        id,
        name: items[0]!.Name,
        icon: items[0]!.IconUrl,
        bundleId: items[0]!.GroupId || items[0]!.BundleId,
        versions: [...versions].map(([version, builds]) => ({ version, builds })),
        apps: items,
      };
    })
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" }));
}
export function preferredId(items: { Id: string; Name: string }[], local: string | null, server: string): string {
  return (
    [local, server].map((value) => items.find((item) => item.Id === value || item.Name === value)?.Id).find(Boolean) ||
    items[0]?.Id ||
    ""
  );
}
export function readLocal<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw === null ? fallback : (JSON.parse(raw) as T);
  } catch {
    return fallback;
  }
}
export function writeLocal(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {}
}
export function relativeTime(value: string) {
  const elapsed = Date.now() - Date.parse(value);
  if (!Number.isFinite(elapsed)) return value;
  if (elapsed < 60000) return "Just now";
  if (elapsed < 3600000) return `${Math.floor(elapsed / 60000)}m ago`;
  if (elapsed < 86400000) return `${Math.floor(elapsed / 3600000)}h ago`;
  return new Date(value).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}
