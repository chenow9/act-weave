<script setup lang="ts">
// @ts-nocheck — inject surface under page split (ZKL-64 item 17)
/** OpenAPI imports modals (ZKL-64 item 17). */
import ToolSchemaTreeView from "./ToolSchemaTreeView.vue";
import { useOpenAPIImportsPageContext } from "../composables/useOpenAPIImportsPageContext";

const scp = useOpenAPIImportsPageContext();
/* prettier-ignore */
const {
  connectionsStore, importModalVisible, importMode, selectedOpenAPIFile, selectedOpenAPIFilePreview, selectedImportId, detailLoading, detailError, actionNote, importingOpenAPI, generatingDraftsByImportId, deletingImportId, pendingDeleteImport, importDialogRef, detailDialogRef, deleteDialogRef,
  openapiDropdowns, importForm, importProviders, selectedWorkspaceOption, selectedProviderOption, selectedProviderCanImportOnline, selectedConnectionOption, selectedImport, selectedWorkspace, selectedConnection, selectedImportDetail, canImportOpenAPI, selectedImportDetailVisible, workspaceLabel, providerLabel, canProviderImportOnline,
  statusClass, statusDotClass, connectionAddress, toggleOpenAPIDropdown, handleModalTab, closeImportModal, retryImportDetail, closeImportDetail, closeDeleteConfirm, confirmRemoveImport, dismissActionNote, selectImportProvider, selectImportConnection, selectOpenAPIFile, importOpenAPI, generateDrafts
} = scp;
void ToolSchemaTreeView;
</script>

