import React, { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  ArrowDownToLine,
  ArrowLeft,
  ArrowRight,
  Bell,
  BellOff,
  Check,
  CheckCheck,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleHelp,
  CloudDownload,
  Code2,
  Copy,
  Download,
  Ellipsis,
  ExternalLink,
  FileArchive,
  FolderOpen,
  Github,
  Globe2,
  HardDrive,
  KeyRound,
  Layers3,
  LayoutGrid,
  Loader2,
  Monitor,
  Moon,
  Package,
  PanelLeftClose,
  Plus,
  Puzzle,
  RefreshCw,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Smartphone,
  Sparkles,
  Sun,
  Terminal,
  Trash2,
  Upload,
  X,
  XCircle,
} from "lucide-react";
import Uppy from "@uppy/core";
import Tus from "@uppy/tus";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Badge } from "./components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./components/ui/dropdown-menu";
import { cn } from "./lib/utils";
import {
  groupApps,
  preferredId,
  readLocal,
  relativeTime,
  writeLocal,
  compareVersions,
  type AppItem,
  type AppGroup,
  type Builder,
  type Profile,
  type State,
  type Settings,
  type Tweak,
  type Repository,
  type RepoData,
  type RepoApp,
} from "./lib/data";
import { postJSON, request } from "./lib/api";
import { disablePush, enablePush, isInstalled, registerWorker, syncPush } from "./lib/push";

type Page = "apps" | "sources" | "tweaks" | "settings";
type UploadSource = { id?: string; name?: string; url?: string };
type Modal =
  | { type: "upload"; source?: UploadSource }
  | { type: "tweak" }
  | { type: "profile" }
  | { type: "builder" }
  | { type: "source" }
  | { type: "frame"; title: string; url: string }
  | null;
