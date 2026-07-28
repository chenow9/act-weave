import type { App } from "vue";
import VxeUITable from "vxe-table";
import "vxe-table/lib/style.css";

let installed = false;

/** Install VXE once when a schema-table chunk first loads (HIGH-02 ownership). */
export function ensureVxe(app: App) {
  if (installed) return;
  app.use(VxeUITable);
  installed = true;
}
