<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 14)
/** Smart DAG modals (blueprint picker + sandbox) (ZKL-64 item 14). */
import { useSmartDagPageContext } from "../composables/useSmartDagPageContext";

const scp = useSmartDagPageContext();
const {
  selectedBlueprintId,
  blueprintPickerOpen,
  listQuery,
  statusFilter,
  listPage,
  sandbox,
  sandboxError,
  blueprintModalRef,
  blueprintSearchInputRef,
  sandboxModalRef,
  sandboxInputRef,
  currentBlueprint,
  averageAiScore,
  filteredBlueprintList,
  totalListPages,
  paginatedBlueprintList,
  listPageNumbers,
  paginationStart,
  paginationEnd,
  closeBlueprintPicker,
  closeSandbox,
  handleBlueprintModalKeydown,
  handleSandboxModalKeydown,
  getStatusText,
  getStatusClass,
  getAutomationClass,
  setStatusFilter,
  loadBlueprint,
  runSandboxTrial,
} = scp;
</script>

<template>
  <div v-if="blueprintPickerOpen" class="smart-modal-backdrop" @click.self="closeBlueprintPicker()">
    <div
      ref="blueprintModalRef"
      data-testid="blueprint-picker-modal"
      class="blueprint-picker-modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="blueprint-picker-title"
      tabindex="-1"
      @keydown="handleBlueprintModalKeydown"
    >
      <header>
        <div>
          <span>Blueprint Hub</span>
          <h2 id="blueprint-picker-title">打开智能蓝图</h2>
          <p>智能编排默认直达画布，蓝图库仅用于切换已有流程、查看待复核资产与继续协作。</p>
        </div>
        <button type="button" aria-label="关闭蓝图库" title="关闭蓝图库" @click="closeBlueprintPicker()">
          <i class="fa-solid fa-xmark" />
        </button>
      </header>

      <section>
        <div class="smart-picker-toolbar">
          <label>
            <i class="fa-solid fa-magnifying-glass" />
            <input
              ref="blueprintSearchInputRef"
              v-model="listQuery"
              type="text"
              placeholder="搜索蓝图名称 / Agent / AI 策略..."
            />
          </label>
          <div class="smart-status-filter" role="radiogroup" aria-label="蓝图状态筛选">
            <button
              type="button"
              role="radio"
              :aria-checked="statusFilter === 'ALL'"
              :class="{ active: statusFilter === 'ALL' }"
              @click="setStatusFilter('ALL')"
            >
              全部状态
            </button>
            <button
              type="button"
              role="radio"
              :aria-checked="statusFilter === 'published'"
              :class="{ active: statusFilter === 'published' }"
              @click="setStatusFilter('published')"
            >
              已发布
            </button>
            <button
              type="button"
              role="radio"
              :aria-checked="statusFilter === 'review'"
              :class="{ active: statusFilter === 'review' }"
              @click="setStatusFilter('review')"
            >
              待复核
            </button>
            <button
              type="button"
              role="radio"
              :aria-checked="statusFilter === 'draft'"
              :class="{ active: statusFilter === 'draft' }"
              @click="setStatusFilter('draft')"
            >
              AI 草稿
            </button>
          </div>
          <span>共 {{ filteredBlueprintList.length }} 条蓝图</span>
          <span>平均 AI 命中 {{ averageAiScore }}%</span>
        </div>

        <div class="smart-blueprint-grid">
          <article
            v-for="workflow in paginatedBlueprintList"
            :key="workflow.id"
            :class="{ selected: selectedBlueprintId === workflow.id }"
          >
            <div>
              <strong>{{ workflow.name }}</strong>
              <span class="smart-status-badge" :class="getStatusClass(workflow.status)">{{
                getStatusText(workflow.status)
              }}</span>
            </div>
            <p>{{ workflow.description }}</p>
            <div class="smart-blueprint-chips">
              <span :class="getAutomationClass(workflow.automationMode)">{{ workflow.automationMode }}</span>
              <span>{{ workflow.agent }}</span>
              <span>{{ workflow.space }}</span>
            </div>
            <div class="smart-blueprint-stats">
              <span
                ><small>节点数</small><b>{{ workflow.nodes.length }}</b></span
              >
              <span
                ><small>连线数</small><b>{{ workflow.connections.length }}</b></span
              >
              <span
                ><small>AI 评分</small><b>{{ workflow.aiScore }}%</b></span
              >
            </div>
            <button type="button" @click="loadBlueprint(workflow.id)">
              {{ selectedBlueprintId === workflow.id ? "继续编辑" : "打开画布" }}
            </button>
          </article>
          <div v-if="!paginatedBlueprintList.length" class="smart-picker-empty">
            <i class="fa-solid fa-wand-magic-sparkles" />
            <strong>没有匹配到智能编排蓝图</strong>
            <span>换个关键词试试，或者清空筛选条件。</span>
          </div>
        </div>
      </section>

      <footer>
        <span v-if="filteredBlueprintList.length"
          >显示第 {{ paginationStart }} - {{ paginationEnd }} 条，共 {{ filteredBlueprintList.length }} 条</span
        >
        <span v-else>当前没有可展示的数据</span>
        <div>
          <button type="button" :disabled="listPage === 1" @click="listPage -= 1">上一页</button>
          <button
            v-for="page in listPageNumbers"
            :key="page"
            type="button"
            :class="{ active: listPage === page }"
            @click="listPage = page"
          >
            {{ page }}
          </button>
          <button type="button" :disabled="listPage === totalListPages" @click="listPage += 1">下一页</button>
        </div>
      </footer>
    </div>
  </div>

  <div v-if="sandbox.show" class="smart-modal-backdrop" @click.self="closeSandbox()">
    <div
      ref="sandboxModalRef"
      class="smart-trial-modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="smart-trial-title"
      tabindex="-1"
      @keydown="handleSandboxModalKeydown"
    >
      <header>
        <i class="fa-solid fa-flask" />
        <div>
          <h2 id="smart-trial-title">智能编排模拟试运行</h2>
          <p>提供入参来执行测试沙箱，验证 AI 补全后的链路无误</p>
        </div>
        <button type="button" aria-label="关闭模拟试运行" title="关闭模拟试运行" @click="closeSandbox()">
          <i class="fa-solid fa-xmark" />
        </button>
      </header>
      <label>
        <span>测试实例流 ID</span>
        <input :value="currentBlueprint.id" readonly />
      </label>
      <label>
        <span>测试参数 (JSON Schema)</span>
        <textarea
          ref="sandboxInputRef"
          v-model="sandbox.inputJson"
          rows="4"
          :aria-invalid="Boolean(sandboxError)"
          :aria-describedby="sandboxError ? 'sandbox-json-error' : undefined"
        />
      </label>
      <p v-if="sandboxError" id="sandbox-json-error" class="smart-trial-error" role="alert">{{ sandboxError }}</p>
      <footer>
        <button type="button" @click="closeSandbox()">取消</button>
        <button type="button" class="primary" @click="runSandboxTrial">
          <i class="fa-solid fa-circle-play" />
          运行沙箱
        </button>
      </footer>
    </div>
  </div>
</template>
