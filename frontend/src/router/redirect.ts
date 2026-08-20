/** Only in-app paths may be used after login. Reject protocol-relative and auth routes. */
export function safePostLoginPath(value: unknown): string | null {
  const raw = Array.isArray(value) ? value[0] : value;
  if (typeof raw !== "string") return null;
  const path = raw.trim();
  if (!path.startsWith("/") || path.startsWith("//") || path.includes("://")) return null;
  if (path === "/login" || path.startsWith("/login?") || path.startsWith("/login/")) return null;
  if (path === "/change-password" || path.startsWith("/change-password?")) return null;
  return path;
}
