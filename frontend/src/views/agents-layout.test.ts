import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const agentsView = readFileSync(resolve(currentDir, "AgentsView.vue"), "utf8");
const dataTable = readFileSync(resolve(currentDir, "../components/DataTable.vue"), "utf8");
const managementRowActions = readFileSync(resolve(currentDir, "../components/ManagementRowActions.vue"), "utf8");

describe("agents view prototype alignment", () => {
  it("uses the reference registry dashboard and table layout instead of row cards", () => {
    expect(agentsView).toContain("agent-grid");
    expect(agentsView).toContain("ManagementPageHeader");
    expect(agentsView).toContain("ManagementSummaryStrip");
    expect(agentsView).toContain("<ManagementList");
    expect(agentsView).toContain("ManagementListColumn<Agent>");
    expect(agentsView).toContain('storage-key="actweave:agents:columns"');
    expect(agentsView).toContain('{ key: "identity", label: "Agent"');
    expect(agentsView).toContain('label: "绑定空间"');
    expect(agentsView).toContain('label: "决策模型"');
    expect(agentsView).toContain('label: "最近修改"');
    expect(agentsView).toContain('{ key: "actions", label: "操作", width: 68');
    expect(agentsView).toContain("agent-identity-cell");
    expect(agentsView).toContain("agent-workspace-pill");
    expect(agentsView).toContain("agent-model-chip");
    expect(agentsView).toContain("source-note");
    expect(agentsView).toContain("prompt-preview");
    expect(agentsView).toContain("查看 Revision");
    expect(agentsView).toContain(":pagination=\"agents.pagination\"");
    expect(agentsView).toContain("hasAgentRecords");
    expect(agentsView).toContain("暂无 Agent");
    expect(agentsView).not.toContain("<el-table");
    expect(agentsView).not.toContain("agent-table-head");
    expect(agentsView).not.toContain("agent-row");
    expect(agentsView).not.toContain("m1-editor");
  });

  it("uses prototype controls and Font Awesome icon treatment for Agent actions", () => {
    expect(agentsView).toContain("primary-button");
    expect(agentsView).toContain("ghost-button");
    expect(agentsView).toContain("ManagementList");
    expect(agentsView).toContain("ManagementRowActions");
    expect(agentsView).not.toContain('menu-label="更多 Agent 操作"');
    expect(managementRowActions).toContain("width: 44px;");
    expect(managementRowActions).toContain("gap: 4px;");
    expect(agentsView).toContain("fa-solid fa-circle-info");
    expect(agentsView).toContain("fa-wand-magic-sparkles");
    expect(agentsView).toContain("fa-solid fa-file-lines");
    expect(agentsView).toContain("fa-solid fa-circle-plus");
    expect(agentsView).toContain("fa-solid fa-sliders");
    expect(agentsView).toContain("agent-studio-panel");
    expect(agentsView).toContain("agent-studio-shell");
    expect(agentsView).toContain("agent-studio-section");
    expect(agentsView).toContain("agent-status-toggle");
    expect(agentsView).toContain("studio-prompt-editor");
    expect(agentsView).toContain("agent-prompt-detail-modal");
    expect(agentsView).toContain("AppSelect");
    expect(agentsView).not.toContain("<el-drawer");
    expect(agentsView).toContain("agent-capability-dialog");
    expect(agentsView).toContain('class="modal-field select-field"');
    expect(agentsView).not.toContain("@element-plus/icons-vue");
    expect(agentsView).not.toContain("<el-button type=\"primary\" :icon=\"CirclePlus\"");
  });

  it("keeps Agent creation free of manual ids and uses the shared feedback toast", () => {
    expect(agentsView).toContain("id: \"\"");
    expect(agentsView).not.toContain("agent.custom-");
    expect(agentsView).not.toContain("Agent ID");
    expect(agentsView).not.toContain("v-model=\"draftAgent.id\"");
    expect(agentsView).toContain("agentActionNote");
    expect(agentsView).toContain("['action-toast'");
  });

  it("uses the shared segmented filters and studio actions from the new UI refactor", () => {
    expect(agentsView).toContain("agentStatusFilter");
    expect(agentsView).toContain("agentManagementFilterOptions");
    expect(agentsView).toContain("ManagementSegmentedFilter");
    expect(agentsView).not.toContain("workspaceFilterOptions");
    expect(agentsView).not.toContain("agentWorkspaceFilter");
    expect(agentsView).toContain("activeWorkspaceFilterId");
    expect(agentsView).toContain("studioMode");
    expect(agentsView).toContain("enterCreateMode");
    expect(agentsView).toContain("enterEditMode");
    expect(agentsView).toContain("agent-studio-actions");
    expect(agentsView).toContain('{ label: "全部", value: "ALL"');
    expect(agentsView).toContain("运行中");
    expect(agentsView).toContain('{ label: "暂停", value: "DISABLED"');
    expect(agentsView).not.toContain("`${option.label} (${option.count})`");
    expect(agentsView).toContain('selection-tone="neutral"');
    expect(agentsView).toContain('{ key: "actions", label: "操作", width: 68');
  });

  it("keeps the reference visual details locally scoped to the Agent page", () => {
    expect(agentsView).toContain("<style scoped>");
    expect(agentsView).toContain(".agent-registry-card.management-list-card");
    expect(agentsView).toContain("background: transparent");
    expect(agentsView).toContain("box-shadow: none");
    expect(agentsView).not.toContain(".agent-grid .panel");
    expect(agentsView).not.toContain(".agent-registry-table th");
    expect(dataTable).toContain("table-layout: fixed;");
    expect(dataTable).toContain("position: sticky;");
    expect(agentsView).toContain(".agent-status-pill");
    expect(agentsView).toContain("border-radius: 999px");
    expect(agentsView).toContain(".agent-workspace-pill");
    expect(agentsView).toContain(".agent-model-chip");
    expect(agentsView).toContain(".agent-grid .segmented-filter");
    expect(agentsView).toContain(".agent-create-button span");
    expect(agentsView).toContain("text-transform: none");
    expect(agentsView).toContain("color: #fff");
    expect(agentsView).toContain(".agent-create-button i");
    expect(agentsView).toContain("color: #34d399");
  });
});
