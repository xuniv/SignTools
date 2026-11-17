import { RadioGroup, RadioGroupOption } from "./components/ui/radio-group";
import { useEffect, useRef, useState } from "react";
import { Check, ChevronLeft, ChevronRight, Download, Loader2, Search } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { request } from "./lib/api";
import { compactDate, storeTitle, type StoreApp, type StoreVersion } from "./lib/store";
import type { VaultAccount } from "./lib/vault";
const PAGE_SIZE = 20;
const cache = new Map<string, { versions: StoreVersion[]; loaded: Set<string>; time: number }>();
export function VersionsDialog({
  app,
  account,
  owner,
  onClose,
  onSession,
  onDownload,
  busy,
}: {
  app: StoreApp | null;
  account?: VaultAccount;
  owner: string;
  onClose: () => void;
  onSession: (session: string, account: VaultAccount) => Promise<void>;
  onDownload: (app: StoreApp, id: string, version?: StoreVersion) => Promise<void>;
  busy: boolean;
}) {
  const [versions, setVersions] = useState<StoreVersion[]>([]),
    [selected, setSelected] = useState(""),
    [query, setQuery] = useState(""),
    [page, setPage] = useState(0),
    [loading, setLoading] = useState(false),
    [hydrating, setHydrating] = useState(false),
    [error, setError] = useState("");
  const sessionRef = useRef(onSession);
  sessionRef.current = onSession;
  const downloadRef = useRef(onDownload);
  downloadRef.current = onDownload;
  useEffect(() => {
    if (!app || !account) return;
    const controller = new AbortController();
    let alive = true;
    const key = `${account.id}:${app.id}`;
    let session = account.session;
    setQuery("");
    setPage(0);
    setSelected("");
    setError("");
    const saved = cache.get(key);
    const reusable = saved && Date.now() - saved.time < 10 * 60_000 ? saved : undefined;
    setVersions(reusable?.versions || []);
    setLoading(!reusable);
    setHydrating(false);
    const call = async (operation: string, fields: Record<string, unknown>) => {
      const result = await request<{ versions?: StoreVersion[]; session?: string }>(`/api/store/${operation}`, {
        method: "POST",
        signal: controller.signal,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ clientId: owner, session, appId: app.id, bundleId: app.bundleId, ...fields }),
      });
      if (!alive) throw new DOMException("Closed", "AbortError");
      if (result.session) {
        session = result.session;
        await sessionRef.current(session, account);
      }
      return result;
    };
    void (async () => {
      try {
        const all = reusable?.versions || (await call("versions", {})).versions || [];
        if (!alive) return;
        const entry = reusable || { versions: all, loaded: new Set<string>(), time: Date.now() };
        cache.set(key, entry);
        if (cache.size > 32) cache.delete(cache.keys().next().value!);
        setVersions([...entry.versions]);
        setLoading(false);
        const pending = entry.versions.filter((item) => !entry.loaded.has(item.id));
        setHydrating(!!pending.length);
        // Each request contains at most one page. The server bounds its own metadata concurrency.
        // Hydrate the visible page first, then continue in order; closing aborts the current batch.
        for (let offset = 0; offset < pending.length && alive; offset += PAGE_SIZE) {
          const batch = pending.slice(offset, offset + PAGE_SIZE);
          const response = await call("version-metadata-batch", { versionIds: batch.map((item) => item.id) });
          if (!alive) return;
          const resolved = new Map(
            batch.map((item) => [
              item.id,
              (response.versions || []).find((found) => found.id === item.id) || {
                id: item.id,
                error: "No metadata returned.",
              },
            ]),
          );
          entry.versions = entry.versions.map((item) =>
            resolved.has(item.id) ? { ...item, ...resolved.get(item.id) } : item,
          );
          for (const item of batch) entry.loaded.add(item.id);
          entry.time = Date.now();
          setVersions([...entry.versions]);
        }
      } catch (reason) {
        if (alive && !controller.signal.aborted) setError(reason instanceof Error ? reason.message : String(reason));
      } finally {
        if (alive) {
          setLoading(false);
          setHydrating(false);
        }
      }
    })();
    return () => {
      alive = false;
      controller.abort();
    };
  }, [app?.id, account?.id, owner]);
  const filtered = versions.filter((item) =>
    [item.version, item.name, item.id].join(" ").toLowerCase().includes(query.toLowerCase()),
  );
  const pageCount = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE)),
    current = Math.min(page, pageCount - 1),
    visible = filtered.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE),
    details = versions.find((item) => item.id === selected),
    resolved = versions.filter((item) => item.version || item.error).length;
  return (
    <Dialog
      open={!!app}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="version-dialog flex w-[calc(100vw-24px)] max-w-[620px] flex-col">
        <DialogHeader className="text-left">
          <DialogTitle>{app ? storeTitle(app) : "App"} · Versions</DialogTitle>
          <DialogDescription>Choose a release to download. Newest releases appear first.</DialogDescription>
        </DialogHeader>
        <div className="relative">
          <Search size={15} className="absolute left-3 top-3 text-muted-foreground" />
          <Input
            aria-label="Search app versions"
            placeholder="Search version, name, or version ID"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setPage(0);
            }}
            className="pl-9"
          />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
          <span>
            {versions.length} releases{hydrating ? ` · Reading details ${resolved}/${versions.length}` : ""}
          </span>
          {hydrating && <Loader2 size={13} className="animate-spin" />}
        </div>
        {error && (
          <p role="alert" className="text-xs leading-relaxed text-destructive">
            {error}
          </p>
        )}
        <RadioGroup
          value={selected || "__latest"}
          onValueChange={(value) => setSelected(value === "__latest" ? "" : value)}
          aria-label="Available app versions"
          className="version-list gap-0"
        >
          <RadioGroupOption value="__latest">
            <span className="min-w-0">
              <span className="block font-medium">Latest available{app?.version ? ` · ${app.version}` : ""}</span>
              <span className="mt-1 block text-xs text-muted-foreground">
                Use the current release from this account’s storefront.
              </span>
            </span>
            {selected === "" && <Check size={17} className="shrink-0 text-primary" />}
          </RadioGroupOption>
          {loading ? (
            <div className="flex items-center gap-2 px-3 py-8 text-sm text-muted-foreground">
              <Loader2 size={16} className="animate-spin" />
              Loading releases…
            </div>
          ) : (
            visible.map((item) => (
              <RadioGroupOption key={item.id} value={item.id}>
                <span className="min-w-0">
                  <span className="block text-sm font-medium">
                    {item.version
                      ? `Version ${item.version}`
                      : item.error || !hydrating
                        ? "Version details unavailable"
                        : "Reading version details…"}
                  </span>
                  {item.name && item.name !== app?.name && <span className="mt-1 block text-xs">{item.name}</span>}
                  <span className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    {compactDate(item.date) && <time dateTime={item.date}>{compactDate(item.date)}</time>}
                    <span className="break-all font-mono text-[10px]">Version ID {item.id}</span>
                  </span>
                  {item.error && <span className="mt-1 block text-xs text-muted-foreground">{item.error}</span>}
                </span>
                {selected === item.id && <Check size={17} className="shrink-0 text-primary" />}
              </RadioGroupOption>
            ))
          )}
        </RadioGroup>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
          <span className="text-xs text-muted-foreground">
            Page {current + 1} of {pageCount}
            {query && hydrating ? " · More details are still loading" : ""}
          </span>
          <div className="flex gap-1">
            <Button
              variant="ghost"
              size="sm"
              disabled={current === 0}
              aria-label="Previous version page"
              onClick={() => setPage(Math.max(0, current - 1))}
            >
              <ChevronLeft size={15} />
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={current + 1 >= pageCount}
              aria-label="Next version page"
              onClick={() => setPage(current + 1)}
            >
              <ChevronRight size={15} />
            </Button>
          </div>
        </div>
        <Button
          disabled={busy || loading || !app}
          onClick={() => {
            if (app) void downloadRef.current(app, selected, details);
          }}
          className="justify-center gap-2"
        >
          <Download size={15} />
          {selected && details?.version ? `Download ${details.version}` : "Download selected release"}
        </Button>
      </DialogContent>
    </Dialog>
  );
}
