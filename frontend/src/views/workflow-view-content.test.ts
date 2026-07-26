import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const workflowView = readFileSync(resolve(currentDir, "WorkflowView.vue"), "utf8");
const dataTable = readFileSync(resolve(currentDir, "../components/DataTable.vue"), "utf8");
const managementRowActions = readFileSync(resolve(currentDir, "../components/ManagementRowActions.vue"), "utf8");

describe("workflow view static content", () => {
  const orchestrationTableBlock = workflowView.match(/<ManagementList\s[\s\S]*?<\/ManagementList>/)?.[0] || "";

  it("uses ManagementList for the orchestration dashboard and keeps scoped visual classes", () => {
    expect(workflowView).toContain("ManagementPageHeader");
    expect(workflowView).toContain('title="编排"');
    expect(workflowView).toContain("workflow-orchestration-page");
    expect(workflowView).not.toContain("workflow-view-toggle");
    expect(workflowView).not.toContain("workflow-canvas-select");
    expect(workflowView).toContain("ManagementSummaryStrip");
    expect(workflowView).toContain("workflow-orchestration-table-card");
    expect(workflowView).toContain("<ManagementList");
    expect(workflowView).toContain("ManagementListColumn<WorkflowSummary>");
    expect(workflowView).toContain('storage-key="actweave:workflows:columns"');
    expect(workflowView).not.toContain("workflow-agent-chip");
    expect(workflowView).toContain("workflow-status-badge");
    expect(workflowView).not.toContain("workflowFilterAgentOptions");
    expect(workflowView).toContain('{ key: "workflow", label: "业务流程名"');
    expect(workflowView).toContain('{ key: "workspace", label: "业务空间"');
    expect(workflowView).not.toContain('{ key: "agent", label: "归属 Agent"');
    expect(workflowView).toContain('{ key: "nodes", label: "当前节点数"');
    expect(workflowView).toContain('{ key: "successRate", label: "运行成功率"');
    expect(workflowView).toContain('{ key: "status", label: "当前状态"');
    expect(workflowView).toContain('{ key: "updatedAt", label: "最近修改"');
    expect(workflowView).toContain('{ key: "actions", label: "操作"');
    expect(workflowView).toContain('{ key: "actions", label: "操作", width: 68');
    expect(orchestrationTableBlock).toContain(':rows="workflowStore.pageItems"');
    expect(orchestrationTableBlock).toContain(':pagination="workflowStore.pagination"');
    expect(orchestrationTableBlock).toContain('selection-tone="neutral"');
    expect(orchestrationTableBlock).toContain("ManagementRowActions");
    expect(orchestrationTableBlock).toContain("workflow-workspace-cell");
    expect(workflowView).toContain("<style scoped>");
    expect(workflowView).not.toContain("workflow-table-head");
  });

  it("uses centered detail and metadata modals instead of Element drawers", () => {
    expect(workflowView).toContain("workflowDetailVisible");
    expect(workflowView).toContain("workflowMetadataVisible");
    expect(workflowView).toContain("workflowMetadataMode");
    expect(workflowView).toContain("workflow-detail-modal-card");
    expect(workflowView).toContain("workflow-metadata-modal-card");
    expect(workflowView).toContain("workflow-detail-actions");
    expect(workflowView).toContain("workflow-metadata-actions");
    expect(workflowView).not.toContain("<el-drawer");
    expect(workflowView).not.toContain("workflowDrawerVisible");
    expect(workflowView).not.toContain("workflowDrawerMode");
  });

  it("keeps WorkflowRevisionPanel emit wiring for activate/rollback/compare/disable unchanged", () => {
    expect(workflowView).toContain("<WorkflowRevisionPanel");
    expect(workflowView).toContain('@activate="activateRevision"');
    expect(workflowView).toContain('@rollback="rollbackRevision"');
    expect(workflowView).toContain('@compare="compareRevision"');
    expect(workflowView).toContain('@disable="disableWorkflowRuns"');
    expect(workflowView).toContain(':busy-revision-id="pendingRevisionActionId"');
    expect(workflowView).toContain(':disable-busy="pendingWorkflowDisable || pendingRevisionCompare"');
  });

  it("keeps workflow dialogs in a modal transition and wires keyboard escape handling", () => {
    expect(workflowView).toContain('<Transition name="modal-fade">');
    expect(workflowView).toContain("@keydown.esc.stop.prevent=\"closeWorkflowDetail\"");
    expect(workflowView).toContain("@keydown.esc.stop.prevent=\"closeWorkflowMetadata\"");
    expect(workflowView).toContain("captureWorkflowFocus");
    expect(workflowView).toContain("restoreWorkflowFocus");
  });

  it("keeps row details and the list canvas entry keyboard and assistive-technology friendly", () => {
    expect(workflowView).toContain('@select-row="openWorkflowDetail"');
    expect(workflowView).toContain(':selected-row-key="selectedWorkflow?.id"');
    expect(workflowView).toContain('label: "编辑流程图"');
    expect(workflowView).not.toContain("canvasTargetWorkflow");
    expect(workflowView).not.toContain('aria-label="选择要编辑画布的编排"');
  });

  it("keeps workflow table actions at accessible hit target sizes", () => {
    expect(workflowView).toContain("ManagementRowActions");
    expect(workflowView).not.toContain(".workflow-table-action");
    expect(workflowView).not.toContain(".workflow-icon-action");
    expect(managementRowActions).toContain("width: 44px;");
    expect(managementRowActions).toContain("height: 44px;");
    expect(managementRowActions).toContain("gap: 4px;");
    expect(dataTable).toContain("overflow: hidden;");
    expect(dataTable).toContain("box-shadow: 4px 0 10px -6px rgba(15, 23, 42, 0.16);");
  });

  it("renders workflow feedback as a dismissible live status", () => {
    expect(workflowView).toContain('class="action-toast"');
    expect(workflowView).toContain('role="status"');
    expect(workflowView).toContain('aria-live="polite"');
    expect(workflowView).toContain('aria-label="隐藏提示"');
  });

  // P4.3: compile/trial failure CTA → SmartDag generate session / revise (D14).
  it("exposes revise-from-failure CTA wiring for compile/trial issues", () => {
    expect(workflowView).toContain("reviseDraftFromFailure");
    expect(workflowView).toContain("canReviseFromFailure");
    expect(workflowView).toContain('data-action="revise-draft-from-failure"');
    expect(workflowView).toContain("按问题修订草稿");
    expect(workflowView).toContain('name: "smart-dag"');
    expect(workflowView).toContain("reviseSource");
    expect(workflowView).toContain("feedbackIssues");
    expect(workflowView).toContain('@revise-from-failure="reviseDraftFromFailure"');
    expect(workflowView).toContain(":show-revise-cta=\"canReviseFromFailure\"");
  });

  it("keeps the workflow graph draft as the editor source instead of deprecated compatibility fields", () => {
    expect(workflowView).toContain("workflowStore.loadWorkflowDraft");
    expect(workflowView).toContain("workflowStore.saveWorkflowDraft");
    expect(workflowView).toContain("activeDraft.graph");
    expect(workflowView).toContain("editorGraph");
    expect(workflowView).toContain("WorkflowGraphDraft");
    expect(workflowView).not.toContain("emptyWorkflowDSL");
    expect(workflowView).not.toContain("emptyCanvasGraph");
  });
});
