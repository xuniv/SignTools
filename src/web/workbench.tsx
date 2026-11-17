import React, { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import {
  Apple,
  ArrowDownToLine,
  ChevronRight,
  Download,
  FileKey2,
  KeyRound,
  Loader2,
  LockKeyhole,
  Package,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Smartphone,
  Terminal,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { Button } from "./components/ui/button";
import { Input } from "./components/ui/input";
import { Badge } from "./components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./components/ui/dialog";
import { postJSON, request } from "./lib/api";
import { readLocal, writeLocal, type Artifact, type UploadSource } from "./lib/data";
import {
  clientId,
  createBackup,
  loadAccounts,
  loadModels,
  removeAccount,
  removeModel,
  restoreBackup,
  saveAccount,
  saveModel,
  toBase64,
  type VaultAccount,
  type VaultModel,
} from "./lib/vault";

type StoreApp = {
  id: string;
  bundleId: string;
  name: string;
  version?: string;
  price?: string | number;
  platforms?: string[];
  purchaseDate?: string;
};
type StoreVersion = { id: string; version?: string; name?: string; date?: string };
type Info = { available: boolean; version?: string; revision?: string; platformSupported?: boolean };
type Task = {
  id: string;
  kind: string;
  title: string;
  state: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  progress: number;
  logs: string[];
  error?: string;
  createdAt: string;
  result?: {
    artifact?: Artifact;
    report?: unknown;
    session?: string;
    modelId?: string;
    account?: { email: string; country: string };
  };
};
type StoreResponse = {
  session?: string;
  results?: StoreApp[];
  apps?: StoreApp[];
  count?: number;
  totalCount?: number;
  page?: number;
  versions?: StoreVersion[];
  version?: StoreVersion;
  latestVersionId?: string;
};
type LoginResponse = {
  session: string;
  account: { id: string; email: string; name: string; country: string; storefront: string };
  requires2FA?: boolean;
};
type Notice = { kind: "error" | "success"; text: string } | null;
const selectClass =
  "h-9 w-full min-w-0 rounded-md border border-input bg-background px-3 text-sm shadow-sm outline-none focus:ring-1 focus:ring-ring";
const countries = [
  ["US", "United States"],
  ["KR", "South Korea"],
  ["JP", "Japan"],
  ["GB", "United Kingdom"],
  ["CA", "Canada"],
  ["AU", "Australia"],
  ["DE", "Germany"],
  ["FR", "France"],
  ["HK", "Hong Kong"],
  ["TW", "Taiwan"],
  ["SG", "Singapore"],
  ["IN", "India"],
  ["BR", "Brazil"],
  ["MX", "Mexico"],
  ["IT", "Italy"],
  ["ES", "Spain"],
  ["NL", "Netherlands"],
  ["NZ", "New Zealand"],
];

function Panel({
  title,
  detail,
  action,
  children,
}: {
  title: string;
  detail?: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="rounded-xl border bg-card shadow-sm">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b px-5 py-4">
        <div>
          <h2 className="text-sm font-semibold">{title}</h2>
          {detail && <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{detail}</p>}
        </div>
        {action}
      </div>
      <div className="p-5">{children}</div>
    </section>
  );
}
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="grid min-w-0 gap-2 text-xs font-medium">
      {label}
      {children}
    </label>
  );
}
function Empty({ icon, title, detail }: { icon: ReactNode; title: string; detail: string }) {
  return (
    <div className="flex flex-col items-center px-4 py-12 text-center">
      <div className="mb-4 flex h-11 w-11 items-center justify-center rounded-xl border bg-secondary text-muted-foreground">
        {icon}
      </div>
      <h3 className="text-sm font-medium">{title}</h3>
      <p className="mt-2 max-w-sm text-xs leading-6 text-muted-foreground">{detail}</p>
    </div>
  );
}
function artifactSource(artifact: Artifact): UploadSource {
  return { libraryId: artifact.id, name: artifact.name, metadata: artifact };
}

function DialogError({ notice }: { notice: Notice }) {
  return notice?.kind === "error" ? (
    <p
      role="alert"
      className="rounded-md border border-red-200 bg-red-50 p-3 text-xs leading-relaxed text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300"
    >
      {notice.text}
    </p>
  ) : null;
}

