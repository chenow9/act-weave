/**
 * Hover detail for A2UI charts: which category the cursor is over, and the
 * tooltip that says what was measured there.
 *
 * The markup already carries every answer — one target per category, one
 * pre-rendered tooltip card per target — so this module only reveals things and
 * moves the band and guide. Nothing here builds HTML, which keeps chart escaping
 * in the drawing code where the rest of it lives.
 *
 * Listeners live on the document because chat re-renders replace message markup
 * wholesale; anything bound to a figure would be thrown away with it.
 */

/** Widest tooltip plus its gap, per the stylesheet. */
const TIP_SPACE = 232;

let installed = false;

export function installChartHover(): void {
  if (installed || typeof document === "undefined") return;
  installed = true;
  document.addEventListener("pointermove", onPointerMove, { passive: true });
  // A pointer that leaves the window stops sending moves, so the last hovered
  // chart would keep its tooltip.
  document.addEventListener("pointerleave", () => clear(), { passive: true });
}

function onPointerMove(event: PointerEvent): void {
  const target = event.target instanceof Element ? event.target : null;
  const body = target?.closest<HTMLElement>(".a2ui-chart-body");
  if (!body) {
    clear();
    return;
  }
  const hit = target?.closest<SVGElement>("[data-a2ui-hit]");
  if (!hit) {
    clear(body);
    return;
  }
  if (activeBody && activeBody !== body) clear();
  activeBody = body;
  body.classList.add("is-hovering");
  reveal(body, hit);
  place(body, event.clientX);
}

let activeBody: HTMLElement | null = null;

function reveal(body: HTMLElement, hit: SVGElement): void {
  const index = hit.getAttribute("data-a2ui-hit") ?? "";
  for (const shape of body.querySelectorAll<SVGElement>("[data-a2ui-cat].is-active")) {
    if (shape.getAttribute("data-a2ui-cat") !== index) shape.classList.remove("is-active");
  }
  for (const shape of body.querySelectorAll<SVGElement>(`[data-a2ui-cat="${cssEscape(index)}"]`)) {
    shape.classList.add("is-active");
  }
  for (const card of body.querySelectorAll<HTMLElement>(".a2ui-chart-tip.is-active")) {
    if (card.getAttribute("data-a2ui-tip") !== index) card.classList.remove("is-active");
  }
  body.querySelector<HTMLElement>(`.a2ui-chart-tip[data-a2ui-tip="${cssEscape(index)}"]`)?.classList.add("is-active");

  // A band is a rectangle over one category; a slice is its own target and needs
  // no backdrop, so radial charts have nothing to move here.
  const band = body.querySelector<SVGRectElement>(".a2ui-chart-band");
  const guide = body.querySelector<SVGLineElement>(".a2ui-chart-guide");
  const box = hit instanceof SVGRectElement ? hit : null;
  if (band) {
    band.classList.toggle("is-active", !!box);
    if (box) {
      for (const name of ["x", "y", "width", "height"]) band.setAttribute(name, box.getAttribute(name) ?? "0");
    }
  }
  const guideX = hit.getAttribute("data-a2ui-guide");
  if (guide) {
    guide.classList.toggle("is-active", !!guideX && !!box);
    if (guideX && box) {
      const top = Number(box.getAttribute("y") ?? 0);
      const bottom = top + Number(box.getAttribute("height") ?? 0);
      guide.setAttribute("x1", guideX);
      guide.setAttribute("x2", guideX);
      guide.setAttribute("y1", String(top));
      guide.setAttribute("y2", String(bottom));
    }
  }
}

function place(body: HTMLElement, clientX: number): void {
  const card = body.querySelector<HTMLElement>(".a2ui-chart-tip.is-active");
  if (!card) return;
  const box = body.getBoundingClientRect();
  const offset = clientX - box.left;
  card.style.left = `${offset}px`;
  // Where the card would run out of room the tooltip changes sides rather than
  // being clamped, which would make it stop tracking the cursor.
  card.classList.toggle("is-flipped", offset + TIP_SPACE > box.width);
}

function clear(body: HTMLElement | null = activeBody): void {
  if (!body) return;
  body.classList.remove("is-hovering");
  for (const element of body.querySelectorAll(".is-active")) element.classList.remove("is-active");
  if (body === activeBody) activeBody = null;
}

/** Category indexes are digits, but a selector should not trust its input. */
function cssEscape(value: string): string {
  return value.replace(/["\\]/g, "\\$&");
}
