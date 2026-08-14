<script setup lang="ts">
// @ts-nocheck — inject surface + slot row typing under page split (ZKL-64 item 11)
/** Connection detail panel (ZKL-64 item 11). */
import { useI18n } from "vue-i18n";

import { useServiceConnectionsPageContext } from "../composables/useServiceConnectionsPageContext";

const { t } = useI18n();
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
  connectionUsesAuthentication,
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
          {{ t("connections.backToList") }}
        </button>
        <span />
        <small>{{ t("connections.readonlyDetail") }}</small>
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
          {{
            isConnectionVerifying(detailConnection.id) ? t("connections.verifying") : t("connections.verifyConnection")
          }}
        </button>
        <button
          class="connection-primary-button compact"
          type="button"
          @click.stop="openConnectionEditor(detailConnection)"
        >
          <i class="fa-solid fa-pen" />
          {{ t("connections.editConnection") }}
        </button>
      </div>
    </div>

    <section class="connection-detail-hero">
      <div>
        <div class="connection-detail-hero-icon"><i class="fa-solid fa-plug" /></div>
        <div>
          <span class="connection-eyebrow">{{ t("connections.detailEyebrow") }}</span>
          <h2
            tabindex="0"
            :title="detailConnection.name"
            :aria-label="t('connections.fullNameAria', { name: detailConnection.name })"
          >
            {{ detailConnection.name }}
          </h2>
          <p>
            <span
              tabindex="0"
              :title="connectionAddress(detailConnection)"
              :aria-label="t('connections.fullAddressAria', { address: connectionAddress(detailConnection) })"
              >{{ connectionAddress(detailConnection) }}</span
            >
            <button
              class="connection-copy-button hero-copy"
              type="button"
              :aria-label="t('connections.copyDetailAddressAria')"
              @click.stop="copyConnectionText(connectionAddress(detailConnection), t('connections.serviceAddress'))"
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
          statusClass(detailConnection) === 'available' ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'
        "
      />
      <div>
        <strong>{{
          statusClass(detailConnection) === "available"
            ? t("connections.availableForTools")
            : t("connections.needsAttention")
        }}</strong>
        <span>{{ verificationSummary(detailConnection) }}</span>
      </div>
    </div>

    <section class="connection-detail-grid">
      <article class="connection-detail-card">
        <header class="connection-detail-card-head">
          <i class="fa-solid fa-link" />
          <strong>{{ t("connections.serviceAddressCard") }}</strong>
          <span>{{ t("connections.serviceAddressCardHint") }}</span>
        </header>
        <div class="connection-detail-facts">
          <span
            ><small>{{ t("connections.finalAddress") }}</small
            ><code
              tabindex="0"
              :title="connectionAddress(detailConnection)"
              :aria-label="t('connections.fullFinalAddressAria', { address: connectionAddress(detailConnection) })"
              >{{ connectionAddress(detailConnection) }}</code
            ></span
          >
          <span
            ><small>{{ t("connections.verificationEndpoint") }}</small
            ><code
              tabindex="0"
              :title="`${detailConnection.protocolConfig.verificationMethod || 'GET'} ${verificationPathLabel(detailConnection)}`"
              :aria-label="
                t('connections.fullVerificationAria', {
                  endpoint: `${detailConnection.protocolConfig.verificationMethod || 'GET'} ${verificationPathLabel(detailConnection)}`,
                })
              "
              >{{ detailConnection.protocolConfig.verificationMethod || "GET" }}
              {{ verificationPathLabel(detailConnection) }}</code
            ></span
          >
          <span
            ><small>{{ t("connections.protocol") }}</small
            ><b>{{ detailConnection.protocol || "HTTP" }}</b></span
          >
          <span
            ><small>{{ t("connections.port") }}</small
            ><code>{{ connectionPortLabel(detailConnection) }}</code></span
          >
          <span
            ><small>Base Path</small><code>{{ detailConnection.protocolConfig.basePath || "/" }}</code></span
          >
          <span
            ><small>{{ t("connections.expectedStatus") }}</small
            ><code>{{ detailConnection.protocolConfig.expectedStatus || "200-299" }}</code></span
          >
        </div>
      </article>

      <article class="connection-detail-card">
        <header class="connection-detail-card-head">
          <i class="fa-solid fa-key" />
          <strong>{{ t("connections.authMethod") }}</strong>
        </header>
        <div class="connection-detail-facts">
          <span
            ><small>{{ t("connections.authType") }}</small
            ><b>{{ authModeLabel(detailConnection.authConfig.mode, detailConnection.authConfig.label) }}</b></span
          >
          <span v-if="connectionUsesAuthentication(detailConnection)"
            ><small>{{ t("connections.credentialPlacement") }}</small
            ><b>{{ credentialPlacementLabel(detailConnection) }}</b></span
          >
          <span v-else
            ><small>{{ t("connections.credentialHandling") }}</small
            ><b>{{ t("connections.noCredentialInjection") }}</b></span
          >
          <span
            ><small>{{ t("connections.environment") }}</small
            ><em>{{ environmentLabel(detailConnection.environment) }}</em></span
          >
          <span v-if="connectionUsesAuthentication(detailConnection)"
            ><small>{{ t("connections.afterExpires") }}</small
            ><b>{{ refreshModeLabel(detailConnection) }}</b></span
          >
        </div>
      </article>

      <article class="connection-detail-card">
        <header class="connection-detail-card-head">
          <i class="fa-solid fa-vial-circle-check" />
          <strong>{{ t("connections.verificationResult") }}</strong>
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