<template>
  <div v-if="importModalVisible" class="openapi-modal-backdrop" @click.self="closeImportModal">
    <section
      ref="importDialogRef"
      class="openapi-modal-card"
      role="dialog"
      aria-modal="true"
      aria-label="导入 OpenAPI"
      tabindex="-1"
      @click.stop
      @keydown.esc.stop.prevent="closeImportModal"
      @keydown.tab="handleModalTab($event, importDialogRef)"
    >
      <header class="openapi-modal-head">
        <div>
          <span><i class="fa-solid fa-file-import" /></span>
          <div>
            <h3>导入 OpenAPI</h3>
            <p>确认当前业务空间并选择 Provider、服务连接，解析接口清单</p>
          </div>
        </div>
        <button type="button" title="关闭" aria-label="关闭导入弹框" @click="closeImportModal">
          <i class="fa-solid fa-xmark" />
        </button>
      </header>

      <div class="openapi-modal-body">
        <div class="openapi-field">
          <label>当前业务空间</label>
          <div class="openapi-reference-select is-readonly" data-testid="openapi-current-workspace">
            <span
              ><i class="fa-solid fa-layer-group" />{{
                selectedWorkspaceOption
                  ? `${selectedWorkspaceOption.name} · ${selectedWorkspaceOption.displayName}`
                  : "未选择业务空间"
              }}</span
            >
            <small>在页面顶部切换</small>
          </div>
        </div>

        <div class="openapi-field">
          <label>导入方式</label>
          <div class="openapi-import-mode-tabs" role="tablist" aria-label="OpenAPI 导入方式">
            <button
              type="button"
              role="tab"
              :aria-selected="importMode === 'FILE'"
              :class="{ active: importMode === 'FILE' }"
              @click="importMode = 'FILE'"
            >
              <i class="fa-solid fa-file-arrow-up" /> 本地文件
            </button>
            <button
              type="button"
              role="tab"
              :aria-selected="importMode === 'ONLINE'"
              :class="{ active: importMode === 'ONLINE' }"
              @click="importMode = 'ONLINE'"
            >
              <i class="fa-solid fa-cloud-arrow-down" /> Provider 在线文档
            </button>
          </div>
        </div>

        <div class="openapi-field dropdown" @click.stop>
          <label>选择 Provider <b class="field-required-mark">*</b></label>
          <button
            class="openapi-reference-select"
            type="button"
            aria-haspopup="listbox"
            :aria-expanded="openapiDropdowns.provider"
            :disabled="!importForm.workspaceId || !importProviders.length"
            data-testid="openapi-provider-select"
            @click="toggleOpenAPIDropdown('provider')"
          >
            <span
              ><i class="fa-solid fa-cubes" />{{
                selectedProviderOption?.name ||
                (importForm.workspaceId
                  ? importProviders.length
                    ? "请选择 Provider"
                    : "当前空间暂无 Provider"
                  : "先选择业务空间")
              }}</span
            >
            <i class="fa-solid fa-chevron-down" :class="{ open: openapiDropdowns.provider }" />
          </button>
          <div v-if="openapiDropdowns.provider" class="openapi-select-menu" role="listbox">
            <button
              v-for="provider in importProviders"
              :key="provider.id"
              class="openapi-select-option"
              :class="{ selected: importForm.providerId === provider.id }"
              type="button"
              role="option"
              :aria-selected="importForm.providerId === provider.id"
              @click="selectImportProvider(provider.id)"
            >
              <span class="openapi-option-copy">
                <strong>{{ provider.name }}</strong>
                <small>{{ canProviderImportOnline(provider) ? "可在线导入" : "未配置在线 OpenAPI 文档" }}</small>
              </span>
              <i v-if="importForm.providerId === provider.id" class="fa-solid fa-circle-check" />
            </button>
          </div>
        </div>

        <div class="openapi-field dropdown" @click.stop>
          <label>选择服务连接</label>
          <button
            class="openapi-reference-select"
            type="button"
            aria-haspopup="listbox"
            :aria-expanded="openapiDropdowns.connection"
            :disabled="!importForm.providerId"
            data-testid="openapi-connection-select"
            @click="toggleOpenAPIDropdown('connection')"
          >
            <span
              ><i class="fa-solid fa-plug" />{{
                selectedConnectionOption?.name || (importForm.providerId ? "使用 Provider 默认连接" : "先选择 Provider")
              }}</span
            >
            <i class="fa-solid fa-chevron-down" :class="{ open: openapiDropdowns.connection }" />
          </button>
          <div v-if="openapiDropdowns.connection" class="openapi-select-menu" role="listbox">
            <button
              class="openapi-select-option"
              type="button"
              role="option"
              :aria-selected="!importForm.connectionId"
              @click="selectImportConnection('')"
            >
              <span>使用 Provider 默认连接</span>
              <i v-if="!importForm.connectionId" class="fa-solid fa-circle-check" />
            </button>
            <button
              v-for="connection in connectionsStore.serviceConnections.filter(
                (item) => item.providerId === importForm.providerId,
              )"
              :key="connection.id"
              class="openapi-select-option"
              :class="{ selected: importForm.connectionId === connection.id }"
              type="button"
              role="option"
              :aria-selected="importForm.connectionId === connection.id"
              @click="selectImportConnection(connection.id)"
            >
              <span>{{ connection.name }}</span>
              <i v-if="importForm.connectionId === connection.id" class="fa-solid fa-circle-check" />
            </button>
          </div>
        </div>

        <div v-if="importMode === 'FILE'" class="openapi-field">
          <label>OpenAPI 文件 <b class="field-required-mark">*</b></label>
          <label class="openapi-file-picker">
            <span class="openapi-file-picker-button"><i class="fa-solid fa-folder-open" />选择文件</span>
            <span class="openapi-file-picker-name">{{ selectedOpenAPIFile?.name || "请选择 JSON 或 YAML 文件" }}</span>
            <span class="openapi-file-picker-meta">最大 4 MB</span>
            <input
              class="openapi-file-input"
              data-testid="openapi-file-input"
              type="file"
              accept=".json,.yaml,.yml,application/json,application/yaml,text/yaml"
              @change="selectOpenAPIFile"
            />
          </label>
          <small v-if="selectedOpenAPIFilePreview.error" class="openapi-file-error" role="alert">{{
            selectedOpenAPIFilePreview.error
          }}</small>
        </div>

        <div v-if="importMode === 'FILE' && selectedOpenAPIFilePreview.endpointCount" class="import-drawer-preview">
          <div>
            <i class="fa-solid fa-list-check" />
            <span>
              <strong>识别到 {{ selectedOpenAPIFilePreview.endpointCount }} 个接口</strong>
              <small>{{ selectedOpenAPIFilePreview.readyCount }} 个接口可生成 Tool 草稿，导入后可逐项确认。</small>
            </span>
          </div>
          <div class="openapi-preview-table">
            <div><strong>方法</strong><strong>路径</strong><strong>建议 Tool</strong><strong>状态</strong></div>
            <div v-for="row in selectedOpenAPIFilePreview.rows.slice(0, 6)" :key="`${row.method}:${row.path}`">
              <span>{{ row.method }}</span
              ><span>{{ row.path }}</span
              ><span>{{ row.suggestedTool }}</span
              ><span>{{ row.statusText }}</span>
            </div>
          </div>
        </div>

        <div v-else-if="importMode === 'ONLINE' && selectedProviderCanImportOnline" class="import-drawer-preview">
          <div>
            <i class="fa-solid fa-cloud-arrow-down" />
            <span>
              <strong>Provider OpenAPI 来源</strong>
              <small>后端将从所选 Provider 的受管来源拉取并解析 OpenAPI 文档。</small>
            </span>
          </div>
          <div class="import-preview-empty">请求仅提交 Provider 和可选 Connection，不上传文件，也不绑定 Agent。</div>
        </div>
        <div
          v-else-if="importMode === 'ONLINE' && selectedProviderOption"
          class="import-drawer-preview unavailable"
          role="status"
          aria-live="polite"
        >
          <div>
            <i class="fa-solid fa-circle-info" />
            <span>
              <strong>Provider 和 Connection 已加载</strong>
              <small>当前 Provider 未配置可在线读取的 OpenAPI 文档，暂时不能发起在线导入。</small>
            </span>
          </div>
          <div class="import-preview-empty">
            数据不会再从下拉框中隐藏。需要在线导入时，请到 Provider 管理补充文档地址并启用按需发现。
          </div>
        </div>
        <div
          v-else-if="importMode === 'ONLINE'"
          class="import-drawer-preview unavailable"
          role="status"
          aria-live="polite"
        >
          <div>
            <i class="fa-solid fa-circle-info" />
            <span>
              <strong>当前空间暂无 Provider</strong>
              <small>请先在 Provider 管理中登记服务，再返回导入 OpenAPI。</small>
            </span>
          </div>
        </div>
      </div>

      <footer class="openapi-modal-actions">
        <span>导入后生成 Tool 草稿</span>
        <div>
          <button type="button" :disabled="importingOpenAPI" @click="closeImportModal">取消</button>
          <button type="button" :disabled="!canImportOpenAPI || importingOpenAPI" @click="importOpenAPI">
            <i v-if="importingOpenAPI" class="fa-solid fa-spinner fa-spin" />
            {{ importingOpenAPI ? "解析中" : "开始导入" }}
          </button>
        </div>
      </footer>
    </section>
  </div>

  <div v-if="selectedImportDetailVisible" class="openapi-modal-backdrop" @click.self="closeImportDetail">
    <section
      v-if="selectedImport"
      ref="detailDialogRef"
      class="openapi-modal-card openapi-detail-modal-card"
      role="dialog"
      aria-modal="true"
      aria-label="导入详情"
      tabindex="-1"
      @keydown.esc.stop.prevent="closeImportDetail"
      @keydown.tab="handleModalTab($event, detailDialogRef)"
    >
      <header class="openapi-modal-head openapi-detail-modal-head">
        <div>
          <span><i class="fa-solid fa-file-code" /></span>
          <div>
            <h3>导入详情</h3>
            <p>查看导入归属、连接与结构化契约</p>
          </div>
        </div>
        <button type="button" title="关闭" aria-label="关闭详情弹框" @click="closeImportDetail">
          <i class="fa-solid fa-xmark" />
        </button>
      </header>
      <div class="openapi-detail-modal-body" data-testid="openapi-detail-body">
        <div
          v-if="detailLoading"
          class="openapi-detail-state"
          data-testid="openapi-detail-loading"
          role="status"
          aria-live="polite"
        >
          <i class="fa-solid fa-spinner fa-spin" aria-hidden="true" />
          <strong>正在加载导入详情…</strong>
          <p>请稍候，不会触发 Tool 草稿生成。</p>
        </div>
        <div
          v-else-if="detailError"
          class="openapi-detail-state is-error"
          data-testid="openapi-detail-error"
          role="alert"
        >
          <i class="fa-solid fa-circle-exclamation" aria-hidden="true" />
          <strong>导入详情加载失败</strong>
          <p>{{ detailError }}</p>
          <div class="openapi-detail-state-actions">
            <button type="button" data-testid="openapi-detail-retry" @click="retryImportDetail">重试</button>
            <button type="button" data-testid="openapi-detail-error-close" @click="closeImportDetail">关闭</button>
          </div>
        </div>
        <template v-else>
          <div class="openapi-detail-hero">
            <i class="fa-solid fa-file-code" />
            <div>
              <strong :title="selectedImport.fileName">{{ selectedImport.fileName }}</strong>
              <small
                :title="`${selectedImport.source} · ${workspaceLabel(selectedImport.workspaceId)} · ${selectedImport.providerId || 'Provider'}`"
                >{{ selectedImport.source }} · {{ workspaceLabel(selectedImport.workspaceId) }} ·
                {{ selectedImport.providerId || "Provider" }}</small
              >
            </div>
            <span class="openapi-status-pill" :class="statusClass(selectedImport.status)">
              <span :class="statusDotClass(selectedImport.status)" />
              {{ selectedImport.status }}
            </span>
          </div>
          <div class="openapi-detail-grid import-detail-grid">
            <div class="config-summary-item">
              <i class="fa-solid fa-layer-group" />
              <span>归属空间</span>
              <strong :title="selectedWorkspace?.name || selectedImport.workspaceId">{{
                selectedWorkspace?.name || selectedImport.workspaceId
              }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-cubes" />
              <span>来源 Provider</span>
              <strong :title="providerLabel(selectedImport.providerId)">{{
                providerLabel(selectedImport.providerId)
              }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-plug-circle-bolt" />
              <span>服务连接</span>
              <strong :title="selectedConnection?.name || ''">{{ selectedConnection?.name }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-server" />
              <span>服务地址</span>
              <strong :title="connectionAddress(selectedConnection)">{{
                connectionAddress(selectedConnection)
              }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-list-check" />
              <span>接口数量</span>
              <strong>{{ selectedImport.totalEndpoints }}</strong>
            </div>
            <div class="config-summary-item">
              <i class="fa-solid fa-wand-magic-sparkles" />
              <span>生成状态</span>
              <strong>{{ selectedImport.readyEndpoints }} 个可生成</strong>
            </div>
          </div>
          <div class="openapi-detail-schema-stack">
            <ToolSchemaTreeView
              :nodes="selectedImportDetail?.requestTransport || []"
              title="请求参数"
              empty-text="当前导入记录未返回传输参数。"
            />
            <ToolSchemaTreeView
              :nodes="selectedImportDetail?.requestBodyNodes || []"
              title="请求体 Body"
              empty-text="当前导入记录未返回请求体结构。"
            />
            <ToolSchemaTreeView
              :nodes="selectedImportDetail?.responseNodes || []"
              title="响应结果"
              empty-text="当前导入记录未返回响应结构。"
            />
          </div>
          <div v-if="selectedImportDetail?.endpoints.length" class="tool-schema-endpoint-list">
            <div class="editable-schema-head">
              <div>
                <strong>接口明细</strong>
                <span>按接口查看导入出的结构化契约。</span>
              </div>
            </div>
            <div
              v-for="endpoint in selectedImportDetail.endpoints"
              :key="`${endpoint.method}-${endpoint.path}`"
              class="tool-schema-endpoint-card"
            >
              <div class="tool-schema-endpoint-head">
                <strong :title="`${endpoint.method} ${endpoint.path}`"
                  >{{ endpoint.method }} {{ endpoint.path }}</strong
                >
                <span>{{ endpoint.summary || endpoint.operationId || endpoint.status }}</span>
              </div>
              <ToolSchemaTreeView
                :nodes="endpoint.requestContract ? ([endpoint.requestContract].flat() as ToolSchemaNode[]) : []"
                title="请求体 Body"
                empty-text="无请求结构"
              />
              <ToolSchemaTreeView
                :nodes="endpoint.responseContract ? ([endpoint.responseContract].flat() as ToolSchemaNode[]) : []"
                title="响应结果"
                empty-text="无响应结构"
              />
            </div>
          </div>
        </template>
      </div>
      <div class="drawer-footer-actions openapi-detail-actions">
        <button type="button" @click="closeImportDetail">关闭</button>
        <button
          type="button"
          :disabled="detailLoading || Boolean(detailError) || Boolean(generatingDraftsByImportId[selectedImport.id])"
          @click="generateDrafts(selectedImport)"
        >
          <i v-if="generatingDraftsByImportId[selectedImport.id]" class="fa-solid fa-spinner fa-spin" />
          {{ generatingDraftsByImportId[selectedImport.id] ? "生成中" : "生成 Tool 草稿" }}
        </button>
      </div>
    </section>
  </div>

  <div v-if="pendingDeleteImport" class="openapi-modal-backdrop" @click.self="closeDeleteConfirm">
    <section
      ref="deleteDialogRef"
      class="openapi-modal-card openapi-confirm-modal-card"
      role="dialog"
      aria-modal="true"
      aria-label="删除导入记录"
      tabindex="-1"
      @keydown.esc.stop.prevent="closeDeleteConfirm"
      @keydown.tab="handleModalTab($event, deleteDialogRef)"
    >
      <header class="openapi-modal-head">
        <div>
          <span><i class="fa-solid fa-triangle-exclamation" /></span>
          <div>
            <h3>删除导入记录</h3>
            <p>删除后需要重新导入才能再次生成草稿</p>
          </div>
        </div>
        <button
          type="button"
          title="关闭"
          aria-label="关闭删除确认弹框"
          :disabled="Boolean(deletingImportId)"
          @click="closeDeleteConfirm"
        >
          <i class="fa-solid fa-xmark" />
        </button>
      </header>
      <div class="openapi-confirm-body">
        <strong>{{ pendingDeleteImport.fileName }}</strong>
        <p>确认删除这条 OpenAPI 导入记录？已生成的 Tool 草稿不会被自动删除。</p>
      </div>
      <footer class="openapi-modal-actions">
        <span>此操作会立即同步到后端</span>
        <div>
          <button type="button" :disabled="Boolean(deletingImportId)" @click="closeDeleteConfirm">取消</button>
          <button class="danger" type="button" :disabled="Boolean(deletingImportId)" @click="confirmRemoveImport">
            <i v-if="deletingImportId" class="fa-solid fa-spinner fa-spin" />
            {{ deletingImportId ? "删除中" : "确认删除" }}
          </button>
        </div>
      </footer>
    </section>
  </div>

  <div
    v-if="actionNote && !importModalVisible && !selectedImportId && !pendingDeleteImport"
    class="action-toast"
    role="status"
    aria-live="polite"
  >
    <span>{{ actionNote }}</span>
    <button type="button" aria-label="关闭提示" @click="dismissActionNote">
      <i class="fa-solid fa-xmark" />
    </button>
  </div>
</template>
