import { useEffect, useState } from "react";
import {
  ArrowDownToLine,
  Check,
  Download,
  Ellipsis,
  ExternalLink,
  FileArchive,
  KeyRound,
  Loader2,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./components/ui/dropdown-menu";
import { request } from "./lib/api";
import { relativeTime, type Artifact, type UploadSource } from "./lib/data";
import { cn } from "./lib/utils";
export function Library({
  onUpload,
  onSign,
  onInstall,
  onError,
}: {
  onUpload: () => void;
  onSign: (source: UploadSource) => void;
  onInstall: (artifact: Artifact) => void;
  onError: (message: string) => void;
}) {
  const [items, setItems] = useState<Artifact[]>([]),
    [loading, setLoading] = useState(true),
    [query, setQuery] = useState(""),
    [busy, setBusy] = useState("");
  async function refresh() {
    const result = await request<{ items: Artifact[] }>("/api/library");
    setItems(result.items || []);
  }
  useEffect(() => {
    void refresh()
      .catch((error) => onError(String(error)))
      .finally(() => setLoading(false));
    const timer = setInterval(() => {
      void refresh().catch(() => {});
    }, 15000);
    return () => clearInterval(timer);
  }, []);
  const filtered = items.filter((item) =>
    [item.name, item.displayName, item.bundleId, item.version, item.buildVersion, item.encryption]
      .join(" ")
      .toLowerCase()
      .includes(query.toLowerCase()),
  );
  async function remove(item: Artifact) {
    if (!confirm(`Delete ${item.displayName || item.name} from the IPA library?`)) return;
    setBusy(item.id);
    try {
      await request(`/api/library/${encodeURIComponent(item.id)}`, { method: "DELETE" });
      await refresh();
    } catch (error) {
      onError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy("");
    }
  }
  return (
    <div>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="relative w-full sm:w-80">
          <Search size={14} className="absolute left-3 top-3 text-muted-foreground" />
          <Input
            className="h-9 bg-card pl-9 text-xs"
            placeholder="Search apps, versions, or DRM state…"
            aria-label="Search IPA library"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">
            {items.length} IPA{items.length === 1 ? "" : "s"}
          </span>
          <Button
            size="icon"
            variant="outline"
            className="h-9 w-9"
            aria-label="Refresh IPA library"
            onClick={() => void refresh().catch((error) => onError(String(error)))}
          >
            <RefreshCw size={14} />
          </Button>
        </div>
      </div>
      <div className="mb-5 rounded-lg border bg-card px-4 py-3 text-xs leading-relaxed text-muted-foreground">
        Install only hosts the original IPA without signing or changing its entitlements. Encrypted App Store apps
        still require the purchasing account and matching Apple authorization. Use Decrypt with a matching model when
        available.
      </div>
      {loading ? (
        <div className="flex h-60 items-center justify-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="animate-spin" size={16} />
          Loading IPA library…
        </div>
      ) : !filtered.length ? (
        <div className="surface grid justify-items-center gap-3 px-5 py-14 text-center">
          <FileArchive size={30} className="text-muted-foreground" />
          <h2 className="font-semibold">{items.length ? "No matching IPA files" : "Keep your original IPAs ready"}</h2>
          <p className="max-w-md text-xs leading-relaxed text-muted-foreground">
            Upload without signing, or download from Store. Every artifact keeps its source, version, and DRM state.
          </p>
          <Button variant="outline" onClick={onUpload}>
            <Plus size={15} className="mr-2" />
            Upload IPA
          </Button>
        </div>
      ) : (
        <div className="grid gap-3 xl:grid-cols-2">
          {filtered.map((item) => (
            <article className="surface p-4" key={item.id}>
              <div className="flex items-start gap-3">
                <div className="rounded-xl bg-secondary p-3 text-primary">
                  <FileArchive size={20} />
                </div>
                <div className="min-w-0 flex-1">
                  <h2 className="truncate text-sm font-semibold">{item.displayName || item.name}</h2>
                  <p className="mt-1 break-all font-mono text-[10px] text-muted-foreground">
                    {item.bundleId || "Unknown bundle identifier"}
                  </p>
                  <p className="mt-2 text-xs text-muted-foreground">
                    v{item.version || "?"}
                    {item.buildVersion ? ` (${item.buildVersion})` : ""}
                    <span className="mx-1.5">·</span>
                    {item.source}
                    <span className="mx-1.5">·</span>
                    {relativeTime(item.createdAt)}
                  </p>
                </div>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7"
                      aria-label={`Actions for ${item.displayName || item.name}`}
                    >
                      <Ellipsis size={16} />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem asChild>
                      <a href={item.downloadUrl}>
                        <Download size={14} className="mr-2" />
                        Download IPA
                      </a>
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      onSelect={() =>
                        onSign({ libraryId: item.id, name: item.displayName || item.name, metadata: item })
                      }
                    >
                      <ShieldCheck size={14} className="mr-2" />
                      Sign a copy
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      className="text-destructive"
                      onSelect={() => void remove(item)}
                      disabled={busy === item.id}
                    >
                      <Trash2 size={14} className="mr-2" />
                      Delete artifact
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </div>
              <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t pt-3">
                <span
                  className={cn(
                    "status",
                    item.encryption === "encrypted"
                      ? "status-working"
                      : item.encryption === "decrypted" || item.encryption === "unencrypted"
                        ? "status-ready"
                        : "bg-secondary text-muted-foreground",
                  )}
                >
                  {item.encryption === "encrypted"
                    ? "DRM encrypted"
                    : item.encryption === "decrypted"
                      ? "Decrypted"
                      : item.encryption === "unencrypted"
                        ? "No DRM detected"
                        : "DRM unknown"}
                </span>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-8 gap-1.5 text-xs"
                    onClick={() => onSign({ libraryId: item.id, name: item.displayName || item.name, metadata: item })}
                  >
                    <ShieldCheck size={12} />
                    Sign
                  </Button>
                  <Button size="sm" className="h-8 gap-1.5 text-xs" onClick={() => onInstall(item)}>
                    <ArrowDownToLine size={12} />
                    {item.installUrl ? "Install page" : "Create install page"}
                  </Button>
                </div>
              </div>
              {item.installUrl && (
                <a
                  className="mt-3 flex items-center gap-1.5 truncate text-[10px] text-primary"
                  href={item.installUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <ExternalLink size={11} />
                  {new URL(item.installUrl, location.origin).href}
                </a>
              )}
              {item.limitations?.length && (
                <p className="mt-2 text-[10px] leading-relaxed text-muted-foreground">{item.limitations.join(" ")}</p>
              )}
            </article>
          ))}
        </div>
      )}
    </div>
  );
}
