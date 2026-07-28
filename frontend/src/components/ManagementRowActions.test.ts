import { mount } from "@vue/test-utils";
import { afterEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";

import ManagementRowActions, { type ManagementRowAction } from "./ManagementRowActions.vue";

const primaryActions: ManagementRowAction[] = [
  { key: "test", label: "测试连接", icon: "fa-solid fa-plug-circle-bolt", tone: "primary" },
  { key: "edit", label: "编辑配置", icon: "fa-solid fa-pen-to-square" },
  { key: "delete", label: "删除配置", icon: "fa-solid fa-trash-can", tone: "danger" },
];

const menuActions: ManagementRowAction[] = [
  { key: "publish", label: "发布工具", icon: "fa-solid fa-paper-plane", tone: "primary" },
  {
    key: "delete",
    label: "删除工具",
    icon: "fa-solid fa-trash-can",
    tone: "danger",
    disabled: true,
    disabledReason: "默认工具不能删除",
  },
];

const mountedWrappers: Array<{ unmount: () => void }> = [];

function trackWrapper<Wrapper extends { unmount: () => void }>(wrapper: Wrapper) {
  mountedWrappers.push(wrapper);
  return wrapper;
}

function mountAttached(actions = menuActions) {
  const host = document.createElement("div");
  host.style.overflow = "hidden";
  document.body.append(host);
  const wrapper = trackWrapper(
    mount(ManagementRowActions, {
      attachTo: host,
      props: { primaryActions, menuActions: actions },
    }),
  );
  return { host, wrapper };
}

function actionButton(wrapper: ReturnType<typeof mount>, key: string) {
  return wrapper.get<HTMLButtonElement>(`button[data-action-key="${key}"]`);
}

afterEach(() => {
  mountedWrappers.splice(0).forEach((wrapper) => wrapper.unmount());
  document.body.innerHTML = "";
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("ManagementRowActions", () => {
  it("blocks disabled and loading actions while keeping useful status tooltips", async () => {
    const wrapper = trackWrapper(
      mount(ManagementRowActions, {
        props: {
          primaryActions: [
            { ...primaryActions[0], disabled: true, disabledReason: "已有配置正在测试" },
            { ...primaryActions[1], loading: true },
          ],
        },
      }),
    );
    const disabled = actionButton(wrapper, "test");
    const loading = actionButton(wrapper, "edit");

    expect(disabled.attributes("disabled")).toBeDefined();
    expect(disabled.attributes("title")).toBe("已有配置正在测试");
    expect(loading.attributes("disabled")).toBeDefined();
    expect(loading.attributes("aria-busy")).toBe("true");
    expect(loading.get("i").classes()).toEqual(expect.arrayContaining(["fa-spinner", "fa-spin"]));

    await disabled.trigger("click");
    await loading.trigger("click");
    expect(wrapper.emitted("action")).toBeUndefined();
  });

  it("shortens visible action labels to four characters while preserving the full accessible label", () => {
    const wrapper = trackWrapper(
      mount(ManagementRowActions, {
        props: {
          primaryActions: [
            { key: "workspace", label: "进入业务空间控制台", shortLabel: "进入空间", icon: "fa-solid fa-arrow-right" },
          ],
        },
      }),
    );

    const button = actionButton(wrapper, "workspace");
    expect(button.get("span").text()).toBe("进入空间");
    expect(button.attributes("aria-label")).toBe("进入业务空间控制台");
    expect(button.attributes("title")).toBe("进入业务空间控制台");
  });

  it("promotes one low-frequency action into the third direct slot when primary actions are present", () => {
    const wrapper = trackWrapper(
      mount(ManagementRowActions, {
        props: {
          primaryActions: primaryActions.slice(0, 2),
          menuActions: [menuActions[1]],
        },
      }),
    );

    expect(
      wrapper.findAll('button[data-action-kind="primary"]').map((action) => action.attributes("data-action-key")),
    ).toEqual(["test", "edit", "delete"]);
    expect(wrapper.find('button[aria-label="更多操作"]').exists()).toBe(false);
    expect(actionButton(wrapper, "delete").attributes("disabled")).toBeDefined();
    expect(actionButton(wrapper, "delete").attributes("title")).toBe("默认工具不能删除");
  });

  it("keeps a sole menu action inside the overflow menu when primaryActions is empty", async () => {
    const wrapper = trackWrapper(
      mount(ManagementRowActions, {
        props: {
          primaryActions: [],
          menuActions: [menuActions[1]],
        },
      }),
    );

    expect(wrapper.findAll('button[data-action-kind="primary"]')).toHaveLength(0);
    const trigger = wrapper.get<HTMLButtonElement>('button[aria-label="更多操作"]');
    expect(trigger.exists()).toBe(true);
    expect(wrapper.classes()).toContain("is-menu-only");

    await trigger.trigger("click");
    await nextTick();
    const menu = document.body.querySelector<HTMLElement>('[role="menu"][aria-label="更多操作"]');
    expect(menu).not.toBeNull();
    expect(menu!.querySelectorAll('[role="menuitem"]')).toHaveLength(1);
    expect(menu!.querySelector('button[data-action-key="delete"]')).not.toBeNull();
  });

  it("reserves the third slot for an accessible overflow trigger", async () => {
    const wrapper = trackWrapper(mount(ManagementRowActions, { props: { primaryActions, menuActions } }));
    const trigger = wrapper.get<HTMLButtonElement>('button[aria-label="更多操作"]');

    expect(wrapper.findAll('button[data-action-kind="primary"]')).toHaveLength(2);
    expect(wrapper.find('button[data-action-key="delete"]').exists()).toBe(false);
    expect(trigger.attributes("title")).toBe("更多操作");
    expect(trigger.attributes("aria-haspopup")).toBe("menu");
    expect(trigger.attributes("aria-expanded")).toBe("false");
    expect(trigger.get("span").text()).toBe("更多");

    await trigger.trigger("click");
    expect(trigger.attributes("aria-expanded")).toBe("true");
    expect(document.body.querySelector(`#${trigger.attributes("aria-controls")}`)).not.toBeNull();
  });

  it("teleports a semantic menu outside clipped hosts and closes after an enabled action", async () => {
    const { host, wrapper } = mountAttached();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await nextTick();

    const menu = document.body.querySelector<HTMLElement>('[role="menu"][aria-label="更多操作"]');
    expect(menu).not.toBeNull();
    expect(host.contains(menu)).toBe(false);
    expect(menu!.style.position).toBe("fixed");
    expect(menu!.querySelectorAll('[role="menuitem"]')).toHaveLength(2);

    const disabledItem = menu!.querySelector<HTMLButtonElement>('button[data-action-key="delete"]');
    expect(disabledItem?.getAttribute("aria-disabled")).toBe("true");
    expect(disabledItem?.title).toBe("默认工具不能删除");

    menu!.querySelector<HTMLButtonElement>('button[data-action-key="publish"]')!.click();
    await nextTick();
    expect(wrapper.emitted("action")).toEqual([["publish"]]);
    expect(document.body.querySelector('[role="menu"][aria-label="更多操作"]')).toBeNull();
  });

  it("closes on Escape and restores focus to the overflow trigger", async () => {
    const { wrapper } = mountAttached();
    const trigger = wrapper.get<HTMLButtonElement>('button[aria-label="更多操作"]');
    await trigger.trigger("click");
    await nextTick();
    document.body.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus();

    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await nextTick();

    expect(document.body.querySelector('[role="menu"][aria-label="更多操作"]')).toBeNull();
    expect(document.activeElement).toBe(trigger.element);
  });

  it("closes when a pointer press occurs outside the trigger and teleported menu", async () => {
    const { wrapper } = mountAttached();
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    const outside = document.createElement("button");
    document.body.append(outside);

    outside.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    await nextTick();

    expect(document.body.querySelector('[role="menu"][aria-label="更多操作"]')).toBeNull();
  });

  it("clamps fixed positioning, flips upward, and repositions on resize and scroll", async () => {
    vi.stubGlobal("innerWidth", 300);
    vi.stubGlobal("innerHeight", 260);
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(208);
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(120);
    const { wrapper } = mountAttached();
    const trigger = wrapper.get<HTMLButtonElement>('button[aria-label="更多操作"]');
    let rect = { top: 20, bottom: 64, left: 251, right: 295, width: 44, height: 44, x: 251, y: 20, toJSON: () => ({}) };
    const rectSpy = vi.spyOn(trigger.element, "getBoundingClientRect").mockImplementation(() => rect as DOMRect);

    await trigger.trigger("click");
    await nextTick();
    const menu = document.body.querySelector<HTMLElement>('[role="menu"][aria-label="更多操作"]')!;
    expect(menu.style.left).toBe("84px");
    expect(menu.style.top).toBe("72px");
    expect(menu.dataset.placement).toBe("bottom");

    rect = { ...rect, top: 210, bottom: 254, left: 6, right: 50, x: 6, y: 210 };
    window.dispatchEvent(new Event("resize"));
    await nextTick();
    await nextTick();
    expect(menu.style.left).toBe("8px");
    expect(menu.style.top).toBe("82px");
    expect(menu.dataset.placement).toBe("top");

    rect = { ...rect, top: 56, bottom: 100, left: 116, right: 160, x: 116, y: 56 };
    document.dispatchEvent(new Event("scroll", { bubbles: true }));
    await nextTick();
    await nextTick();
    expect(menu.style.top).toBe("108px");
    expect(menu.dataset.placement).toBe("bottom");
    expect(rectSpy).toHaveBeenCalledTimes(3);
  });

  it("uses the larger fallback side and caps menu height to preserve the trigger gap", async () => {
    vi.stubGlobal("innerWidth", 320);
    vi.stubGlobal("innerHeight", 260);
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(208);
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(220);
    const { wrapper } = mountAttached();
    const trigger = wrapper.get<HTMLButtonElement>('button[aria-label="更多操作"]');
    vi.spyOn(trigger.element, "getBoundingClientRect").mockReturnValue({
      top: 190,
      bottom: 234,
      left: 138,
      right: 182,
      width: 44,
      height: 44,
      x: 138,
      y: 190,
      toJSON: () => ({}),
    } as DOMRect);

    await trigger.trigger("click");
    await nextTick();
    const menu = document.body.querySelector<HTMLElement>('[role="menu"][aria-label="更多操作"]')!;

    expect(menu.dataset.placement).toBe("top");
    expect(menu.style.top).toBe("8px");
    expect(menu.style.maxHeight).toBe("174px");
    expect(Number.parseInt(menu.style.top) + Number.parseInt(menu.style.maxHeight)).toBeLessThanOrEqual(182);
  });

  it("uses one roving menuitem tabindex and wraps Arrow navigation across enabled actions", async () => {
    const keyboardActions: ManagementRowAction[] = [
      { key: "edit", label: "编辑工具", icon: "fa-solid fa-pen-to-square" },
      { key: "publish", label: "发布工具", icon: "fa-solid fa-paper-plane", disabled: true },
      { key: "toggle", label: "停用工具", icon: "fa-solid fa-ban" },
      { key: "delete", label: "删除工具", icon: "fa-solid fa-trash-can", tone: "danger" },
    ];
    const { wrapper } = mountAttached(keyboardActions);
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await nextTick();
    const items = Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
    const [editItem, disabledItem, toggleItem, deleteItem] = items;

    expect(items.map((item) => item.tabIndex)).toEqual([0, -1, -1, -1]);
    expect(document.activeElement).toBe(editItem);
    editItem.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    await nextTick();
    expect(document.activeElement).toBe(toggleItem);
    expect(disabledItem.tabIndex).toBe(-1);
    toggleItem.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    await nextTick();
    expect(document.activeElement).toBe(deleteItem);
    deleteItem.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    await nextTick();
    expect(document.activeElement).toBe(editItem);
    editItem.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    await nextTick();
    expect(document.activeElement).toBe(deleteItem);
    expect(items.map((item) => item.tabIndex)).toEqual([-1, -1, -1, 0]);
  });

  it("moves roving menu focus to the first and last enabled actions with Home and End", async () => {
    const keyboardActions: ManagementRowAction[] = [
      { key: "edit", label: "编辑工具", icon: "fa-solid fa-pen-to-square" },
      { key: "publish", label: "发布工具", icon: "fa-solid fa-paper-plane", disabled: true },
      { key: "toggle", label: "停用工具", icon: "fa-solid fa-ban" },
      { key: "delete", label: "删除工具", icon: "fa-solid fa-trash-can", tone: "danger" },
    ];
    const { wrapper } = mountAttached(keyboardActions);
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await nextTick();
    const items = Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));

    items[0].dispatchEvent(new KeyboardEvent("keydown", { key: "End", bubbles: true }));
    await nextTick();
    expect(document.activeElement).toBe(items[3]);
    items[3].dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true }));
    await nextTick();
    expect(document.activeElement).toBe(items[0]);
    expect(items.map((item) => item.tabIndex)).toEqual([0, -1, -1, -1]);
  });

  it("removes document and viewport listeners when unmounted", () => {
    const removeDocumentListener = vi.spyOn(document, "removeEventListener");
    const removeWindowListener = vi.spyOn(window, "removeEventListener");
    const wrapper = mount(ManagementRowActions, { props: { primaryActions, menuActions } });

    wrapper.unmount();

    expect(removeDocumentListener).toHaveBeenCalledWith("pointerdown", expect.any(Function));
    expect(removeDocumentListener).toHaveBeenCalledWith("keydown", expect.any(Function));
    expect(removeDocumentListener).toHaveBeenCalledWith("scroll", expect.any(Function), true);
    expect(removeWindowListener).toHaveBeenCalledWith("resize", expect.any(Function));
  });

  it("shows full shortLabel in the menu without 4-grapheme truncation while aria/title keep full label", async () => {
    const longName = "超长中文服务提供者名称加EnglishSuffix-ABCDEFGHIJKLMNOP";
    const providerMenu: ManagementRowAction[] = [
      { key: "edit", label: `编辑 ${longName}`, shortLabel: "编辑", icon: "fa-solid fa-pen-to-square" },
      { key: "sync", label: `同步 ${longName}`, shortLabel: "同步", icon: "fa-solid fa-rotate", tone: "primary" },
      { key: "assets", label: `查看 ${longName} 的能力资产`, shortLabel: "查看能力资产", icon: "fa-solid fa-cubes" },
      { key: "delete", label: `删除 ${longName}`, shortLabel: "删除", icon: "fa-solid fa-trash-can", tone: "danger" },
    ];
    const { wrapper } = mountAttached(providerMenu);
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await nextTick();

    const items = Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'));
    expect(items.map((item) => item.querySelector("span")?.textContent)).toEqual([
      "编辑",
      "同步",
      "查看能力资产",
      "删除",
    ]);
    expect(items[2].getAttribute("aria-label")).toBe(`查看 ${longName} 的能力资产`);
    expect(items[2].getAttribute("title")).toBe(`查看 ${longName} 的能力资产`);
    expect(items[3].className).toContain("tone-danger");
  });

  it("falls back to full label in the menu when shortLabel is omitted", async () => {
    const { wrapper } = mountAttached([
      { key: "publish", label: "发布工具到生产环境目录", icon: "fa-solid fa-paper-plane", tone: "primary" },
    ]);
    await wrapper.get('button[aria-label="更多操作"]').trigger("click");
    await nextTick();
    const item = document.body.querySelector<HTMLButtonElement>('button[data-action-key="publish"]');
    expect(item?.querySelector("span")?.textContent).toBe("发布工具到生产环境目录");
    expect(item?.getAttribute("aria-label")).toBe("发布工具到生产环境目录");
  });

  it("keeps primary button compact 4-grapheme shortLabel truncation", () => {
    const wrapper = trackWrapper(
      mount(ManagementRowActions, {
        props: {
          primaryActions: [
            {
              key: "assets",
              label: "查看超长对象名称的能力资产",
              shortLabel: "查看能力资产",
              icon: "fa-solid fa-cubes",
            },
          ],
        },
      }),
    );
    const button = actionButton(wrapper, "assets");
    expect(button.get("span").text()).toBe("查看能力");
    expect(button.attributes("aria-label")).toBe("查看超长对象名称的能力资产");
  });
});
