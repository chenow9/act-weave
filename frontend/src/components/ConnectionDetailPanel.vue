<script setup lang="ts">
// @ts-nocheck — inject surface + slot row typing under page split (ZKL-64 item 11)
/** Connection detail panel (ZKL-64 item 11). */
import { useServiceConnectionsPageContext } from "../composables/useServiceConnectionsPageContext";

const scp = useServiceConnectionsPageContext();
const {
  connectionCurrentView,
  detailConnection,
  detailVerificationChecks,
  copyConnectionText,
  isConnectionVerifying,
  statusLabel,
  verificationCheckAction,
  statusClass,
  statusPillClass,
  statusDotClass,
  connectionAddress,
  verificationPathLabel,
  connectionPortLabel,
  credentialPlacementLabel,
  refreshModeLabel,
  closeConnectionPreview,
  openConnectionEditor,
  verifyConnection,
  environmentLabel,
  authModeLabel,
  verificationModeLabel,
  verificationSummary,
} = scp;
</script>

<template>
  <section v-if="connectionCurrentView === 'detail' && detailConnection" class="connection-detail-page">
    <div class="connection-detail-topbar">
      <div>
        <button class="connection-detail-back" type="button" @click.stop="closeConnectionPreview">
          <i class="fa-solid fa-chevron-left" />
          返回连接列表
        </button>
        <span />
        <small>只读详情</small>
      </div>
      <div>
        <button
          class="connection-secondary-button"
          type="button"
          :aria-busy="isConnectionVerifying(detailConnection.id) ? 'true' : 'false'"
          :disabled="isConnectionVerifying(detailConnection.id)"
          @click.stop="verifyConnection(detailConnection)"
        >
          <i :class="isConnectionVerifying(detailConnection.id) ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-vial'" />
          {{ isConnectionVerifying(detailConnection.id) ? "验证中" : "验证连接" }}
        </button>
        <button
          class="connection-primary-button compact"
          type="button"
          @click.stop="openConnectionEditor(detailConnection)"
        >
          <i class="fa-solid fa-pen" />
          编辑连接
        </button>
      </div>
    </div>

    <section class="connection-detail-hero">
      <div>
        <div class="connection-detail-hero-icon"><i class="fa-solid fa-plug" /></div>
        <div>
          <span class="connection-eyebrow">连接详情</span>
          <h2 tabindex="0" :title="detailConnection.name" :aria-label="`完整连接名称：${detailConnection.name}`">
            {{ detailConnection.name }}
          </h2>
          <p>
            <span
              tabindex="0"
              :title="connectionAddress(detailConnection)"
              :aria-label="`完整服务地址：${connectionAddress(detailConnection)}`"
              >{{ connectionAddress(detailConnection) }}</span
            >
            <button
              class="connection-copy-button hero-copy"
              type="button"
              aria-label="复制详情服务地址"
              @click.stop="copyConnectionText(connectionAddress(detailConnection), '服务地址')"
            >
              <i class="fa-regular fa-copy" />
            </button>
          </p>
        </div>
      </div>
      <span class="connection-status-pill large" :class="statusPillClass(detailConnection)">
        <span class="connection-status-dot" :class="statusDotClass(detailConnection)" />
        {{ statusLabel(detailConnection) }}
      </span>
    </section>

    <div class="connection-verdict-banner" :class="statusClass(detailConnection)">
      <i
        :class="
          statusLabel(detailConnection) === '可用' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'
        "
      />
      <div>
        <strong>{{ statusLabel(detailConnection) === "可用" ? "当前连接可被 Tool 使用" : "当前连接需要处理" }}</strong>
        <span>{{ verificationSummary(detailConnection.id) }}</span>
      </div>
    </div>

    <section class="connection-detail-grid">
      <article class="connection-detail-card">
        <header class="connection-detail-card-head">
          <i class="fa-solid fa-link" />
          <strong>服务地址</strong>
          <span>— Tool 调用时实际访问的位置</span>
        </header>
        <div class="connection-detail-facts">
          <span
            ><small>最终访问地址</small
            ><code
              tabindex="0"
              :title="connectionAddress(detailConnection)"
              :aria-label="`完整最终访问地址：${connectionAddress(detailConnection)}`"
              >{{ connectionAddress(detailConnection) }}</code
            ></span
          >
          <span
            ><small>验证接口</small
            ><code
              tabindex="0"
              :title="`${detailConnection.protocolConfig.verificationMethod || 'GET'} ${verificationPathLabel(detailConnection)}`"
              :aria-label="`完整验证接口：${detailConnection.protocolConfig.verificationMethod || 'GET'} ${verificationPathLabel(detailConnection)}`"
              >{{ detailConnection.protocolConfig.verificationMethod || "GET" }}
              {{ verificationPathLabel(detailConnection) }}</code
            ></span
          >
          <span
            ><small>协议</small><b>{{ detailConnection.protocol || "HTTP" }}</b></span
          >
          <span
            ><small>端口</small><code>{{ connectionPortLabel(detailConnection) }}</code></span
          >
          <span
            ><small>Base Path</small><code>{{ detailConnection.protocolConfig.basePath || "/" }}</code></span
          >
          <span
            ><small>期望状态码</small
            ><code>{{ detailConnection.protocolConfig.expectedStatus || "200-299" }}</code></span
          >
        </div>
      </article>

      <article class="connection-detail-card">
        <header class="connection-detail-card-head">
          <i class="fa-solid fa-key" />
          <strong>认证方式</strong>
        </header>
        <div class="connection-detail-facts">
          <span
            ><small>认证类型</small
            ><b>{{ authModeLabel(detailConnection.authConfig.mode, detailConnection.authConfig.label) }}</b></span
          >
          <span
            ><small>凭证位置</small><b>{{ credentialPlacementLabel(detailConnection) }}</b></span
          >
          <span
            ><small>使用环境</small><em>{{ environmentLabel(detailConnection.environment) }}</em></span
          >
          <span
            ><small>凭证过期后</small><b>{{ refreshModeLabel(detailConnection) }}</b></span
          >
        </div>
      </article>

      <article class="connection-detail-card">
        <header class="connection-detail-card-head">
          <i class="fa-solid fa-vial-circle-check" />
          <strong>验证结果</strong>
          <span>{{ verificationModeLabel(detailConnection.id) }}</span>
        </header>
        <div class="connection-verification-plan">
          <div
            v-for="check in detailVerificationChecks"
            :key="check.label"
            class="connection-verification-item"
            :class="check.status"
          >
            <div><i :class="check.icon" /></div>
            <span>
              <b>{{ check.statusLabel }}</b>
              <small>{{ check.desc }}</small>
            </span>
            <button
              v-if="check.status === 'failed'"
              class="connection-inline-action"
              type="button"
              @click.stop="verificationCheckAction(detailConnection, check)"
            >
              {{ check.actionLabel }}
            </button>
            <i v-else :class="check.status === 'passed' ? 'fa-solid fa-circle-check' : 'fa-regular fa-circle'" />
          </div>
        </div>
      </article>
    </section>
  </section>
</template>
