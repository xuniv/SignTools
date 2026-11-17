import { postJSON, request } from "./api";
export function isInstalled() {
  return (
    matchMedia("(display-mode: standalone)").matches ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true
  );
}
export async function registerWorker() {
  if ("serviceWorker" in navigator && window.isSecureContext)
    return navigator.serviceWorker.register("/sw.js", { scope: "/" });
  return null;
}
function decodeKey(key: string) {
  const raw = atob(key.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (key.length % 4)) % 4));
  return Uint8Array.from(raw, (char) => char.charCodeAt(0));
}
export async function syncPush(appIds: string[]) {
  if (
    !("serviceWorker" in navigator) ||
    !("PushManager" in window) ||
    !isInstalled() ||
    !("Notification" in window) ||
    Notification.permission !== "granted"
  )
    return;
  const registration = await registerWorker();
  const subscription = await registration?.pushManager.getSubscription();
  if (subscription && appIds.length)
    await postJSON("/api/push/subscribe", { subscription: subscription.toJSON(), appIds });
}
export async function enablePush(appIds: string[]) {
  if (!isInstalled()) throw new Error("Add SignTools to your Home Screen first, then open the installed app.");
  if (!("PushManager" in window) || !("Notification" in window))
    throw new Error("Web Push is unavailable in this browser. Use an installed app on iOS 16.4 or later.");
  const permissionPromise = Notification.requestPermission();
  const keyPromise = request<{ enabled: boolean; publicKey: string }>("/api/push/key");
  const [permission, key] = await Promise.all([permissionPromise, keyPromise]);
  if (!key.enabled) throw new Error("Push notifications are not enabled on this server.");
  if (permission !== "granted")
    throw new Error("Notifications were not allowed. You can change this in your device settings.");
  const registration = await registerWorker();
  if (!registration) throw new Error("A secure HTTPS connection is required.");
  await navigator.serviceWorker.ready;
  const subscription =
    (await registration.pushManager.getSubscription()) ??
    (await registration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: decodeKey(key.publicKey),
    }));
  await postJSON("/api/push/subscribe", { subscription: subscription.toJSON(), appIds });
}
export async function disablePush() {
  if (!("serviceWorker" in navigator)) return;
  const registration = await navigator.serviceWorker.getRegistration("/");
  const subscription = await registration?.pushManager.getSubscription();
  if (subscription) {
    await request("/api/push/subscribe", {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ endpoint: subscription.endpoint }),
    });
    await subscription.unsubscribe();
  }
}
