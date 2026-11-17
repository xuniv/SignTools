export async function request<T = unknown>(url: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(url, {
    credentials: "same-origin",
    redirect: "manual",
    ...options,
    headers: { "X-Requested-With": "fetch", ...options.headers },
  });
  if (response.type === "opaqueredirect" || response.status === 401)
    throw new Error("Your session expired. Open the login page and try again.");
  if (!response.ok) {
    let message = await response.text();
    try {
      message = JSON.parse(message).message || message;
    } catch {}
    throw new Error(message || `Request failed (${response.status})`);
  }
  if (response.status === 204) return undefined as T;
  return response.headers.get("content-type")?.includes("application/json")
    ? (response.json() as Promise<T>)
    : ((await response.text()) as T);
}
export function postJSON<T = unknown>(url: string, data: unknown) {
  return request<T>(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
}