const emptyState: State = { Apps: [], Builders: [], Profiles: [], Tweaks: [] };
const emptySettings: Settings = {
  builders: [],
  profiles: [],
  repositories: [],
  defaultBuilder: "",
  defaultProfile: "",
  loginPage: false,
  pushEnabled: false,
};
const pages = [
  { id: "apps", label: "App library", icon: LayoutGrid },
  { id: "sources", label: "Sources", icon: Globe2 },
  { id: "tweaks", label: "Tweaks", icon: Puzzle },
  { id: "settings", label: "Settings", icon: Settings2 },
] as const;
const statusName = ["Signing", "Ready", "Failed", "Queued"];
const allAppIds = (state: State) => state.Apps.map((app) => app.Id);
function errorText(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
function initialPage(): Page {
  const hash = location.hash.slice(1);
  return pages.some((page) => page.id === hash) ? (hash as Page) : "apps";
}
function AppIcon({ name, src, small = false }: { name: string; src?: string; small?: boolean }) {
  const [broken, setBroken] = useState(false);
  useEffect(() => setBroken(false), [src]);
  return (
    <div
      className={cn(
        "relative flex shrink-0 items-center justify-center overflow-hidden rounded-[23%] bg-secondary text-xl font-semibold text-primary ring-1 ring-black/5",
        small ? "h-10 w-10" : "h-14 w-14",
      )}
    >
      {name.trim().slice(0, 1).toUpperCase() || <Package size={24} />}{" "}
      {src && !broken && (
        <img
          src={src}
          alt=""
          className="absolute inset-0 h-full w-full object-cover"
          onError={() => setBroken(true)}
          loading="lazy"
          referrerPolicy="no-referrer"
        />
      )}
    </div>
  );
}
function Status({ value }: { value: number }) {
  return (
    <span className={cn("status", value === 1 ? "status-ready" : value === 2 ? "status-failed" : "status-working")}>
      {statusName[value] || "Unknown"}
    </span>
  );
}
function Empty({
  icon: Icon = FolderOpen,
  title,
  description,
  action,
}: {
  icon?: typeof FolderOpen;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="surface grid min-h-80 place-content-center justify-items-center gap-3 px-6 py-16 text-center">
      <div className="mb-2 rounded-2xl border bg-background p-4 text-muted-foreground">
        <Icon size={26} strokeWidth={1.4} />
      </div>
      <h3 className="text-base font-semibold tracking-tight">{title}</h3>
      <p className="max-w-80 text-sm leading-relaxed text-muted-foreground">{description}</p>
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  const id = React.useId();
  let associated = false;
  const content = React.Children.map(children, (child) => {
    if (!associated && React.isValidElement<{ id?: string; "aria-label"?: string }>(child)) {
      associated = true;
      return React.cloneElement(child, { id, "aria-label": child.props["aria-label"] || label });
    }
    return child;
  });
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {content}
      {hint && <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p>}
    </div>
  );
}
function Select({ children, ...props }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className="select" {...props}>
      {children}
    </select>
  );
}
function Spinner() {
  return <Loader2 size={16} className="animate-spin" />;
}
function App() {
  const [state, setState] = useState<State>(emptyState),
    [settings, setSettings] = useState<Settings>(emptySettings),
    [page, setPage] = useState<Page>(initialPage),
    [modal, setModal] = useState<Modal>(null),
    [loading, setLoading] = useState(true),
    [online, setOnline] = useState(false),
    [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all"),
    [notice, setNotice] = useState<{ text: string; error?: boolean } | null>(null),
    [theme, setTheme] = useState(window.theme?.current() ?? "light");
  const [prefBuilder, setPrefBuilder] = useState(() => readLocal<string | null>("signtools.builder", null)),
    [prefProfile, setPrefProfile] = useState(() => readLocal<string | null>("signtools.profile", null));
  const [localRepos, setLocalRepos] = useState<Repository[]>(() => {
      const saved = readLocal<Repository[]>("signtools.sources", []);
      return Array.isArray(saved)
        ? saved.filter((repo) => repo && typeof repo.name === "string" && typeof repo.url === "string")
        : [];
    }),
    [hiddenRepos, setHiddenRepos] = useState<string[]>(() => {
      const saved = readLocal<string[]>("signtools.hiddenSources", []);
      return Array.isArray(saved) ? saved.filter((url) => typeof url === "string") : [];
    });
  const [pushOn, setPushOn] = useState(false),
    [pushBusy, setPushBusy] = useState(false),
    [installed, setInstalled] = useState(isInstalled);
  const toast = useCallback((text: string, error = false) => setNotice({ text, error }), []);
  const updateState = useCallback((next: State) => {
    const clean = {
      ...next,
      Apps: next.Apps || [],
      Tweaks: next.Tweaks || [],
      Builders: next.Builders || [],
      Profiles: next.Profiles || [],
    };
    setState(clean);
    setOnline(true);
    void syncPush(allAppIds(clean)).catch(() => {});
  }, []);
  const refresh = useCallback(async () => {
    const next = await request<State>("/api/state");
    updateState(next);
  }, [updateState]);
  const refreshAll = useCallback(async () => {
    const [next, options] = await Promise.all([request<State>("/api/state"), request<Settings>("/api/settings")]);
    updateState(next);
    setSettings({
      ...emptySettings,
      ...options,
      repositories: options.repositories || [],
      builders: options.builders || [],
      profiles: options.profiles || [],
    });
  }, [updateState]);
  useEffect(() => {
    void refreshAll()
      .catch((error) => toast(errorText(error), true))
      .finally(() => setLoading(false));
    const events = new EventSource("/api/events");
    events.addEventListener("state", (event) => {
      try {
        updateState(JSON.parse((event as MessageEvent).data));
      } catch {}
    });
    events.onopen = () => setOnline(true);
    events.onerror = () => setOnline(false);
    const poll = setInterval(() => {
      if (document.visibilityState === "visible") void refresh().catch(() => setOnline(false));
    }, 15000);
    const focus = () => void refreshAll().catch(() => setOnline(false));
    window.addEventListener("focus", focus);
    return () => {
      events.close();
      clearInterval(poll);
      window.removeEventListener("focus", focus);
    };
  }, [refreshAll, refresh, toast, updateState]);
  useEffect(() => {
    const change = () => {
      setPage(initialPage());
      setSearch("");
    };
    window.addEventListener("hashchange", change);
    return () => window.removeEventListener("hashchange", change);
  }, []);
  useEffect(() => {
    const change = () => setTheme(window.theme.current());
    document.addEventListener("themechange", change);
    return () => document.removeEventListener("themechange", change);
  }, []);
  useEffect(() => {
    void registerWorker()
      .then(async (registration) => setPushOn(!!(await registration?.pushManager?.getSubscription())))
      .catch(() => {});
    const media = matchMedia("(display-mode: standalone)");
    const change = () => setInstalled(isInstalled());
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(() => setNotice(null), notice.error ? 12000 : 5000);
    return () => clearTimeout(timer);
  }, [notice]);
  const builders = settings.builders.length ? settings.builders : state.Builders,
    profiles = settings.profiles.length ? settings.profiles : state.Profiles;
  const builder = preferredId(builders, prefBuilder, settings.defaultBuilder),
    profile = preferredId(profiles, prefProfile, settings.defaultProfile);
  const setBuilder = (id: string) => {
      setPrefBuilder(id);
      writeLocal("signtools.builder", id);
    },
    setProfile = (id: string) => {
      setPrefProfile(id);
      writeLocal("signtools.profile", id);
    };
  const repos = useMemo(
    () => [
      ...new Map(
        [...settings.repositories, ...localRepos]
          .filter((repo) => !hiddenRepos.includes(repo.url))
          .map((repo) => [repo.url, repo]),
      ).values(),
    ],
    [settings.repositories, localRepos, hiddenRepos],
  );
  const navigate = (next: Page) => {
    setPage(next);
    setSearch("");
    location.hash = next;
  };
  const onAction = async (url: string, message?: string) => {
    try {
      await request(url);
      await refresh();
      if (message) toast(message);
    } catch (error) {
      toast(errorText(error), true);
    }
  };
  const onSaved = async (message: string) => {
    setModal(null);
    await refreshAll();
    toast(message);
  };
  const signing = state.Apps.filter((app) => app.Status === 0 || app.Status === 3).length,
    ready = state.Apps.filter((app) => app.Status === 1).length;
  const matching = state.Apps.filter(
    (app) =>
      (filter === "all" ||
        (filter === "ready"
          ? app.Status === 1
          : filter === "signing"
            ? app.Status === 0 || app.Status === 3
            : app.Status === 2)) &&
      [app.Name, app.BundleId, app.GroupId, app.Version, app.ProfileName]
        .join(" ")
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  const groups = groupApps(matching),
    totalGroups = groupApps(state.Apps).length;
  async function togglePush() {
    setPushBusy(true);
    try {
      if (pushOn) {
        await disablePush();
        setPushOn(false);
        toast("Notifications turned off for this device.");
      } else {
        await enablePush(allAppIds(state));
        setPushOn(true);
        toast("You’ll receive a link when signing finishes.");
      }
    } catch (error) {
      toast(errorText(error), true);
    } finally {
      setPushBusy(false);
    }
  }
  const Nav = ({ mobile = false }: { mobile?: boolean }) => (
    <>
      {pages.map((item) => {
        const Icon = item.icon;
        return (
          <button
            key={item.id}
            onClick={() => navigate(item.id)}
            data-active={page === item.id}
            className={cn("nav-link", mobile && "flex-1 flex-col gap-1 rounded-none px-1 py-3 text-[10px]")}
          >
            <Icon size={mobile ? 20 : 17} strokeWidth={1.7} />
            <span>{mobile && item.id === "apps" ? "Library" : item.label}</span>
            {!mobile && item.id === "apps" && (
              <span className="ml-auto rounded bg-card/80 px-1.5 py-0.5 text-[10px] font-medium tabular-nums">
                {totalGroups}
              </span>
            )}
            {!mobile && item.id === "sources" && repos.length > 0 && (
              <span className="ml-auto text-[10px] tabular-nums">{repos.length}</span>
            )}
          </button>
        );
      })}
    </>
  );
  return (
    <div className="min-h-dvh">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-[224px] flex-col border-r bg-card p-4 md:flex">
        <a href="/#apps" className="mb-10 mt-4 flex items-center gap-2.5 px-3 text-lg font-semibold tracking-[-.04em]">
          <span className="flex h-8 w-8 items-center justify-center rounded-[10px] bg-primary text-white">
            <Layers3 size={19} />
          </span>
          SignTools
        </a>
        <p className="eyebrow mb-3 px-3">Manage</p>
        <nav className="grid gap-1">
          <Nav />
        </nav>
        <div className="mt-8 px-3">
          <p className="eyebrow mb-4">Signing activity</p>
          <div className="grid gap-3 text-xs text-muted-foreground">
            <span className="flex items-center gap-2.5">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
              {ready} ready to install
            </span>
            <span className="flex items-center gap-2.5">
              <span className={cn("h-1.5 w-1.5 rounded-full", signing ? "bg-amber-400" : "bg-muted-foreground/40")} />
              {signing} in progress
            </span>
          </div>
        </div>
        <div className="mt-auto space-y-4">
          <div className="rounded-xl border bg-background p-3.5">
            <Smartphone size={18} className="mb-2 text-primary" />
            <p className="text-xs font-medium">Your library. Anywhere.</p>
            <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">
              Add to Home Screen for the full app experience.
            </p>
          </div>
          <div className="flex items-center gap-2 border-t px-2 pt-4">
            <div className="flex h-8 w-8 items-center justify-center rounded-full bg-secondary text-primary">
              <ShieldCheck size={16} />
            </div>
            <div>
              <p className="text-xs font-medium">Private workspace</p>
              <p className="mt-0.5 text-[10px] text-muted-foreground">Self-hosted · Your control</p>
            </div>
            <button
              className="ml-auto p-1 text-muted-foreground"
              title="Toggle theme"
              aria-label="Toggle color theme"
              onClick={() => window.theme.toggle()}
            >
              {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
            </button>
          </div>
        </div>
      </aside>
      <main className="app-main md:ml-[224px]">
        <header className="flex h-16 items-center justify-between border-b bg-card px-5 pt-[env(safe-area-inset-top)] sm:px-8">
          <div className="flex items-center gap-2 text-xs">
            <span className="font-semibold md:hidden">SignTools</span>
            <span className="hidden text-muted-foreground md:inline">Workspace</span>
            <ChevronRight size={12} className="text-muted-foreground" />
            <span>{pages.find((item) => item.id === page)?.label}</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <span className={cn("h-1.5 w-1.5 rounded-full", online ? "bg-emerald-500" : "bg-amber-400")} />
              {online ? "Connected" : "Reconnecting"}
            </span>
            <span className="h-4 w-px bg-border" />
            <button
              className="text-muted-foreground md:hidden"
              aria-label="Toggle color theme"
              onClick={() => window.theme.toggle()}
            >
              {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
            </button>
            {installed && settings.pushEnabled && (
              <button
                title={pushOn ? "Turn off notifications" : "Enable signing notifications"}
                aria-label={pushOn ? "Turn off notifications" : "Enable signing notifications"}
                onClick={() => void togglePush()}
                disabled={pushBusy}
                className={cn("p-1", pushOn ? "text-primary" : "text-muted-foreground")}
              >
                {pushBusy ? <Spinner /> : pushOn ? <Bell size={17} /> : <BellOff size={17} />}
              </button>
            )}
            <span className="flex h-7 w-7 items-center justify-center rounded-full bg-emerald-100 text-[10px] font-semibold text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
              ST
            </span>
          </div>
        </header>
        <div className="mx-auto max-w-[1500px] px-5 py-8 sm:px-8 sm:py-10 lg:px-12">
          <div className="mb-8 flex flex-wrap items-start justify-between gap-4">
            <div>
              <p className="eyebrow mb-2.5">
                {page === "apps"
                  ? "Everything you sign"
                  : page === "sources"
                    ? "Discover & import"
                    : page === "tweaks"
                      ? "Make it yours"
                      : "Your workspace"}
              </p>
              <h1 className="page-title">
                {page === "apps"
                  ? "App library"
                  : page === "sources"
                    ? "Sources"
                    : page === "tweaks"
                      ? "Tweak library"
                      : "Settings"}
              </h1>
              <p className="mt-2 text-[13px] leading-relaxed text-muted-foreground">
                {page === "apps"
                  ? "All your apps and versions, thoughtfully organized."
                  : page === "sources"
                    ? "Browse your favorite repositories and bring apps into your library."
                    : page === "tweaks"
                      ? "Keep your tweaks together. Inject them when you sign."
                      : "Make signing feel effortless, on every device."}
              </p>
            </div>
            {page === "apps" ? (
              <Button onClick={() => setModal({ type: "upload" })} className="mt-1 gap-2 shadow-sm">
                <Plus size={16} />
                Sign an app
              </Button>
            ) : page === "sources" ? (
              <Button onClick={() => setModal({ type: "source" })} className="gap-2">
                <Plus size={16} />
                Add source
              </Button>
            ) : page === "tweaks" ? (
              <Button onClick={() => setModal({ type: "tweak" })} className="gap-2">
                <Plus size={16} />
                Add tweaks
              </Button>
            ) : null}
          </div>
          {page === "apps" && (
            <>
              <div className="mb-7 grid grid-cols-3 gap-3 sm:gap-4">
                {[
                  {
                    label: "Apps in library",
                    value: totalGroups,
                    icon: Layers3,
                    detail: `${state.Apps.length} total builds`,
                  },
                  { label: "Ready to install", value: ready, icon: CheckCircle2, detail: "Signed and waiting" },
                  { label: "In progress", value: signing, icon: Activity, detail: "Signing across builders" },
                ].map(({ label, value, icon: Icon, detail }) => (
                  <div key={label} className="surface px-3 py-4 sm:px-5">
                    <div className="mb-3 flex items-center justify-between gap-2">
                      <span className="text-[10px] font-medium text-muted-foreground sm:text-xs">{label}</span>
                      <Icon size={15} strokeWidth={1.5} className="hidden text-muted-foreground sm:block" />
                    </div>
                    <span className="text-2xl font-semibold tracking-[-.05em] sm:text-[30px]">
                      {loading ? "—" : value}
                    </span>
                    <p className="mt-1.5 hidden text-[10px] text-muted-foreground sm:block">{detail}</p>
                  </div>
                ))}
              </div>
              <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
                <div className="flex max-w-full gap-0.5 overflow-auto rounded-lg border bg-card p-1">
                  {[
                    { id: "all", name: "All apps" },
                    { id: "ready", name: "Ready" },
                    { id: "signing", name: "In progress" },
                    { id: "failed", name: "Failed" },
                  ].map((item) => (
                    <button
                      key={item.id}
                      onClick={() => setFilter(item.id)}
                      className={cn(
                        "whitespace-nowrap rounded-md px-3 py-1.5 text-[11px] transition-colors",
                        filter === item.id
                          ? "bg-secondary font-semibold text-foreground"
                          : "text-muted-foreground hover:text-foreground",
                      )}
                    >
                      {item.name}
                    </button>
                  ))}
                </div>
                <div className="relative w-full sm:w-64">
                  <Search size={14} className="absolute left-3 top-3 text-muted-foreground" />
                  <Input
                    aria-label="Search apps, versions, profiles"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder="Search your library..."
                    className="h-9 bg-card pl-9 text-xs"
                  />
                </div>
              </div>
              {loading ? (
                <div className="grid gap-4 lg:grid-cols-2">
                  {[0, 1, 2, 3].map((item) => (
                    <div key={item} className="surface h-52 animate-pulse bg-secondary/50" />
                  ))}
                </div>
              ) : groups.length ? (
                <div className="grid items-start gap-4 xl:grid-cols-2">
                  {groups.map((group) => (
                    <AppCard
                      key={group.id}
                      group={group}
                      onInstall={(app) =>
                        setModal({ type: "frame", title: `Install ${app.Name}`, url: app.InstallUrl })
                      }
                      onFrame={(title, url) => setModal({ type: "frame", title, url })}
                      onCreate={(app) => setModal({ type: "upload", source: { id: app.Id, name: app.Name } })}
                      onAction={onAction}
                      toast={toast}
                    />
                  ))}
                </div>
              ) : (
                <Empty
                  title={state.Apps.length ? "No matching apps" : "A fresh start for your apps"}
                  description={
                    state.Apps.length
                      ? "Try another name, bundle identifier, version, or status."
                      : "Upload an IPA or browse a source to sign your first app."
                  }
                  action={
                    <Button variant="outline" onClick={() => setModal({ type: "upload" })}>
                      <Plus size={15} className="mr-2" />
                      Sign an app
                    </Button>
                  }
                />
              )}
              <p className="mt-6 flex items-center justify-center gap-1.5 text-[10px] text-muted-foreground">
                <Layers3 size={12} />
                Grouped by app identity · Versions newest first
              </p>
            </>
          )}
          {page === "sources" && (
            <Sources
              repos={repos}
              query={search}
              setQuery={setSearch}
              add={() => setModal({ type: "source" })}
              remove={(repo) => {
                if (settings.repositories.some((item) => item.url === repo.url)) {
                  const next = [...hiddenRepos, repo.url];
                  setHiddenRepos(next);
                  writeLocal("signtools.hiddenSources", next);
                }
                const next = localRepos.filter((item) => item.url !== repo.url);
                setLocalRepos(next);
                writeLocal("signtools.sources", next);
                toast("Source removed from this browser.");
              }}
              onSign={(app, version) =>
                setModal({
                  type: "upload",
                  source: { name: `${app.name} ${version.version}`, url: version.downloadURL },
                })
              }
            />
          )}
          {page === "tweaks" && (
            <>
              <div className="relative mb-5 max-w-sm">
                <Search size={14} className="absolute left-3 top-3 text-muted-foreground" />
                <Input
                  aria-label="Search tweaks"
                  placeholder="Search tweaks..."
                  className="h-9 bg-card pl-9 text-xs"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                />
              </div>
              {state.Tweaks.length ? (
                <div className="grid gap-3 lg:grid-cols-2">
                  {state.Tweaks.filter((tweak) => tweak.Name.toLowerCase().includes(search.toLowerCase())).map(
                    (tweak) => (
                      <div className="surface flex items-center gap-4 p-5" key={tweak.Id}>
                        <div className="rounded-xl bg-secondary p-3 text-primary">
                          <Puzzle size={20} />
                        </div>
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-sm font-medium">{tweak.Name}</p>
                          <p className="mt-1 text-xs text-muted-foreground">
                            {tweak.Size} <span className="mx-1">·</span> {relativeTime(tweak.ModTime)}
                          </p>
                        </div>
                        <a href={tweak.DownloadUrl} className="subtle-button" aria-label={`Download ${tweak.Name}`}>
                          <Download size={16} />
                        </a>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon" aria-label={`Actions for ${tweak.Name}`}>
                              <Ellipsis size={16} />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onSelect={() => setModal({ type: "frame", title: "Rename tweak", url: tweak.RenameUrl })}
                            >
                              Rename
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              className="text-destructive"
                              onSelect={() => {
                                if (confirm(`Delete ${tweak.Name}?`)) void onAction(tweak.DeleteUrl, "Tweak deleted.");
                              }}
                            >
                              Delete
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                    ),
                  )}
                </div>
              ) : (
                <Empty
                  icon={Puzzle}
                  title="A little extra for your apps"
                  description="Upload your tweak files once, then choose them from the library each time you sign."
                  action={
                    <Button variant="outline" onClick={() => setModal({ type: "tweak" })}>
                      <Plus size={15} className="mr-2" />
                      Add tweaks
                    </Button>
                  }
                />
              )}
            </>
          )}
          {page === "settings" && (
            <div className="grid items-start gap-6 lg:grid-cols-2">
              <section className="surface p-6">
                <div className="mb-5 flex items-center gap-2">
                  <Sparkles size={17} className="text-primary" />
                  <h2 className="font-semibold">Signing preferences</h2>
                </div>
                <p className="mb-5 text-xs leading-relaxed text-muted-foreground">
                  Your choices are saved on this device and selected for your next signing job.
                </p>
                <div className="grid gap-5">
                  <Field label="Preferred builder">
                    <Select
                      aria-label="Preferred builder"
                      value={builder}
                      onChange={(event) => setBuilder(event.target.value)}
                    >
                      {builders.map((item) => (
                        <option value={item.Id} key={item.Id}>
                          {item.Name}
                        </option>
                      ))}
                    </Select>
                  </Field>
                  <Field label="Preferred signing profile">
                    <Select
                      aria-label="Preferred signing profile"
                      value={profile}
                      onChange={(event) => setProfile(event.target.value)}
                    >
                      {!profiles.length && <option value="">No profiles available</option>}
                      {profiles.map((item) => (
                        <option value={item.Id} key={item.Id}>
                          {item.Name}
                        </option>
                      ))}
                    </Select>
                  </Field>
                </div>
              </section>
              <section className="surface p-6">
                <div className="mb-5 flex items-center gap-2">
                  <Smartphone size={17} className="text-primary" />
                  <h2 className="font-semibold">App experience</h2>
                </div>
                <div className="mb-5 flex items-center justify-between gap-3">
                  <div>
                    <p className="text-xs font-medium">Appearance</p>
                    <p className="mt-1 text-xs text-muted-foreground">Follows your device until changed.</p>
                  </div>
                  <Button variant="outline" size="sm" onClick={() => window.theme.toggle()}>
                    {theme === "dark" ? <Moon size={14} className="mr-2" /> : <Sun size={14} className="mr-2" />}
                    {theme === "dark" ? "Dark" : "Light"}
                  </Button>
                </div>
                <div className="border-t pt-5">
                  <p className="text-xs font-medium">Signing notifications</p>
                  <p className="mb-3 mt-1 text-xs leading-relaxed text-muted-foreground">
                    {!installed
                      ? "In Safari, tap Share → Add to Home Screen, then open SignTools from your Home Screen to enable notifications."
                      : !settings.pushEnabled
                        ? "Your server administrator can enable Web Push in extra_conf."
                        : "Get an installation link when an app finishes signing, even after you close SignTools."}
                  </p>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => void togglePush()}
                    disabled={!installed || !settings.pushEnabled || pushBusy}
                  >
                    {pushBusy ? (
                      <Spinner />
                    ) : pushOn ? (
                      <BellOff size={14} className="mr-2" />
                    ) : (
                      <Bell size={14} className="mr-2" />
                    )}
                    {pushOn ? "Turn off notifications" : "Enable notifications"}
                  </Button>
                </div>
              </section>
              <section className="surface p-6">
                <div className="mb-5 flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Server size={17} className="text-primary" />
                    <h2 className="font-semibold">Builders</h2>
                    <Badge variant="secondary" className="text-[10px]">
                      {builders.length}
                    </Badge>
                  </div>
                  <Button variant="outline" size="sm" onClick={() => setModal({ type: "builder" })}>
                    <Plus size={14} className="mr-1.5" />
                    Add
                  </Button>
                </div>
                <div className="divide-y">
                  {builders.map((item) => (
                    <div className="flex items-center gap-3 py-3" key={item.Id}>
                      <div className="rounded-lg bg-secondary p-2 text-muted-foreground">
                        {item.Type === "github" || item.Id === "GitHub" ? <Github size={15} /> : <Server size={15} />}
                      </div>
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-xs font-medium">{item.Name}</p>
                        <p className="mt-1 text-[10px] text-muted-foreground">{item.Type || item.Id}</p>
                      </div>
                      {item.Id === builder && <Check size={13} className="text-primary" />}
                      {item.Managed && (
                        <button
                          className="subtle-button"
                          aria-label={`Remove builder ${item.Name}`}
                          onClick={() => {
                            if (confirm(`Remove builder ${item.Name}?`))
                              void request(`/api/builders/${encodeURIComponent(item.Id)}`, { method: "DELETE" })
                                .then(() => onSaved("Builder removed."))
                                .catch((error) => toast(errorText(error), true));
                          }}
                        >
                          <Trash2 size={14} />
                        </button>
                      )}
                    </div>
                  ))}
                </div>
                <p className="mt-4 border-t pt-4 text-[10px] leading-relaxed text-muted-foreground">
                  Tokens and keys stay on the server. Saved builders are available to this workspace.
                </p>
              </section>
              <section className="surface p-6">
                <div className="mb-5 flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <KeyRound size={17} className="text-primary" />
                    <h2 className="font-semibold">Signing profiles</h2>
                    <Badge variant="secondary" className="text-[10px]">
                      {profiles.length}
                    </Badge>
                  </div>
                  <Button variant="outline" size="sm" onClick={() => setModal({ type: "profile" })}>
                    <Plus size={14} className="mr-1.5" />
                    Add
                  </Button>
                </div>
                {profiles.length ? (
                  <div className="divide-y">
                    {profiles.map((item) => (
                      <div className="flex items-center gap-3 py-3" key={item.Id}>
                        <div className="rounded-lg bg-secondary p-2 text-muted-foreground">
                          <ShieldCheck size={15} />
                        </div>
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-xs font-medium">{item.Name}</p>
                          <p className="mt-1 text-[10px] text-muted-foreground">
                            {item.IsAccount ? "Apple account" : "Provisioning profile"}
                          </p>
                        </div>
                        {item.Id === profile && <Check size={13} className="text-primary" />}
                        <button
                          className="subtle-button"
                          aria-label={`Remove profile ${item.Name}`}
                          onClick={() => {
                            if (confirm(`Delete signing profile ${item.Name}?`))
                              void request(`/api/profiles/${encodeURIComponent(item.Id)}`, { method: "DELETE" })
                                .then(() => onSaved("Profile removed."))
                                .catch((error) => toast(errorText(error), true));
                          }}
                        >
                          <Trash2 size={14} />
                        </button>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="py-5 text-xs text-muted-foreground">
                    Add a .p12 certificate and provisioning profile to start signing.
                  </p>
                )}
                <p className="mt-4 border-t pt-4 text-[10px] leading-relaxed text-muted-foreground">
                  Certificates and passwords are sent directly to your server and never saved in browser storage.
                </p>
              </section>
              {hiddenRepos.length > 0 && (
                <section className="surface p-6">
                  <h2 className="mb-2 font-semibold">Hidden server sources</h2>
                  <p className="mb-4 text-xs text-muted-foreground">
                    {hiddenRepos.length} default source(s) hidden on this device.
                  </p>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setHiddenRepos([]);
                      writeLocal("signtools.hiddenSources", []);
                      toast("Server sources restored.");
                    }}
                  >
                    Restore sources
                  </Button>
                </section>
              )}
              {settings.loginPage && (
                <section className="surface p-6">
                  <h2 className="mb-2 font-semibold">Workspace session</h2>
                  <p className="mb-4 text-xs text-muted-foreground">You are signed in to this private workspace.</p>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      void fetch("/logout", { method: "POST", credentials: "same-origin" })
                        .then((response) => {
                          if (!response.ok) throw new Error("Could not sign out.");
                          location.assign("/login");
                        })
                        .catch((error) => toast(errorText(error), true))
                    }
                  >
                    Sign out
                  </Button>
                </section>
              )}
            </div>
          )}
        </div>
      </main>
      <nav className="mobile-nav fixed inset-x-0 bottom-0 z-40 flex border-t bg-card/95 backdrop-blur-lg md:hidden">
        <Nav mobile />
      </nav>
      <Dialog
        open={!!modal}
        onOpenChange={(open) => {
          if (!open) {
            setModal(null);
            void refresh().catch(() => {});
          }
        }}
      >
        <DialogContent
          className={cn(
            "max-w-lg gap-5 rounded-2xl",
            modal?.type === "upload" && "sm:max-w-xl",
            modal?.type === "frame" && "h-[min(760px,88dvh)] max-w-xl grid-rows-[auto_1fr]",
          )}
        >
          {modal?.type === "upload" && (
            <SignForm
              source={modal.source}
              state={state}
              builders={builders}
              profiles={profiles}
              builder={builder}
              profile={profile}
              setBuilder={setBuilder}
              setProfile={setProfile}
              onSuccess={async (id) => {
                if (id) void syncPush([...allAppIds(state), id]).catch(() => {});
                await onSaved("App added to the signing queue.");
              }}
              onError={(text) => toast(text, true)}
            />
          )}
          {modal?.type === "tweak" && (
            <TweakForm
              onSuccess={() => onSaved("Tweaks added to your library.")}
              onError={(text) => toast(text, true)}
            />
          )}
          {modal?.type === "profile" && (
            <ProfileForm onSuccess={() => onSaved("Signing profile added.")} onError={(text) => toast(text, true)} />
          )}
          {modal?.type === "builder" && (
            <BuilderForm onSuccess={() => onSaved("Builder added.")} onError={(text) => toast(text, true)} />
          )}
          {modal?.type === "source" && (
            <SourceForm
              onSuccess={(repo) => {
                const next = [...localRepos.filter((item) => item.url !== repo.url), repo];
                setLocalRepos(next);
                writeLocal("signtools.sources", next);
                setHiddenRepos(hiddenRepos.filter((url) => url !== repo.url));
                writeLocal(
                  "signtools.hiddenSources",
                  hiddenRepos.filter((url) => url !== repo.url),
                );
                setModal(null);
                toast("Source added to this browser.");
              }}
              onError={(text) => toast(text, true)}
            />
          )}
          {modal?.type === "frame" && (
            <>
              <DialogHeader>
                <DialogTitle>{modal.title}</DialogTitle>
                <DialogDescription className="flex items-center justify-between">
                  Continue here without leaving your library.
                  <a
                    href={modal.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 text-primary"
                  >
                    Open page
                    <ExternalLink size={12} />
                  </a>
                </DialogDescription>
              </DialogHeader>
              <iframe
                title={modal.title}
                src={modal.url}
                className="h-full min-h-0 w-full rounded-xl border bg-background"
              />
            </>
          )}
        </DialogContent>
      </Dialog>
      {notice && (
        <div
          role={notice.error ? "alert" : "status"}
          className={cn(
            "fixed bottom-24 left-4 right-4 z-[100] mx-auto flex max-w-lg items-start gap-3 rounded-xl border bg-card px-4 py-3 shadow-xl md:bottom-6 md:left-auto md:right-6",
            notice.error ? "border-red-300" : "border-emerald-200",
          )}
        >
          <span className={cn("mt-0.5 shrink-0", notice.error ? "text-red-500" : "text-primary")}>
            {notice.error ? <XCircle size={17} /> : <CheckCircle2 size={17} />}
          </span>
          <p className="min-w-0 flex-1 break-words text-xs leading-relaxed">{notice.text}</p>
          <button
            onClick={() => setNotice(null)}
            aria-label="Dismiss notification"
            className="shrink-0 text-muted-foreground"
          >
            <X size={14} />
          </button>
        </div>
      )}
    </div>
  );
}
function AppCard({
  group,
  onInstall,
  onFrame,
  onCreate,
  onAction,
  toast,
}: {
  group: AppGroup;
  onInstall: (app: AppItem) => void;
  onFrame: (title: string, url: string) => void;
  onCreate: (app: AppItem) => void;
  onAction: (url: string, message?: string) => Promise<void>;
  toast: (text: string, error?: boolean) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const first = group.versions[0];
  if (!first) return null;
  const versionRows = expanded ? group.versions : group.versions.slice(0, 1);
  return (
    <article className="surface enter overflow-hidden">
      <div className="flex items-center gap-3.5 px-5 pb-4 pt-5">
        <AppIcon name={group.name} src={group.icon} />
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-[15px] font-semibold tracking-[-.02em]" title={group.name}>
            {group.name}
          </h2>
          <p className="mt-1 truncate font-mono text-[10px] text-muted-foreground" title={group.bundleId}>
            {group.bundleId || "Bundle ID will appear after signing"}
          </p>
          <div className="mt-2 flex items-center gap-1.5">
            <span className="text-[10px] text-muted-foreground">
              {group.versions.length} version{group.versions.length === 1 ? "" : "s"}
            </span>
            <span className="text-[10px] text-border">/</span>
            <span className="text-[10px] text-muted-foreground">
              {group.apps.length} build{group.apps.length === 1 ? "" : "s"}
            </span>
          </div>
        </div>
        <span className="self-start rounded-full border px-2 py-1 text-[9px] font-medium text-muted-foreground">
          {group.apps.every((app) => app.IsMac) ? "macOS" : "iOS"}
        </span>
      </div>
      <div className="divide-y border-t">
        {versionRows.map(({ version, builds }, index) => (
          <div key={version}>
            <div className="flex items-center gap-2 bg-background/60 px-5 py-2.5">
              <span className="text-xs font-semibold">{version ? `v${version}` : "Unknown version"}</span>
              {index === 0 && (
                <span className="rounded bg-primary/8 px-1.5 py-0.5 text-[8px] font-semibold uppercase tracking-wider text-primary">
                  Latest
                </span>
              )}
              <span className="ml-auto text-[10px] text-muted-foreground">
                {builds.length} build{builds.length === 1 ? "" : "s"}
              </span>
            </div>
            <div className="divide-y divide-border/60">
              {builds.map((app) => (
                <div key={app.Id} className="px-5 py-3.5">
                  <div className="flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <div className="flex items-center gap-1.5 text-xs">
                        <ShieldCheck size={13} className="shrink-0 text-muted-foreground" />
                        <span className="truncate font-medium">{app.ProfileName || "Unknown profile"}</span>
                      </div>
                      <p className="mt-1.5 flex flex-wrap items-center gap-1.5 text-[10px] text-muted-foreground">
                        <span title={app.ModTime}>{relativeTime(app.ModTime)}</span>
                        {app.TweakCount > 0 && (
                          <span title={app.TweakNames}>
                            · {app.TweakCount} tweak{app.TweakCount === 1 ? "" : "s"}
                          </span>
                        )}
                      </p>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5">
                      <Status value={app.Status} />
                      {app.Status === 1 && !app.IsMac && (
                        <Button size="sm" className="ml-1 h-7 px-3 text-[10px]" onClick={() => onInstall(app)}>
                          Install
                          <ArrowDownToLine size={11} className="ml-1.5" />
                        </Button>
                      )}
                      {app.Status === 1 && app.IsMac && (
                        <Button size="sm" variant="outline" asChild className="ml-1 h-7 px-2 text-[10px]">
                          <a href={app.DownloadSignedUrl}>
                            Download
                            <Download size={11} className="ml-1" />
                          </a>
                        </Button>
                      )}
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-7 w-6 text-muted-foreground"
                            aria-label={`Actions for ${app.Name} ${app.Version}`}
                          >
                            <Ellipsis size={16} />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuLabel className="text-xs">Build actions</DropdownMenuLabel>
                          <DropdownMenuItem onSelect={() => onCreate(app)}>
                            <Copy size={14} className="mr-2" />
                            Create from this app
                          </DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => void onAction(app.ResignUrl, "Signing started.")}>
                            <RefreshCw size={14} className="mr-2" />
                            Sign again
                          </DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => onFrame("Rename app", app.RenameUrl)}>
                            Rename app
                          </DropdownMenuItem>
                          {(app.Status === 0 || app.Status === 3) && (
                            <DropdownMenuItem onSelect={() => onFrame("Verification code", app.TwoFactorUrl)}>
                              <KeyRound size={14} className="mr-2" />
                              Submit 2FA code
                            </DropdownMenuItem>
                          )}
                          <DropdownMenuSeparator />
                          {app.Status === 1 && (
                            <DropdownMenuItem asChild>
                              <a href={app.DownloadSignedUrl}>
                                <Download size={14} className="mr-2" />
                                Download signed IPA
                              </a>
                            </DropdownMenuItem>
                          )}
                          <DropdownMenuItem asChild>
                            <a href={app.DownloadUnsignedUrl}>Download original IPA</a>
                          </DropdownMenuItem>
                          {app.TweakCount > 0 && (
                            <DropdownMenuItem asChild>
                              <a href={app.DownloadTweaksUrl}>Download tweaks</a>
                            </DropdownMenuItem>
                          )}
                          {app.Status === 1 && !app.IsMac && (
                            <DropdownMenuItem
                              onSelect={() => {
                                void navigator.clipboard
                                  .writeText(new URL(app.InstallUrl, location.origin).href)
                                  .then(() => toast("Installation link copied."))
                                  .catch(() => toast("Could not copy the link.", true));
                              }}
                            >
                              Copy installation link
                            </DropdownMenuItem>
                          )}
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            className="text-destructive focus:text-destructive"
                            onSelect={() => {
                              if (confirm(`Delete ${app.Name} ${app.Version}? This removes this build and its files.`))
                                void onAction(app.DeleteUrl, "Build deleted.");
                            }}
                          >
                            <Trash2 size={14} className="mr-2" />
                            Delete build
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </div>
                  {app.WorkflowUrl && <BuildLog app={app} />}
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
      {group.versions.length > 1 && (
        <button
          className="flex w-full items-center justify-center gap-1.5 border-t bg-background/40 px-5 py-3 text-[10px] font-medium text-muted-foreground hover:bg-secondary"
          onClick={() => setExpanded(!expanded)}
          aria-expanded={expanded}
        >
          {expanded
            ? "Show latest version"
            : `View ${group.versions.length - 1} older version${group.versions.length === 2 ? "" : "s"}`}
          <ChevronDown size={12} className={cn("transition-transform", expanded && "rotate-180")} />
        </button>
      )}
    </article>
  );
}
function BuildLog({ app }: { app: AppItem }) {
  const [open, setOpen] = useState(false),
    [text, setText] = useState(""),
    [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setBusy(true);
    setText("");
    void (async () => {
      try {
        const response = await fetch(`/api/apps/${encodeURIComponent(app.Id)}/logs`, {
          credentials: "same-origin",
          redirect: "error",
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(`Logs unavailable (${response.status}).`);
        const reader = response.body?.getReader();
        if (!reader) {
          setText("No log output yet.");
          return;
        }
        const decoder = new TextDecoder();
        let content = "";
        while (true) {
          const chunk = await reader.read();
          if (chunk.done) break;
          content = (content + decoder.decode(chunk.value, { stream: true })).slice(-96000);
          setText(content);
        }
        setText(content || "No log output yet.");
      } catch (error) {
        if (!controller.signal.aborted) setText(errorText(error));
      } finally {
        if (!controller.signal.aborted) setBusy(false);
      }
    })();
    return () => controller.abort();
  }, [open, app.Id]);
  return (
    <div className="mt-3">
      <button
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="flex items-center gap-1.5 text-[10px] text-muted-foreground hover:text-foreground"
      >
        <Terminal size={11} />
        {open ? "Hide progress log" : "View progress log"}
        <ChevronDown size={10} className={cn(open && "rotate-180")} />
      </button>
      {open && (
        <div className="mt-2 overflow-hidden rounded-lg border bg-background">
          <div className="flex items-center justify-between border-b px-3 py-2 text-[9px] text-muted-foreground">
            <span className="flex items-center gap-1.5">
              {busy && <Loader2 size={10} className="animate-spin" />}Builder output
            </span>
            <a href={app.WorkflowUrl} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1">
              Open status
              <ExternalLink size={9} />
            </a>
          </div>
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all p-3 font-mono text-[10px] leading-relaxed">
            {text || "Connecting to builder…"}
          </pre>
        </div>
      )}
    </div>
  );
}
function Sources({
  repos,
  query,
  setQuery,
  add,
  remove,
  onSign,
}: {
  repos: Repository[];
  query: string;
  setQuery: (q: string) => void;
  add: () => void;
  remove: (repo: Repository) => void;
  onSign: (app: RepoApp, version: RepoApp["versions"][number]) => void;
}) {
  const [active, setActive] = useState(repos[0]?.url || ""),
    [data, setData] = useState<RepoData | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [reload, setReload] = useState(0);
  useEffect(() => {
    if (!repos.some((repo) => repo.url === active)) setActive(repos[0]?.url || "");
  }, [repos, active]);
  useEffect(() => {
    if (!active) {
      setData(null);
      return;
    }
    const controller = new AbortController();
    setBusy(true);
    setError("");
    setData(null);
    void request<RepoData>(`/api/repository?url=${encodeURIComponent(active)}`, { signal: controller.signal })
      .then(setData)
      .catch((error) => {
        if (!controller.signal.aborted) setError(errorText(error));
      })
      .finally(() => {
        if (!controller.signal.aborted) setBusy(false);
      });
    return () => controller.abort();
  }, [active, reload]);
  if (!repos.length)
    return (
      <Empty
        icon={Globe2}
        title="A world of apps, one library"
        description="Add an AltStore-compatible HTTPS source to browse apps, choose versions, and sign them in a few taps."
        action={
          <Button variant="outline" onClick={add}>
            <Plus size={15} className="mr-2" />
            Add your first source
          </Button>
        }
      />
    );
  const apps = (data?.apps || []).filter((app) =>
    [app.name, app.bundleIdentifier, app.developerName].join(" ").toLowerCase().includes(query.toLowerCase()),
  );
  return (
    <div>
      <div className="mb-6 flex flex-wrap gap-2">
        {repos.map((repo) => (
          <div
            key={repo.url}
            className={cn(
              "flex max-w-full items-center gap-1 rounded-lg border bg-card pl-3 pr-1 text-xs",
              active === repo.url && "border-primary/50 bg-primary/5",
            )}
          >
            <button onClick={() => setActive(repo.url)} className="flex min-w-0 items-center gap-2 py-2.5">
              <Globe2 size={13} className="shrink-0 text-primary" />
              <span className="truncate font-medium">{repo.name}</span>
            </button>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="h-6 w-6" aria-label={`Source actions for ${repo.name}`}>
                  <Ellipsis size={13} />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem asChild>
                  <a href={repo.url} target="_blank" rel="noopener noreferrer">
                    Open source JSON
                    <ExternalLink size={12} className="ml-2" />
                  </a>
                </DropdownMenuItem>
                <DropdownMenuItem className="text-destructive" onSelect={() => remove(repo)}>
                  Remove from this browser
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ))}
      </div>
      <div className="surface mb-6 flex flex-wrap items-center justify-between gap-4 p-5">
        <div>
          <div className="mb-1 flex items-center gap-2">
            <h2 className="text-sm font-semibold">{data?.name || repos.find((repo) => repo.url === active)?.name}</h2>
            {data && (
              <Badge variant="secondary" className="text-[9px]">
                {data.apps?.length || 0} apps
              </Badge>
            )}
          </div>
          <p className="max-w-[70vw] truncate font-mono text-[10px] text-muted-foreground">{active}</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="relative">
            <Search size={13} className="absolute left-3 top-3 text-muted-foreground" />
            <Input
              aria-label="Search source apps"
              className="h-9 w-48 pl-9 text-xs"
              placeholder="Search source..."
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
          <Button
            variant="outline"
            size="icon"
            className="h-9 w-9"
            aria-label="Refresh source"
            onClick={() => setReload(reload + 1)}
            disabled={busy}
          >
            <RefreshCw size={14} className={cn(busy && "animate-spin")} />
          </Button>
        </div>
      </div>
      {busy ? (
        <div className="flex min-h-64 items-center justify-center gap-2 text-xs text-muted-foreground">
          <Spinner />
          Loading source…
        </div>
      ) : error ? (
        <div className="surface p-8 text-center">
          <XCircle size={22} className="mx-auto mb-3 text-destructive" />
          <p className="text-sm font-medium">Couldn’t load this source</p>
          <p className="mx-auto mt-2 max-w-lg break-words text-xs leading-relaxed text-muted-foreground">{error}</p>
          <Button variant="outline" size="sm" className="mt-4" onClick={() => setReload(reload + 1)}>
            Try again
          </Button>
        </div>
      ) : apps.length ? (
        <div className="grid items-start gap-4 lg:grid-cols-2 2xl:grid-cols-3">
          {apps.map((app, index) => (
            <RepoCard key={`${app.bundleIdentifier}-${index}`} app={app} onSign={onSign} />
          ))}
        </div>
      ) : (
        <Empty title="No apps found" description="Try another search or choose a different source." />
      )}
    </div>
  );
}
function RepoCard({
  app,
  onSign,
}: {
  app: RepoApp;
  onSign: (app: RepoApp, version: RepoApp["versions"][number]) => void;
}) {
  const versions = [...(app.versions || [])].sort((a, b) => compareVersions(b.version, a.version));
  const [selected, setSelected] = useState(versions[0]?.version || "");
  const version = versions.find((item) => item.version === selected) || versions[0];
  return (
    <div className="surface flex h-full flex-col p-5">
      <div className="mb-4 flex items-center gap-3">
        <AppIcon name={app.name} src={app.iconURL} />
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold">{app.name}</h3>
          <p className="mt-1 truncate text-[10px] text-muted-foreground">
            {app.developerName || app.bundleIdentifier}
          </p>
        </div>
      </div>
      <p className="mb-5 line-clamp-3 text-xs leading-relaxed text-muted-foreground">
        {app.localizedDescription || "Available from this source."}
      </p>
      <div className="mt-auto flex items-center gap-2">
        <Select
          aria-label={`Version of ${app.name}`}
          value={selected}
          onChange={(event) => setSelected(event.target.value)}
        >
          {versions.map((item, index) => (
            <option key={`${item.version}-${index}`} value={item.version}>
              {item.version}
              {index === 0 ? " · Latest" : ""}
            </option>
          ))}
        </Select>
        <Button
          size="sm"
          className="h-10 shrink-0 px-3 text-xs"
          disabled={!version?.downloadURL}
          onClick={() => version && onSign(app, version)}
        >
          Sign app
          <ArrowRight size={13} className="ml-2" />
        </Button>
      </div>
      {version?.date && (
        <p className="mt-3 text-[9px] text-muted-foreground">
          Updated {relativeTime(version.date)} {version.size ? `· ${(version.size / 1048576).toFixed(1)} MB` : ""}
        </p>
      )}
    </div>
  );
}

async function uploadFiles(files: File[], progress: (value: number) => void, instance: (uploader: Uppy) => void) {
  if (!files.length) return [];
  const uploader = new Uppy({
    id: `upload-${globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`}`,
  }).use(Tus, { endpoint: "/tus/", parallelUploads: 6 });
  instance(uploader);
  uploader.on("progress", progress);
  for (const file of files) uploader.addFile({ name: file.name, type: file.type, data: file });
  try {
    const result = await uploader.upload();
    if (!result) throw new Error("Upload cancelled.");
    if (result.failed?.length) throw new Error(result.failed.map((file) => file.error).join("\n"));
    return (result.successful || []).map((file) => {
      const id = file.uploadURL?.split("/").pop();
      if (!id) throw new Error("The server did not return an upload ID.");
      return id;
    });
  } finally {
    uploader.destroy();
  }
}
function CheckField({
  name,
  label,
  description,
  checked,
  disabled = false,
}: {
  name: string;
  label: string;
  description?: string;
  checked?: boolean;
  disabled?: boolean;
}) {
  return (
    <label className={cn("flex items-start gap-2.5 text-xs", disabled && "opacity-50")}>
      <input type="checkbox" name={name} defaultChecked={checked} disabled={disabled} className="mt-0.5" />
      <span className="leading-relaxed">
        {label}
        {description && <span className="mt-0.5 block text-[10px] text-muted-foreground">{description}</span>}
      </span>
    </label>
  );
}
function SignForm({
  source,
  state,
  builders,
  profiles,
  builder,
  profile,
  setBuilder,
  setProfile,
  onSuccess,
  onError,
}: {
  source?: UploadSource;
  state: State;
  builders: Builder[];
  profiles: Profile[];
  builder: string;
  profile: string;
  setBuilder: (id: string) => void;
  setProfile: (id: string) => void;
  onSuccess: (id?: string) => Promise<void>;
  onError: (text: string) => void;
}) {
  const [mode, setMode] = useState(source?.url ? "url" : source?.id ? "existing" : "file"),
    [busy, setBusy] = useState(false),
    [progress, setProgress] = useState(0),
    [idType, setIdType] = useState("id_original"),
    [advanced, setAdvanced] = useState(false),
    [sourceImport, setSourceImport] = useState(!!source?.url),
    [files, setFiles] = useState<File[]>([]),
    [tweaks, setTweaks] = useState<File[]>([]),
    [tweakMode, setTweakMode] = useState("library");
  const uploader = useRef<Uppy | null>(null),
    selectedProfile = profiles.find((item) => item.Id === profile),
    account = !!selectedProfile?.IsAccount;
  useEffect(() => () => uploader.current?.cancelAll(), []);
  useEffect(() => {
    if (account && idType === "id_prov") setIdType("id_original");
  }, [account, idType]);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const body = new FormData(event.currentTarget);
    setBusy(true);
    setProgress(0);
    try {
      const ipaFiles = mode === "file" ? files : [];
      const tweakFiles = tweakMode === "upload" ? tweaks : [];
      const ids = await uploadFiles([...ipaFiles, ...tweakFiles], setProgress, (instance) => {
        uploader.current = instance;
      });
      if (mode === "file") body.set("file_id", ids[0] || "");
      if (mode === "existing") body.set("file_id", source?.id || "");
      body.set("tweak_ids", ids.slice(ipaFiles.length).join(","));
      if (sourceImport && mode === "url") body.set("source_import", "true");
      // Notification support must never prevent signing on an HTTP/LAN deployment.
      if ("serviceWorker" in navigator && window.isSecureContext) {
        try {
          const registration = await navigator.serviceWorker.getRegistration("/");
          const subscription = await registration?.pushManager?.getSubscription();
          if (subscription) body.set("push_endpoint", subscription.endpoint);
        } catch {}
      }
      const result = await request<{ appId?: string }>("/apps", { method: "POST", body });
      await onSuccess(result?.appId);
    } catch (error) {
      setFiles([]);
      setTweaks([]);
      const fileFields = document.querySelectorAll<HTMLInputElement>("#sign-form input[type=file]");
      for (const field of fileFields) field.value = "";
      onError(`${errorText(error)}${mode === "file" ? " Please select your files again." : ""}`);
    } finally {
      uploader.current = null;
      setBusy(false);
    }
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>Sign an app</DialogTitle>
        <DialogDescription>Choose an app, a builder, and your signing profile.</DialogDescription>
      </DialogHeader>
      <form id="sign-form" onSubmit={(event) => void submit(event)} className="grid gap-5">
        <fieldset disabled={busy} className="dialog-scroll grid gap-5">
          <div>
            <div className="mb-2.5 flex items-center justify-between">
              <label className="text-xs font-medium">App file</label>
              {mode !== "existing" && (
                <div className="flex rounded-md border p-0.5">
                  {["file", "url"].map((item) => (
                    <button
                      key={item}
                      type="button"
                      className={cn(
                        "rounded px-2 py-1 text-[10px]",
                        mode === item ? "bg-secondary font-medium" : "text-muted-foreground",
                      )}
                      onClick={() => {
                        setMode(item);
                        setSourceImport(false);
                      }}
                    >
                      {item === "file" ? "Upload" : "From URL"}
                    </button>
                  ))}
                </div>
              )}
            </div>
            {mode === "existing" ? (
              <div className="flex items-center gap-3 rounded-lg border bg-secondary/40 p-4">
                <FileArchive size={22} className="text-primary" />
                <div>
                  <p className="text-xs font-medium">{source?.name}</p>
                  <p className="mt-1 text-[10px] text-muted-foreground">Create a new build from the original IPA.</p>
                </div>
              </div>
            ) : mode === "url" ? (
              <>
                <Input
                  aria-label="App download URL"
                  name="file_url"
                  type="url"
                  required
                  defaultValue={source?.url}
                  placeholder="https://example.com/app.ipa"
                  onChange={() => setSourceImport(false)}
                />
                {source?.name && <p className="mt-2 text-xs text-muted-foreground">{source.name}</p>}
              </>
            ) : (
              <label className="flex cursor-pointer flex-col items-center gap-2 rounded-xl border border-dashed bg-background px-4 py-6 text-center hover:border-primary/50">
                <span className="mb-1 rounded-xl border bg-card p-3 text-primary">
                  <Upload size={20} />
                </span>
                <span className="text-xs font-medium">{files[0]?.name || "Choose an IPA file"}</span>
                <span className="text-[10px] text-muted-foreground">Resumable upload · .ipa or .zip</span>
                <Input
                  aria-label="Choose IPA file"
                  type="file"
                  accept=".ipa,.zip"
                  required
                  className="mt-1 max-w-xs text-[10px]"
                  onChange={(event) => setFiles(Array.from(event.target.files || []))}
                />
              </label>
            )}
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Builder">
              <Select
                name="builder_id"
                aria-label="Signing builder"
                value={builder}
                onChange={(event) => setBuilder(event.target.value)}
                required
              >
                <option value="" disabled>
                  Select a builder
                </option>
                {builders.map((item) => (
                  <option key={item.Id} value={item.Id}>
                    {item.Name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="Signing profile">
              <Select
                name="profile_id"
                aria-label="Signing profile"
                value={profile}
                onChange={(event) => setProfile(event.target.value)}
                required
              >
                <option value="" disabled>
                  Select a profile
                </option>
                {profiles.map((item) => (
                  <option key={item.Id} value={item.Id}>
                    {item.Name}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          {!profiles.length && (
            <p className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
              Add a signing profile in Settings before signing an app.
            </p>
          )}
          <Field label="Bundle identifier">
            <Select
              name="id"
              aria-label="Bundle identifier mode"
              value={idType}
              onChange={(event) => setIdType(event.target.value)}
            >
              <option value="id_original">Keep the app’s original identifier</option>
              <option value="id_prov" disabled={account}>
                Use the provisioning profile’s identifier
              </option>
              <option value="id_custom">Use a custom identifier</option>
            </Select>
            {idType === "id_custom" && (
              <Input
                name="id_custom_text"
                aria-label="Custom bundle identifier"
                placeholder="com.example.app"
                required
                pattern="[A-Za-z0-9][A-Za-z0-9.\-]*"
              />
            )}
          </Field>
          <div className="border-t pt-4">
            <button
              type="button"
              onClick={() => setAdvanced(!advanced)}
              className="flex w-full items-center justify-between text-xs font-medium"
              aria-expanded={advanced}
            >
              <span className="flex items-center gap-2">
                <Settings2 size={14} />
                Advanced signing options
              </span>
              <ChevronDown size={14} className={cn(advanced && "rotate-180")} />
            </button>
            <div className={cn("mt-5 grid gap-5", !advanced && "hidden")}>
              <Field label="Home Screen name (optional)">
                <Input name="bundle_name" placeholder="Keep original app name" />
              </Field>
              <div className="grid gap-3">
                <p className="eyebrow">Identifier options</p>
                <CheckField
                  key={`encode-${profile}`}
                  name="id_encode"
                  label="Encode original identifiers to unique identifiers"
                  description="Available for Apple account signing."
                  checked={account}
                  disabled={!account}
                />
                <CheckField name="id_patch" label="Patch new identifiers into app binaries" checked />
                <CheckField
                  name="id_force_original"
                  label="Force the original bundle identifier"
                  description="Requires a distribution certificate. Can affect entitlements."
                />
              </div>
              <div className="grid gap-3">
                <p className="eyebrow">App options</p>
                <CheckField
                  name="app_debug"
                  label="Enable app debugging"
                  description="Requires a development certificate."
                />
                <CheckField name="all_devices" label="Enable support for all devices" checked />
                <CheckField name="mac" label="Apply patches for Mac" />
                <CheckField name="file_share" label="Enable file sharing" />
              </div>
              <div>
                <div className="mb-3 flex items-center justify-between">
                  <label className="text-xs font-medium">Inject tweaks</label>
                  <Select
                    aria-label="Tweak source"
                    value={tweakMode}
                    onChange={(event) => setTweakMode(event.target.value)}
                    className="select h-8 w-32 text-xs"
                  >
                    <option value="library">From library</option>
                    <option value="upload">Upload files</option>
                  </Select>
                </div>
                {tweakMode === "library" ? (
                  <div className="max-h-40 space-y-3 overflow-auto rounded-lg border p-3">
                    {state.Tweaks.length ? (
                      state.Tweaks.map((tweak) => (
                        <label key={tweak.Id} className="flex items-center gap-2 text-xs">
                          <input type="checkbox" name="saved_tweak_ids" value={tweak.Id} />
                          <span className="truncate">{tweak.Name}</span>
                        </label>
                      ))
                    ) : (
                      <p className="text-xs text-muted-foreground">No saved tweaks. Add some in the Tweaks view.</p>
                    )}
                  </div>
                ) : (
                  <Input
                    aria-label="Choose tweak files"
                    type="file"
                    multiple
                    onChange={(event) => setTweaks(Array.from(event.target.files || []))}
                  />
                )}
              </div>
            </div>
          </div>
        </fieldset>
        {busy && (
          <div>
            <div className="mb-2 flex items-center justify-between text-[10px] text-muted-foreground">
              <span>{progress < 100 ? "Uploading files…" : "Starting signing job…"}</span>
              <span>{progress}%</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-secondary">
              <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${progress}%` }} />
            </div>
          </div>
        )}
        <div className="flex items-center justify-between gap-3 border-t pt-4">
          <p className="text-[10px] text-muted-foreground">Preferences saved on this device</p>
          <Button type="submit" disabled={busy || !builder || !profile} className="gap-2">
            {busy ? <Spinner /> : <ShieldCheck size={15} />}Start signing
          </Button>
        </div>
      </form>
    </>
  );
}
function TweakForm({ onSuccess, onError }: { onSuccess: () => Promise<void>; onError: (text: string) => void }) {
  const [files, setFiles] = useState<File[]>([]),
    [busy, setBusy] = useState(false),
    [progress, setProgress] = useState(0);
  const uploader = useRef<Uppy | null>(null);
  useEffect(() => () => uploader.current?.cancelAll(), []);
  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    try {
      const ids = await uploadFiles(files, setProgress, (instance) => {
        uploader.current = instance;
      });
      await request("/tweaks", { method: "POST", body: new URLSearchParams({ tweak_ids: ids.join(",") }) });
      await onSuccess();
    } catch (error) {
      onError(errorText(error));
    } finally {
      uploader.current = null;
      setBusy(false);
    }
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>Add tweaks</DialogTitle>
        <DialogDescription>Upload once. Reuse them with any app you sign.</DialogDescription>
      </DialogHeader>
      <form className="grid gap-5" onSubmit={(event) => void submit(event)}>
        <Field label="Tweak files">
          <Input
            aria-label="Choose tweak files"
            type="file"
            multiple
            required
            disabled={busy}
            onChange={(event) => setFiles(Array.from(event.target.files || []))}
          />
        </Field>
        {busy && <p className="text-xs text-muted-foreground">Uploading… {progress}%</p>}
        <Button type="submit" disabled={busy} className="ml-auto gap-2">
          {busy ? <Spinner /> : <Upload size={15} />}Add to library
        </Button>
      </form>
    </>
  );
}
function ProfileForm({ onSuccess, onError }: { onSuccess: () => Promise<void>; onError: (text: string) => void }) {
  const [mode, setMode] = useState("provisioning"),
    [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const body = new FormData(event.currentTarget);
    setBusy(true);
    try {
      await request("/api/profiles", { method: "POST", body });
      await onSuccess();
    } catch (error) {
      onError(errorText(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>Add signing profile</DialogTitle>
        <DialogDescription>Certificate material is stored only on your server.</DialogDescription>
      </DialogHeader>
      <form onSubmit={(event) => void submit(event)} className="grid gap-5">
        <fieldset disabled={busy} className="dialog-scroll grid gap-4">
          <Field label="Profile name">
            <Input name="name" required placeholder="My signing profile" autoComplete="off" />
          </Field>
          <Field label="Signing method">
            <Select aria-label="Signing method" value={mode} onChange={(event) => setMode(event.target.value)}>
              <option value="provisioning">Certificate + provisioning profile</option>
              <option value="account">Certificate + Apple account</option>
            </Select>
          </Field>
          <Field label="Signing certificate (.p12)">
            <Input aria-label="Signing certificate" type="file" name="cert" accept=".p12,.pfx" required />
          </Field>
          <Field label="Certificate password">
            <Input
              type="password"
              name="cert_pass"
              autoComplete="new-password"
              placeholder="Leave empty if not password protected"
            />
          </Field>
          {mode === "provisioning" ? (
            <Field label="Provisioning profile (.mobileprovision)">
              <Input
                aria-label="Provisioning profile file"
                type="file"
                name="prov"
                accept=".mobileprovision,.provisionprofile"
                required
              />
            </Field>
          ) : (
            <>
              <Field label="Apple account">
                <Input name="account_name" type="email" required autoComplete="off" placeholder="name@example.com" />
              </Field>
              <Field label="Apple account password">
                <Input name="account_pass" type="password" required autoComplete="new-password" />
              </Field>
              <p className="text-xs leading-relaxed text-muted-foreground">
                Apple may ask for a verification code while signing. Submit it from the app’s build actions.
              </p>
            </>
          )}
        </fieldset>
        <Button type="submit" disabled={busy} className="ml-auto gap-2">
          {busy ? <Spinner /> : <KeyRound size={14} />}Save profile
        </Button>
      </form>
    </>
  );
}
function BuilderForm({ onSuccess, onError }: { onSuccess: () => Promise<void>; onError: (text: string) => void }) {
  const [type, setType] = useState("github"),
    [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget),
      get = (key: string) => String(form.get(key) || "");
    const data = {
      id: get("id"),
      name: get("name"),
      type,
      [type]:
        type === "github"
          ? {
              enable: true,
              org_name: get("org_name"),
              repo_name: get("repo_name"),
              workflow_file_name: get("workflow_file_name"),
              token: get("token"),
              ref: get("ref"),
            }
          : type === "selfhosted"
            ? { enable: true, url: get("url"), key: get("key") }
            : {
                enable: true,
                org_name: get("org_name"),
                project_name: get("project_name"),
                token: get("token"),
                ref: get("ref"),
                secret_name: get("secret_name"),
              },
    };
    setBusy(true);
    try {
      await postJSON("/api/builders", data);
      await onSuccess();
    } catch (error) {
      onError(errorText(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>Add a builder</DialogTitle>
        <DialogDescription>Connect another signing service to your workspace.</DialogDescription>
      </DialogHeader>
      <form onSubmit={(event) => void submit(event)} className="grid gap-5">
        <fieldset disabled={busy} className="dialog-scroll grid gap-4">
          <Field label="Builder type">
            <Select aria-label="Builder type" value={type} onChange={(event) => setType(event.target.value)}>
              <option value="github">GitHub Actions</option>
              <option value="selfhosted">Self-hosted</option>
              <option value="semaphore">Semaphore</option>
            </Select>
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Display name">
              <Input name="name" required placeholder="My builder" autoComplete="off" />
            </Field>
            <Field label="Unique ID">
              <Input name="id" required pattern="[A-Za-z0-9_-]+" placeholder="my-builder" autoComplete="off" />
            </Field>
          </div>
          {type === "selfhosted" ? (
            <>
              <Field label="Builder URL">
                <Input name="url" required type="url" placeholder="http://192.168.1.10:8090" />
              </Field>
              <Field label="Builder key">
                <Input name="key" required type="password" autoComplete="new-password" />
              </Field>
            </>
          ) : (
            <>
              <Field label={type === "github" ? "Repository owner" : "Organization name"}>
                <Input name="org_name" required autoComplete="off" />
              </Field>
              <Field label={type === "github" ? "Repository name" : "Project name"}>
                <Input
                  name={type === "github" ? "repo_name" : "project_name"}
                  required
                  defaultValue={type === "github" ? "SignTools-CI" : ""}
                  autoComplete="off"
                />
              </Field>
              {type === "github" ? (
                <Field label="Workflow file">
                  <Input name="workflow_file_name" required defaultValue="sign.yml" />
                </Field>
              ) : (
                <Field label="Secret name">
                  <Input name="secret_name" required defaultValue="ios-signer" />
                </Field>
              )}
              <Field label="Branch / ref">
                <Input
                  key={type}
                  name="ref"
                  required
                  defaultValue={type === "github" ? "master" : "refs/heads/master"}
                />
              </Field>
              <Field label={type === "github" ? "GitHub token" : "Semaphore token"}>
                <Input name="token" required type="password" autoComplete="new-password" />
              </Field>
            </>
          )}
          <p className="rounded-lg bg-secondary/60 p-3 text-[10px] leading-relaxed text-muted-foreground">
            Credentials are sent directly to the server and are never returned to the browser. The server will
            configure the builder’s signing service secrets.
          </p>
        </fieldset>
        <Button type="submit" disabled={busy} className="ml-auto gap-2">
          {busy ? <Spinner /> : <Server size={14} />}Connect builder
        </Button>
      </form>
    </>
  );
}
function SourceForm({
  onSuccess,
  onError,
}: {
  onSuccess: (repo: Repository) => void;
  onError: (text: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget),
      url = String(form.get("url") || "").trim(),
      name = String(form.get("name") || "").trim();
    setBusy(true);
    try {
      const data = await request<RepoData>(`/api/repository?url=${encodeURIComponent(url)}`);
      onSuccess({ url, name: name || data.name || new URL(url).hostname });
    } catch (error) {
      onError(errorText(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>Add a source</DialogTitle>
        <DialogDescription>Browse apps from an AltStore-compatible repository.</DialogDescription>
      </DialogHeader>
      <form onSubmit={(event) => void submit(event)} className="grid gap-5">
        <Field label="Repository URL" hint="Use a public HTTPS URL that points to a source JSON file.">
          <Input
            aria-label="Repository URL"
            name="url"
            required
            type="url"
            pattern="https://.*"
            placeholder="https://example.com/source.json"
            disabled={busy}
          />
        </Field>
        <Field label="Display name (optional)">
          <Input name="name" placeholder="Uses the source’s name" disabled={busy} />
        </Field>
        <p className="text-[10px] leading-relaxed text-muted-foreground">
          This source list is saved on this device. Server defaults are available on every device.
        </p>
        <Button type="submit" disabled={busy} className="ml-auto gap-2">
          {busy ? <Spinner /> : <Globe2 size={14} />}Add source
        </Button>
      </form>
    </>
  );
}

const root = document.getElementById("app");
if (root) createRoot(root).render(<App />);
