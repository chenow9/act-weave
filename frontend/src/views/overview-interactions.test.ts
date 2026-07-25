import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const currentDir = dirname(fileURLToPath(import.meta.url));
const overviewView = readFileSync(resolve(currentDir, "OverviewView.vue"), "utf8");

describe("overview interactions", () => {
  it("surfaces expanded KPIs, charts with tooltips, and detail tables", () => {
    expect(overviewView).toContain("工具调用成功率");
    expect(overviewView).toContain("Agent 链路成功率");
    expect(overviewView).toContain("工作流成功率");
    expect(overviewView).toContain("每日明细");
    expect(overviewView).toContain("调用最多的工具");
    expect(overviewView).toContain("失败较多的工具");
    expect(overviewView).toContain("最活跃业务空间");
    expect(overviewView).toContain("OverviewBarChart");
    expect(overviewView).toContain("OverviewSparkline");
    expect(overviewView).toContain('type="date"');
    expect(overviewView).toContain("applyRange");
    expect(overviewView).toContain("overview-data-table");
  });

  it("keeps secondary workspace and orchestration shortcuts actionable", () => {
    expect(overviewView).toContain("业务空间");
    expect(overviewView).toContain('router.push({ name: "workspaces" })');
    expect(overviewView).toContain('router.push({ name: "smart-dag" })');
    expect(overviewView).toContain('@click="goSmartDag"');
  });

  it("uses the shared refactored action buttons on the overview page", () => {
    expect(overviewView).toContain("primary-button");
    expect(overviewView).toContain("ghost-button");
    expect(overviewView).not.toContain("<el-button");
  });
});
