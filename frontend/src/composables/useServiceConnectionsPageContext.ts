import { inject } from "vue";

/** Shared inject for Service Connections page panels (ZKL-64 item 11). */
export function useServiceConnectionsPageContext() {
  const scp = inject<any>("scp");
  if (!scp) throw new Error("scp missing");
  return scp;
}
