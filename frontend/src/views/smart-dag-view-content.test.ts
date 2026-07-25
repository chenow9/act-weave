import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const smartDagView = readFileSync(resolve(currentDir, "SmartDagView.vue"), "utf8");

describe("smart dag view static content", () => {
  it("uses the intelligent orchestration reference canvas layout and scoped visual classes", () => {
    expect(smartDagView).toContain("smart-orchestration-page");
    expect(smartDagView).toContain("smart-blueprint-toolbar");
    expect(smartDagView).toContain("grid-matrix-bg");
    expect(smartDagView).toContain("canvas-grabbable");
    expect(smartDagView).toContain("smart-copilot-panel");
    expect(smartDagView).toContain("AI Copilot");
    expect(smartDagView).toContain("smart-property-panel");
    expect(smartDagView).toContain("属性面板");
    expect(smartDagView).toContain("smart-canvas-node");
    expect(smartDagView).toContain("connection-path");
    expect(smartDagView).toContain("smart-zoom-dock");
    expect(smartDagView).toContain("blueprint-picker-modal");
    expect(smartDagView).toContain("smart-status-filter");
    expect(smartDagView).toContain("smart-trial-modal");
    expect(smartDagView).toContain("智能编排模拟试运行");
    expect(smartDagView).toContain("<style scoped>");
    expect(smartDagView).not.toContain("smart-grid");
    expect(smartDagView).not.toContain("smart-console-hero");
    expect(smartDagView).not.toContain("smart-input-panel");
  });

  it("keeps Smart DAG generation and Workflow Draft handoff behavior available", () => {
    expect(smartDagView).toContain("smart.sendTurn");
    expect(smartDagView).toContain("finishGeneration");
    expect(smartDagView).toContain("workflowStore.saveWorkflowDraft");
    expect(smartDagView).toContain('router.push({ name: "workflow", query: { edit: workflow.id } })');
    expect(smartDagView).toContain("smart.generatedWorkflow");
    expect(smartDagView).toContain("只保存 Workflow Draft");
    expect(smartDagView).not.toContain("createAiDraftFromPrompt");
  });

  it("exposes manual format-canvas using the shared workflow auto-layout", () => {
    expect(smartDagView).toContain("applyAutoLayout");
    expect(smartDagView).toContain("autoLayoutWorkflowGraph");
    expect(smartDagView).toContain("格式化画布");
    expect(smartDagView).toContain('data-action="auto-layout-smart-canvas"');
  });

  it("requires workspace + agent and multi-turn generate session UI (P1.5)", () => {
    expect(smartDagView).toContain("selectedAgentId");
    expect(smartDagView).toContain("agentHasUsableModel");
    expect(smartDagView).toContain("canSendGenerateTurn");
    expect(smartDagView).toContain("turnHistory");
    expect(smartDagView).toContain("smart-turn-history");
    expect(smartDagView).toContain("完成生成");
    expect(smartDagView).toContain("smart.closeSession");
    expect(smartDagView).toContain("canvasRenderKey");
    expect(smartDagView).toContain("未配置可用模型");
    expect(smartDagView).toContain("多轮智能生成");
  });

  // D16 / P1.2.3: Console SmartDag must not expose a user System Prompt editor.
  it("does not provide a user-editable System Prompt control for generation", () => {
    expect(smartDagView).not.toMatch(/systemPrompt|system-prompt|System Prompt/i);
    expect(smartDagView).not.toContain("v-model=\"systemPrompt\"");
  });

  it("does not locally fake publish state from the Smart DAG canvas", () => {
    expect(smartDagView).toContain("function publishWorkflow()");
    expect(smartDagView).toContain("workflowStore.publishWorkflow");
    expect(smartDagView).toContain("readiness.canPublish");
    expect(smartDagView).not.toContain('currentBlueprint.value.status = "published"');
    expect(smartDagView).not.toContain("智能编排已成功发布上线");
  });

  // P3.2 / P3.5: publish+bind productization and wizard copy (D12).
  it("productizes post-publish bind to generate-session agent and states publish+bind requirement", () => {
    expect(smartDagView).toContain("bindPublishedWorkflowToSessionAgent");
    expect(smartDagView).toContain("agentStore.bindCapability");
    expect(smartDagView).toContain("versionPolicy: \"FOLLOW_ACTIVE\"");
    expect(smartDagView).toContain("smart-publish-bind-hint");
    expect(smartDagView).toContain("生成满意 ≠ Agent 已可用");
    expect(smartDagView).toContain("正式 binding 仅在 publish 之后");
    expect(smartDagView).toContain("生成满意不等于 Agent 已可用");
  });

  // P4.3 / D14: failure feedback seed from workflow CTA; draft-only revise.
  it("seeds failure feedback revise from route query and passes feedback on turn", () => {
    expect(smartDagView).toContain("applyReviseQuerySeed");
    expect(smartDagView).toContain("pendingFailureFeedback");
    expect(smartDagView).toContain("feedback: pendingFailureFeedback.value");
    expect(smartDagView).toContain("reviseSource");
    expect(smartDagView).toContain("不自动发布");
    expect(smartDagView).toContain("ensureSession({ workflowId })");
  });

  it("keeps modal, toast, and segmented filter accessibility hooks in place", () => {
    expect(smartDagView).toContain('role="status"');
    expect(smartDagView).toContain('aria-live="polite"');
    expect(smartDagView).toContain("@click=\"closeToast\"");
    expect(smartDagView).toContain('role="dialog"');
    expect(smartDagView).toContain('aria-modal="true"');
    expect(smartDagView).toContain("@keydown=\"handleBlueprintModalKeydown\"");
    expect(smartDagView).toContain("@keydown=\"handleSandboxModalKeydown\"");
    expect(smartDagView).toContain('role="radiogroup"');
    expect(smartDagView).toContain('role="radio"');
    expect(smartDagView).toContain(":aria-checked=");
  });

  it("keeps audited click targets at the 44px interaction baseline", () => {
    expect(smartDagView).toContain("min-height: 44px");
    expect(smartDagView).toContain("min-width: 44px");
    expect(smartDagView).toContain(".smart-screen-warning");
    expect(smartDagView).toContain(":disabled=\"!canSendGenerateTurn || !copilotPrompt.trim()\"");
  });

  it("supports panning the canvas by dragging the empty grid", () => {
    expect(smartDagView).toContain("canvasPanning");
    expect(smartDagView).toContain("startCanvasPan");
    expect(smartDagView).toContain("moveCanvasPan");
    expect(smartDagView).toContain("endCanvasPan");
    expect(smartDagView).toContain("@pointerdown=\"startCanvasPan\"");
    expect(smartDagView).toContain("@pointermove=\"moveCanvasPan\"");
    expect(smartDagView).toContain("@pointerup=\"endCanvasPan\"");
    expect(smartDagView).toContain("@pointercancel=\"endCanvasPan\"");
  });

  it("lets users shrink the floating blueprint toolbar", () => {
    expect(smartDagView).toContain("blueprintToolbarCompact");
    expect(smartDagView).toContain("smart-blueprint-toolbar-compact-button");
    expect(smartDagView).toContain("smart-blueprint-toolbar-restore-button");
    expect(smartDagView).toContain("smart-blueprint-toolbar-compact-actions");
    expect(smartDagView).toContain("is-compact");
    expect(smartDagView).toContain("缩小蓝图工具栏");
    expect(smartDagView).toContain("展开蓝图工具栏");
    expect(smartDagView).toContain(">收起<");
  });

  it("fits the graph into the visible canvas safe area instead of hiding it under panels", () => {
    expect(smartDagView).toContain("canvasContainerRef");
    expect(smartDagView).toContain("calculateGraphBounds");
    expect(smartDagView).toContain("fitCanvasToVisibleArea");
    expect(smartDagView).toContain("Math.max(0.35");
    expect(smartDagView).toContain("requestAnimationFrame(fitCanvasToVisibleArea)");
    expect(smartDagView).toContain("handleViewportResize");
    expect(smartDagView).toContain('window.addEventListener("resize", handleViewportResize)');
  });

  it("uses explicit desktop-only handling below the supported width", () => {
    expect(smartDagView).toContain("smart-narrow-blocker");
    expect(smartDagView).toContain("当前宽度不支持编辑");
    expect(smartDagView).toContain("请切换到 1180px 以上桌面窗口");
    expect(smartDagView).toContain("canvasDesktopMinWidth = 1180");
    expect(smartDagView).toContain("window.innerWidth <= canvasDesktopMinWidth");
    expect(smartDagView).toContain("@media (max-width: 1180px)");
    expect(smartDagView).toContain(":inert=\"isNarrowViewport ? true : undefined\"");
    expect(smartDagView).toContain("pointer-events: none");
  });

  it("uses backend compilation and readiness instead of local validation success", () => {
    expect(smartDagView).toContain("workflowStore.validateWorkflow");
    expect(smartDagView).toContain("workflowStore.trialRunWorkflow");
    expect(smartDagView).toContain("以 Workflow v1 Compiler 和 Readiness 返回结果为准");
    expect(smartDagView).not.toContain("本地结构检查通过");
  });

  it("validates sandbox JSON inline before running the trial modal", () => {
    expect(smartDagView).toContain("sandboxError");
    expect(smartDagView).toContain("runSandboxTrial");
    expect(smartDagView).toContain("JSON.parse");
    expect(smartDagView).toContain('role="alert"');
    expect(smartDagView).toContain("JSON 格式错误");
  });

  it("keeps canvas operation hints and fit action discoverable", () => {
    expect(smartDagView).toContain("smart-canvas-hint");
    expect(smartDagView).toContain("拖动画布");
    expect(smartDagView).toContain("滚轮缩放");
    expect(smartDagView).toContain("适配画布");
  });
});