export function Workbench({
  page,
  onSign,
  onInstall,
}: {
  page: string;
  onSign: (source: UploadSource) => void;
  onInstall?: (artifact: Artifact) => void;
}) {
  const [accounts, setAccounts] = useState<VaultAccount[]>([]);
  const [models, setModels] = useState<VaultModel[]>([]);
  const [activeId, setActiveId] = useState(() => readLocal<string>("signtools.store.account", ""));
  const [country, setCountry] = useState(() => readLocal<string>("signtools.store.country", "US"));
  const [storeCountries, setStoreCountries] = useState(
    countries.map(([code, name]) => ({ code: code!, name: name! })),
  );
  const [owner, setOwner] = useState("");
  const [notice, setNotice] = useState<Notice>(null);
  const [busy, setBusy] = useState("");
  const [info, setInfo] = useState<{ store?: Info; decrypt?: Info }>({});
  const [loginOpen, setLoginOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [authCode, setAuthCode] = useState("");
  const [challenge, setChallenge] = useState<string | null>(null);
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<StoreApp[]>([]);
  const [mode, setMode] = useState<"search" | "purchases">("search");
  const [resultPage, setResultPage] = useState(1);
  const [totalResults, setTotalResults] = useState(0);
  const [purchaseApp, setPurchaseApp] = useState<StoreApp | null>(null);
  const [versionApp, setVersionApp] = useState<StoreApp | null>(null);
  const [versions, setVersions] = useState<StoreVersion[]>([]);
  const [versionId, setVersionId] = useState("");
  const [versionDetails, setVersionDetails] = useState<StoreVersion | null>(null);
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [modelId, setModelId] = useState("auto");
  const [stripAccount, setStripAccount] = useState(true);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [passphrase, setPassphrase] = useState("");
  const [confirmPassphrase, setConfirmPassphrase] = useState("");
  const [restorePassphrase, setRestorePassphrase] = useState("");
  const [backupFile, setBackupFile] = useState<File | null>(null);
  const [restoreOpen, setRestoreOpen] = useState(false);
  const modelInput = useRef<HTMLInputElement>(null);
  const accountsRef = useRef(accounts);
  accountsRef.current = accounts;
  const active = accounts.find((account) => account.id === activeId) || accounts[0];

  const fail = useCallback(
    (error: unknown) => setNotice({ kind: "error", text: error instanceof Error ? error.message : String(error) }),
    [],
  );
  const refreshVault = useCallback(async () => {
    const [savedAccounts, savedModels] = await Promise.all([loadAccounts(), loadModels()]);
    setAccounts(savedAccounts);
    setModels(savedModels);
  }, []);
  const refreshLibrary = useCallback(async () => {
    const data = await request<{ items: Artifact[] }>("/api/library");
    setArtifacts(data.items || []);
  }, []);

  useEffect(() => {
    try {
      setOwner(clientId());
    } catch (error) {
      fail(error);
    }
    void refreshVault().catch(fail);
    void refreshLibrary().catch(fail);
    void request<Info>("/api/store/info")
      .then((store) => setInfo((value) => ({ ...value, store })))
      .catch(fail);
    void request<{ countries: { code: string; storefront: string }[] }>("/api/store/countries")
      .then((data) =>
        setStoreCountries(
          data.countries.map((item) => ({
            code: item.code,
            name: countries.find(([code]) => code === item.code)?.[1] || item.code,
          })),
        ),
      )
      .catch(() => {});
    void request<Info>("/api/decrypt/info")
      .then((decrypt) => setInfo((value) => ({ ...value, decrypt })))
      .catch(fail);
  }, [fail, refreshVault, refreshLibrary]);

  useEffect(() => {
    if (!owner) return;
    let alive = true;
    const poll = async () => {
      try {
        const data = await request<{ tasks: Task[] }>("/api/tasks?clientId=" + encodeURIComponent(owner));
        if (!alive) return;
        setTasks(data.tasks || []);
        const applied = readLocal<string[]>("signtools.tasks.applied", []);
        const taskAccounts = readLocal<Record<string, string>>("signtools.tasks.accounts", {});
        let changed = false;
        for (const task of data.tasks || []) {
          if (task.state !== "succeeded" || applied.includes(task.id)) continue;
          const account =
            accountsRef.current.find((item) => item.id === taskAccounts[task.id]) ||
            accountsRef.current.find(
              (item) => item.email === task.result?.account?.email && item.country === task.result?.account?.country,
            );
          if (account && task.result?.session) {
            await saveAccount({ ...account, session: task.result.session });
            changed = true;
          }
          applied.push(task.id);
          if (task.result?.artifact) changed = true;
        }
        writeLocal("signtools.tasks.applied", applied.slice(-250));
        if (changed) {
          await refreshVault();
          await refreshLibrary();
        }
      } catch (error) {
        if (alive) fail(error);
      }
    };
    void poll();
    const timer = setInterval(() => void poll(), 2500);
    return () => {
      alive = false;
      clearInterval(timer);
    };
  }, [owner, fail, refreshVault, refreshLibrary]);

  async function storeCall<T extends StoreResponse = StoreResponse>(
    operation: string,
    fields: Record<string, unknown> = {},
  ): Promise<T> {
    if (!active) throw new Error("Add an Apple account to continue.");
    const data = await postJSON<T>("/api/store/" + operation, { clientId: owner, session: active.session, ...fields });
    if (data.session) {
      await saveAccount({ ...active, session: data.session });
      await refreshVault();
    }
    return data;
  }

  async function logIn(event: FormEvent) {
    event.preventDefault();
    setBusy("login");
    setNotice(null);
    try {
      if (!crypto.subtle) throw new Error("Encrypted Apple accounts require HTTPS or localhost.");
      const response = await postJSON<LoginResponse>("/api/store/login", {
        clientId: owner,
        email,
        password: challenge ? undefined : password,
        authCode: authCode || undefined,
        session: challenge || undefined,
      });
      setPassword("");
      if (response.requires2FA) {
        setChallenge(response.session);
        setAuthCode("");
        return;
      }
      const previous = accounts.find(
        (account) =>
          account.email.toLowerCase() === response.account.email.toLowerCase() &&
          account.country === response.account.country,
      );
      const saved: VaultAccount = {
        ...response.account,
        id: previous?.id || crypto.randomUUID(),
        session: response.session,
      };
      await saveAccount(saved);
      await refreshVault();
      setActiveId(saved.id);
      writeLocal("signtools.store.account", saved.id);
      setCountry(saved.country || "US");
      writeLocal("signtools.store.country", saved.country || "US");
      setLoginOpen(false);
      setEmail("");
      setAuthCode("");
      setChallenge(null);
      setNotice({ kind: "success", text: "Apple account saved in this browser's encrypted vault." });
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function browse(nextMode: "search" | "purchases", pageNumber = 1) {
    setBusy("browse");
    setNotice(null);
    setMode(nextMode);
    try {
      const data = await storeCall(nextMode, { term, country: country || undefined, limit: 25, page: pageNumber });
      setResults(data.results || data.apps || []);
      setResultPage(pageNumber);
      setTotalResults(data.totalCount ?? data.count ?? (data.results || data.apps || []).length);
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function showVersions(app: StoreApp) {
    setVersionApp(app);
    setVersions([]);
    setVersionId("");
    setVersionDetails(null);
    setBusy("versions");
    setNotice(null);
    try {
      const response = await storeCall("versions", { appId: app.id, bundleId: app.bundleId });
      setVersions(response.versions || []);
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function showVersionMetadata(id: string) {
    setVersionId(id);
    setVersionDetails(null);
    if (!id || !versionApp) return;
    setBusy("metadata");
    try {
      const response = await storeCall("version-metadata", {
        appId: versionApp.id,
        bundleId: versionApp.bundleId,
        versionId: id,
      });
      if (response.version) {
        setVersionDetails(response.version);
        setVersions((all) => all.map((item) => (item.id === id ? { ...item, ...response.version } : item)));
      }
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function download(app: StoreApp, selectedVersion = "") {
    setBusy("download");
    setNotice(null);
    try {
      if (!active) throw new Error("Add an Apple account to download apps.");
      const task = await postJSON<Task>("/api/store/download", {
        clientId: owner,
        session: active.session,
        appId: app.id,
        bundleId: app.bundleId,
        versionId: selectedVersion || undefined,
        displayName: app.name,
        version: selectedVersion
          ? versionDetails?.version || versions.find((item) => item.id === selectedVersion)?.version
          : app.version,
      });
      const mapping = readLocal<Record<string, string>>("signtools.tasks.accounts", {});
      mapping[task.id] = active.id;
      writeLocal("signtools.tasks.accounts", mapping);
      setTasks((all) => [task, ...all.filter((item) => item.id !== task.id)]);
      setVersionApp(null);
      setNotice({ kind: "success", text: "Download started. Its encryption state and IPA will appear below." });
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function purchase() {
    if (!purchaseApp) return;
    setBusy("purchase");
    setNotice(null);
    try {
      await storeCall("purchase", { appId: purchaseApp.id, bundleId: purchaseApp.bundleId });
      setPurchaseApp(null);
      setNotice({ kind: "success", text: "Purchase completed. You can now download the app." });
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function importModels(files: FileList | null) {
    if (!files?.length) return;
    setBusy("models");
    setNotice(null);
    try {
      if (!crypto.subtle) throw new Error("Encrypted enrollment models require HTTPS or localhost.");
      for (const file of Array.from(files)) {
        if (file.size > 64 * 1024) throw new Error("An enrollment model must be 64 KB or smaller.");
        const bytes = new Uint8Array(await file.arrayBuffer());
        const data = toBase64(bytes);
        const details = await postJSON<{
          selector?: string;
          name?: string;
          formatVersion?: string | number;
          supported: boolean;
        }>("/api/decrypt/model-info", { model: data });
        const encrypted = await postJSON<{ sealed: string }>("/api/vault/seal", { purpose: "decrypt-model", data });
        bytes.fill(0);
        await saveModel({ id: crypto.randomUUID(), name: file.name, ...details, sealed: encrypted.sealed });
      }
      await refreshVault();
      setNotice({
        kind: "success",
        text: "Models were encrypted by the server and saved in this browser's encrypted vault.",
      });
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
      if (modelInput.current) modelInput.current.value = "";
    }
  }

  async function beginDecrypt(artifact: Artifact) {
    setBusy("decrypt-" + artifact.id);
    setNotice(null);
    try {
      const selected =
        modelId === "auto"
          ? models.filter((model) => model.supported)
          : models.filter((model) => model.id === modelId);
      if (!selected.length) throw new Error("Upload a supported matching decryption model first.");
      const task = await postJSON<Task>("/api/decrypt", {
        clientId: owner,
        artifactId: artifact.id,
        models: selected.map(({ id, name, sealed }) => ({ id, name, sealed })),
        modelId: modelId === "auto" ? undefined : modelId,
        stripAccountData: stripAccount,
      });
      setTasks((all) => [task, ...all.filter((item) => item.id !== task.id)]);
      setNotice({ kind: "success", text: "Decryption started. Open the task log to follow its progress." });
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function exportBackup() {
    setBusy("export");
    setNotice(null);
    try {
      if (passphrase !== confirmPassphrase) throw new Error("The backup passphrases do not match.");
      const bytes = await createBackup(passphrase);
      const href = URL.createObjectURL(new Blob([bytes.slice().buffer as ArrayBuffer], { type: "application/zip" }));
      const anchor = document.createElement("a");
      anchor.href = href;
      anchor.download = "signtools-backup-" + new Date().toISOString().slice(0, 10) + ".json.zip";
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(href), 1000);
      setPassphrase("");
      setConfirmPassphrase("");
      setNotice({ kind: "success", text: "Encrypted backup exported. Keep its passphrase separately." });
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  async function importBackup() {
    if (!backupFile) return;
    setBusy("restore");
    setNotice(null);
    try {
      await restoreBackup(new Uint8Array(await backupFile.arrayBuffer()), restorePassphrase);
      setRestorePassphrase("");
      setRestoreOpen(false);
      window.location.reload();
    } catch (error) {
      fail(error);
    } finally {
      setBusy("");
    }
  }

  const artifactActions = (artifact: Artifact) => (
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="outline" asChild>
        <a href={artifact.downloadUrl}>
          <ArrowDownToLine />
          IPA
        </a>
      </Button>
      {artifact.encryption === "encrypted" ? (
        <Button
          size="sm"
          variant="outline"
          onClick={() => void beginDecrypt(artifact)}
          disabled={busy.startsWith("decrypt-")}
        >
          Decrypt
        </Button>
      ) : (
        <Button size="sm" onClick={() => onSign(artifactSource(artifact))}>
          <KeyRound />
          Sign
        </Button>
      )}
      {onInstall && artifact.encryption !== "encrypted" && (
        <Button size="sm" variant="outline" onClick={() => onInstall(artifact)}>
          <Smartphone />
          Install
        </Button>
      )}
    </div>
  );

  return (
    <div className="space-y-6">
      {notice && (
        <div
          role={notice.kind === "error" ? "alert" : "status"}
          className={
            "flex items-start justify-between gap-3 rounded-lg border p-4 text-sm leading-relaxed " +
            (notice.kind === "error"
              ? "border-red-200 bg-red-50 text-red-800 dark:border-red-900 dark:bg-red-950/40 dark:text-red-300"
              : "border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300")
          }
        >
          <span>{notice.text}</span>
          <button aria-label="Dismiss message" onClick={() => setNotice(null)}>
            <X size={16} />
          </button>
        </div>
      )}

      {page === "store" && (
        <>
          <Panel
            title="Apple accounts"
            detail="Account sessions stay encrypted in this browser. Each account keeps its own App Store country."
            action={
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setChallenge(null);
                  setPassword("");
                  setAuthCode("");
                  setLoginOpen(true);
                }}
              >
                <Plus />
                Add account
              </Button>
            }
          >
            {!accounts.length ? (
              <Empty
                icon={<Apple size={20} />}
                title="Connect an Apple account"
                detail="Sign in to search your storefront, view purchases and download a specific app version."
              />
            ) : (
              <div className="grid items-end gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,180px)_auto]">
                <Field label="Active account">
                  <select
                    aria-label="Active Apple account"
                    className={selectClass}
                    value={active?.id || ""}
                    onChange={(event) => {
                      const id = event.target.value;
                      setActiveId(id);
                      writeLocal("signtools.store.account", id);
                      const account = accounts.find((item) => item.id === id);
                      if (account?.country) {
                        setCountry(account.country);
                        writeLocal("signtools.store.country", account.country);
                      }
                      setResults([]);
                    }}
                  >
                    {accounts.map((account) => (
                      <option key={account.id} value={account.id}>
                        {account.email} · {account.country || account.storefront}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="Search country">
                  <Input
                    aria-label="Search country"
                    list="store-countries"
                    maxLength={2}
                    value={country}
                    onChange={(event) => {
                      const next = event.target.value.toUpperCase();
                      setCountry(next);
                      writeLocal("signtools.store.country", next);
                    }}
                  />
                  <datalist id="store-countries">
                    {storeCountries.map(({ code, name }) => (
                      <option key={code} value={code}>
                        {name}
                      </option>
                    ))}
                  </datalist>
                </Field>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Remove selected Apple account from this browser"
                  onClick={() => {
                    if (active) {
                      removeAccount(active.id);
                      void refreshVault().catch(fail);
                    }
                  }}
                >
                  <Trash2 />
                </Button>
              </div>
            )}
            {info.store && !info.store.available && (
              <p className="mt-3 text-xs text-muted-foreground">
                The App Store adapter is unavailable on this server. Check the server's ipatool configuration.
              </p>
            )}
          </Panel>
          <Panel
            title="Find an app"
            detail={
              active
                ? `Purchases and downloads use ${active.country || "the account's"} storefront; search can browse another country.`
                : "Choose an account above to begin."
            }
          >
            <form
              className="flex flex-wrap gap-2"
              onSubmit={(event) => {
                event.preventDefault();
                void browse("search");
              }}
            >
              <div className="relative min-w-0 basis-full sm:flex-1">
                <Search className="absolute left-3 top-2.5 text-muted-foreground" size={16} />
                <Input
                  aria-label="Search App Store"
                  placeholder="App name or bundle identifier"
                  value={term}
                  onChange={(event) => setTerm(event.target.value)}
                  className="pl-9"
                />
              </div>
              <Button type="submit" disabled={!active || !!busy || !term.trim()}>
                {busy === "browse" ? <Loader2 className="animate-spin" /> : <Search />}Search
              </Button>
              <Button
                type="button"
                variant="outline"
                disabled={!active || !!busy}
                onClick={() => void browse("purchases")}
              >
                My purchases
              </Button>
            </form>
            {!!results.length ? (
              <>
                <div className="mt-5 divide-y rounded-lg border">
                  {results.map((app) => (
                    <div key={app.id} className="flex flex-wrap items-center justify-between gap-3 p-4">
                      <div className="flex min-w-0 flex-1 items-center gap-3">
                        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border bg-secondary">
                          <Package size={19} className="text-muted-foreground" />
                        </div>
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium">{app.name}</p>
                          <p className="mt-1 truncate text-[11px] text-muted-foreground">
                            {app.bundleId} {app.version ? `· ${app.version}` : ""}
                          </p>
                          <p className="mt-1 text-[11px] text-muted-foreground">
                            {app.price === 0 || app.price === "0" ? "Free" : app.price || ""}
                            {app.purchaseDate ? ` · Purchased ${app.purchaseDate}` : ""}
                          </p>
                        </div>
                      </div>
                      <div className="flex gap-2">
                        <Button size="sm" variant="outline" disabled={!!busy} onClick={() => setPurchaseApp(app)}>
                          Purchase
                        </Button>
                        <Button size="sm" variant="outline" disabled={!!busy} onClick={() => void showVersions(app)}>
                          Versions
                        </Button>
                        <Button size="sm" disabled={!!busy} onClick={() => void download(app)}>
                          <Download />
                          Download
                        </Button>
                      </div>
                    </div>
                  ))}
                </div>
                <div className="mt-4 flex items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>
                    {mode === "purchases" ? "Purchases" : "Search results"} · Page {resultPage}
                    {totalResults ? ` · ${totalResults} apps` : ""}
                  </span>
                  <div className="flex gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={resultPage <= 1 || !!busy}
                      onClick={() => void browse(mode, resultPage - 1)}
                    >
                      Previous
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={results.length < 25 || !!busy}
                      onClick={() => void browse(mode, resultPage + 1)}
                    >
                      Next
                    </Button>
                  </div>
                </div>
              </>
            ) : (
              <Empty
                icon={<Search size={20} />}
                title="A version for every workflow"
                detail="Search for an app, open its available versions, then choose a release to upgrade or downgrade. Purchases only happen when you confirm them."
              />
            )}
          </Panel>
        </>
      )}

      {page === "decrypt" && (
        <>
          <Panel
            title="Decryption models"
            detail="Models are sealed by this server, then encrypted again with your browser key. They are only sent to a decryption job when needed."
            action={
              <>
                <input
                  ref={modelInput}
                  type="file"
                  multiple
                  className="hidden"
                  aria-label="Upload decryption models"
                  onChange={(event) => void importModels(event.target.files)}
                />
                <Button size="sm" variant="outline" disabled={!!busy} onClick={() => modelInput.current?.click()}>
                  {busy === "models" ? <Loader2 className="animate-spin" /> : <Plus />}Add models
                </Button>
              </>
            }
          >
            {!models.length ? (
              <Empty
                icon={<FileKey2 size={21} />}
                title="Bring a matching model"
                detail="Upload one or more enrollment models from your device. Automatic selection matches each IPA's exact encryption selector."
              />
            ) : (
              <div className="space-y-3">
                {models.map((model) => (
                  <div key={model.id} className="flex items-center gap-3 rounded-lg border p-3">
                    <FileKey2 size={18} className="shrink-0 text-primary" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium">{model.name}</p>
                      <p className="mt-1 break-all font-mono text-[10px] text-muted-foreground">
                        {model.selector || "No exact selector"}
                        {model.formatVersion !== undefined ? ` · Format ${model.formatVersion}` : ""}
                      </p>
                    </div>
                    <Badge variant="outline">{model.supported ? "Ready" : "Unsupported"}</Badge>
                    <Button
                      size="icon"
                      variant="ghost"
                      aria-label={"Remove model " + model.name}
                      onClick={() => {
                        removeModel(model.id);
                        if (modelId === model.id) setModelId("auto");
                        void refreshVault().catch(fail);
                      }}
                    >
                      <Trash2 size={15} />
                    </Button>
                  </div>
                ))}
              </div>
            )}
            <div className="mt-5 grid gap-4 sm:grid-cols-2">
              <Field label="Model selection">
                <select className={selectClass} value={modelId} onChange={(event) => setModelId(event.target.value)}>
                  <option value="auto">Automatic · exact selector match</option>
                  {models.map((model) => (
                    <option key={model.id} value={model.id}>
                      {model.name}
                      {model.supported ? "" : " (unsupported)"}
                    </option>
                  ))}
                </select>
              </Field>
              <label className="flex items-center gap-3 self-end py-2 text-xs">
                <input
                  type="checkbox"
                  checked={stripAccount}
                  onChange={(event) => setStripAccount(event.target.checked)}
                />
                Strip account metadata from the decrypted output
              </label>
            </div>
            {info.decrypt && (!info.decrypt.available || info.decrypt.platformSupported === false) && (
              <p className="mt-4 rounded-lg border p-3 text-xs leading-relaxed text-muted-foreground">
                Decryption is unavailable on this server platform. The current upstream engine supports Linux and
                macOS.
              </p>
            )}
          </Panel>
          <Panel
            title="IPAs in your library"
            detail="Encrypted downloads and uploaded files appear here. Outputs keep the upstream verification report."
            action={
              <Button size="sm" variant="ghost" onClick={() => void refreshLibrary().catch(fail)}>
                <RefreshCw />
                Refresh
              </Button>
            }
          >
            {!artifacts.length ? (
              <Empty
                icon={<Package size={21} />}
                title="Your first IPA starts here"
                detail="Download an app from App Store, or upload an IPA from Install only. Then choose a matching model to decrypt it."
              />
            ) : (
              <div className="divide-y">
                {artifacts.map((artifact) => (
                  <div
                    key={artifact.id}
                    className="flex flex-wrap items-center justify-between gap-4 py-4 first:pt-0 last:pb-0"
                  >
                    <div className="min-w-0">
                      <p className="text-sm font-medium">{artifact.displayName || artifact.name}</p>
                      <p className="mt-1 break-all text-xs text-muted-foreground">
                        {artifact.bundleId || artifact.name} · {artifact.version || "Unknown version"}
                        {artifact.buildVersion ? ` (${artifact.buildVersion})` : ""}
                      </p>
                      <div className="mt-2 flex flex-wrap gap-2">
                        <Badge variant="outline">{artifact.encryption}</Badge>
                        <Badge variant="outline">{artifact.source}</Badge>
                      </div>
                      {artifact.limitations?.map((text) => (
                        <p key={text} className="mt-2 max-w-xl text-[11px] leading-relaxed text-muted-foreground">
                          {text}
                        </p>
                      ))}
                    </div>
                    {artifact.encryption === "encrypted" || artifact.encryption === "unknown" ? (
                      <Button
                        size="sm"
                        disabled={!!busy || !models.length || info.decrypt?.platformSupported === false}
                        onClick={() => void beginDecrypt(artifact)}
                      >
                        {busy === "decrypt-" + artifact.id ? <Loader2 className="animate-spin" /> : <LockKeyhole />}
                        Decrypt IPA
                      </Button>
                    ) : (
                      artifactActions(artifact)
                    )}
                  </div>
                ))}
              </div>
            )}
          </Panel>
        </>
      )}

      {page === "backup" && (
        <div className="grid items-start gap-6 lg:grid-cols-2">
          <Panel
            title="Export an encrypted backup"
            detail="One .json.zip file contains your browser key, encrypted Apple accounts, models, repositories and all SignTools preferences."
          >
            <div className="mb-5 flex gap-3 rounded-lg border bg-secondary/50 p-4">
              <ShieldCheck className="mt-0.5 shrink-0 text-primary" size={20} />
              <p className="text-xs leading-6 text-muted-foreground">
                The JSON inside the ZIP is protected with passphrase-derived AES-256-GCM. Your browser key and account
                data never appear as plaintext in the archive.
              </p>
            </div>
            <form
              className="space-y-4"
              onSubmit={(event) => {
                event.preventDefault();
                void exportBackup();
              }}
            >
              <Field label="Backup passphrase">
                <Input
                  type="password"
                  autoComplete="new-password"
                  minLength={10}
                  required
                  value={passphrase}
                  onChange={(event) => setPassphrase(event.target.value)}
                />
              </Field>
              <Field label="Confirm passphrase">
                <Input
                  type="password"
                  autoComplete="new-password"
                  minLength={10}
                  required
                  value={confirmPassphrase}
                  onChange={(event) => setConfirmPassphrase(event.target.value)}
                />
              </Field>
              <Button className="w-full" disabled={!!busy}>
                {busy === "export" ? <Loader2 className="animate-spin" /> : <Download />}Export .json.zip
              </Button>
            </form>
            <p className="mt-4 text-[11px] leading-6 text-muted-foreground">
              Keep the server's extra_conf.enc_key as well. Existing sealed sessions and models require the same server
              key after a server move.
            </p>
          </Panel>
          <Panel
            title="Restore to this browser"
            detail="Import a previous .json.zip backup with its passphrase. Other websites and browser data are unaffected."
          >
            <div className="space-y-4">
              <Field label="Encrypted backup">
                <Input
                  type="file"
                  accept=".zip,application/zip"
                  onChange={(event) => setBackupFile(event.target.files?.[0] || null)}
                />
              </Field>
              <Field label="Backup passphrase">
                <Input
                  type="password"
                  autoComplete="current-password"
                  value={restorePassphrase}
                  onChange={(event) => setRestorePassphrase(event.target.value)}
                />
              </Field>
              <Button
                variant="outline"
                className="w-full"
                disabled={!backupFile || restorePassphrase.length < 10 || !!busy}
                onClick={() => setRestoreOpen(true)}
              >
                <Upload />
                Restore backup
              </Button>
              <p className="text-[11px] leading-6 text-muted-foreground">
                Restore replaces this browser's SignTools vault and preferences, including its encryption key. A wrong
                passphrase leaves current data untouched.
              </p>
            </div>
          </Panel>
        </div>
      )}

      {(page === "store" || page === "decrypt") && (
        <Panel
          title="Tasks"
          detail="Downloads and decryption keep running on the server. Expand a task to inspect its log."
          action={
            <Badge variant="outline">
              {tasks.filter((task) => task.state === "running" || task.state === "queued").length} active
            </Badge>
          }
        >
          {!tasks.length ? (
            <p className="py-3 text-xs text-muted-foreground">Your downloads and decryption tasks will appear here.</p>
          ) : (
            <div className="space-y-3">
              {tasks.slice(0, 25).map((task) => (
                <div key={task.id} className="overflow-hidden rounded-lg border">
                  <div className="flex flex-wrap items-start justify-between gap-3 p-4">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <Terminal size={15} className="text-muted-foreground" />
                        <p className="truncate text-sm font-medium">{task.title}</p>
                      </div>
                      <div className="mt-2 flex items-center gap-2">
                        <Badge variant="outline">{task.state}</Badge>
                        {(task.state === "running" || task.state === "queued") && (
                          <span className="text-[11px] text-muted-foreground">
                            {Math.round(Math.min(100, Math.max(0, task.progress)))}%
                          </span>
                        )}
                      </div>
                      {task.error && (
                        <p
                          role="alert"
                          className="mt-3 break-words text-xs leading-relaxed text-red-600 dark:text-red-400"
                        >
                          {task.error}
                        </p>
                      )}
                    </div>
                    {(task.state === "running" || task.state === "queued") && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() =>
                          void postJSON("/api/tasks/" + encodeURIComponent(task.id) + "/cancel", {
                            clientId: owner,
                          }).catch(fail)
                        }
                      >
                        Cancel
                      </Button>
                    )}
                    {task.state === "succeeded" && task.result?.artifact && (
                      <div className="space-y-2">
                        <Badge variant="outline">{task.result.artifact.encryption}</Badge>
                        {artifactActions(task.result.artifact)}
                      </div>
                    )}
                  </div>
                  <details className="border-t bg-secondary/30">
                    <summary className="flex items-center gap-2 px-4 py-2.5 text-[11px] text-muted-foreground">
                      <ChevronRight size={12} />
                      Task log {task.logs?.length ? `(${task.logs.length})` : ""}
                    </summary>
                    <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words px-4 pb-4 font-mono text-[11px] leading-6">
                      {(task.logs || []).join("\n") || "Waiting for task output…"}
                      {task.result?.report
                        ? "\n\nVerification report\n" + JSON.stringify(task.result.report, null, 2)
                        : ""}
                    </pre>
                  </details>
                </div>
              ))}
            </div>
          )}
        </Panel>
      )}

      <Dialog
        open={loginOpen}
        onOpenChange={(open) => {
          setLoginOpen(open);
          if (!open) {
            setPassword("");
            setAuthCode("");
            setChallenge(null);
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{challenge ? "Verify your Apple account" : "Add an Apple account"}</DialogTitle>
            <DialogDescription>
              {challenge
                ? "Enter the verification code from your trusted Apple device."
                : "Your session is sealed by this server and encrypted again in this browser. No operating-system keychain is used."}
            </DialogDescription>
          </DialogHeader>
          <DialogError notice={notice} />
          <form className="space-y-4" onSubmit={(event) => void logIn(event)}>
            <Field label="Apple account email">
              <Input
                type="email"
                autoComplete="username"
                required
                readOnly={!!challenge}
                value={email}
                onChange={(event) => setEmail(event.target.value)}
              />
            </Field>
            {challenge ? (
              <Field label="Verification code">
                <Input
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  required
                  value={authCode}
                  onChange={(event) => setAuthCode(event.target.value.replace(/\s/g, ""))}
                  autoFocus
                />
              </Field>
            ) : (
              <Field label="Password">
                <Input
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                />
              </Field>
            )}
            <Button className="w-full" disabled={!!busy || !owner}>
              {busy === "login" ? <Loader2 className="animate-spin" /> : <Apple />}
              {challenge ? "Verify account" : "Sign in"}
            </Button>
          </form>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!purchaseApp}
        onOpenChange={(open) => {
          if (!open) setPurchaseApp(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm App Store purchase</DialogTitle>
            <DialogDescription>
              Claim a free app license on your Apple account. Paid purchases must be completed in Apple's App Store;
              downloading paid apps you already own is supported.
            </DialogDescription>
          </DialogHeader>
          <DialogError notice={notice} />
          <div className="rounded-lg border bg-secondary/30 p-4">
            <p className="font-medium">{purchaseApp?.name}</p>
            <p className="mt-2 text-sm">
              Listed price:{" "}
              {purchaseApp?.price === 0 || purchaseApp?.price === "0"
                ? "Free"
                : (purchaseApp?.price ?? "Check your account storefront")}
            </p>
            <p className="mt-2 break-all text-xs text-muted-foreground">
              {active?.email} · {active?.country}
            </p>
          </div>
          <Button disabled={!!busy || Number(purchaseApp?.price || 0) > 0} onClick={() => void purchase()}>
            {busy === "purchase" && <Loader2 className="animate-spin" />}Confirm purchase
          </Button>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!versionApp}
        onOpenChange={(open) => {
          if (!open) setVersionApp(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{versionApp?.name} versions</DialogTitle>
            <DialogDescription>
              Choose a particular release to upgrade or downgrade. Version details come from App Store metadata.
            </DialogDescription>
          </DialogHeader>
          <DialogError notice={notice} />
          <Field label="Version">
            <select
              className={selectClass}
              value={versionId}
              onChange={(event) => void showVersionMetadata(event.target.value)}
            >
              <option value="">Latest available {versionApp?.version ? `· ${versionApp.version}` : ""}</option>
              {versions.map((version) => (
                <option key={version.id} value={version.id}>
                  {version.version || version.name || "Version ID " + version.id}
                  {version.version && version.name && version.name !== version.version ? " · " + version.name : ""}
                </option>
              ))}
            </select>
          </Field>
          {busy === "versions" || busy === "metadata" ? (
            <p className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 size={14} className="animate-spin" />
              Loading version metadata…
            </p>
          ) : (
            versionDetails && (
              <div className="rounded-lg border bg-secondary/30 p-4 text-xs leading-6">
                <p className="font-medium">
                  {versionDetails.name || versionApp?.name} · {versionDetails.version || "Unknown version"}
                </p>
                <p className="text-muted-foreground">
                  Version ID {versionDetails.id}
                  {versionDetails.date ? ` · ${versionDetails.date}` : ""}
                </p>
              </div>
            )
          )}
          <Button
            disabled={!!busy || !versionApp}
            onClick={() => {
              if (versionApp) void download(versionApp, versionId);
            }}
          >
            <Download />
            Download selected version
          </Button>
        </DialogContent>
      </Dialog>
      <Dialog open={restoreOpen} onOpenChange={setRestoreOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Replace this browser's SignTools data?</DialogTitle>
            <DialogDescription>
              The backup replaces your accounts, models, browser encryption key, repositories and preferences. The
              workspace reloads after a successful restore.
            </DialogDescription>
          </DialogHeader>
          <DialogError notice={notice} />
          <Button disabled={!!busy} onClick={() => void importBackup()}>
            {busy === "restore" ? <Loader2 className="animate-spin" /> : <Upload />}Restore encrypted backup
          </Button>
        </DialogContent>
      </Dialog>
    </div>
  );
}
