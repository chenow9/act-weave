<script setup lang="ts">
/** Connection create/edit form panel (ZKL-64 item 11). */
import { useI18n } from "vue-i18n";

import AppSelect from "./AppSelect.vue";
import ConnectionFormActions from "./ConnectionFormActions.vue";
import { useServiceConnectionsPageContext } from "../composables/useServiceConnectionsPageContext";

const { t } = useI18n();
const scp = useServiceConnectionsPageContext();
const {
  OUTBOUND_MODES,
  connectionCurrentView,
  connectionFormMode,
  draftConnection,
  connectionNameInput,
  verificationSectionOpen,
  advancedSectionOpen,
  connectionFormErrors,
  connectionVerificationPhase,
  formSubmitError,
  serviceAddressInput,
  machineCredentialInput,
  impactProof,
  impactLoading,
  switchModePending,
  environmentTrigger,
  connectionFormWorkspace,
  connectionDropdowns,
  environmentOptions,
  refreshModeOptions,
  verificationMethodOptions,
  providerOptions,
  providerSupportedModes,
  hasProviderOutboundContract,
  draftOutboundMode,
  isMigrationConnection,
  brokerClientId,
  brokerScopesText,
  passthroughMaxResidence,
  connectionFormTitle,
  formSubmitting,
  draftConnectionVerificationPreview,
  verificationPathDisplay,
  draftEnvironmentLabel,
  computedRefreshModeLabel,
  needsRefreshConfig,
  showsTokenFieldPaths,
  formVerificationChecks,
  formVerificationResultTitle,
  selectConnectionProvider,
  trapConnectionFormFocus,
  outboundModeCardTitle,
  outboundModeCardHint,
  selectOutboundMode,
  confirmModeSwitch,
  cancelModeSwitch,
  clearConnectionFormError,
  requestCloseConnectionForm,
  selectRefreshMode,
  toggleConnectionDropdown,
  openConnectionDropdownFromKeyboard,
  selectEnvironment,
  handleConnectionOptionKeydown,
  toggleVerificationSection,
  selectVerificationMethod,
  toggleAdvancedSection,
} = scp;
</script>
<template>
  <section
    v-if="connectionCurrentView === 'form'"
    class="connection-form-modal"
    role="dialog"
    aria-modal="true"
    :aria-labelledby="'connection-form-title'"
    @keydown="trapConnectionFormFocus"
  >
    <div class="connection-form-backdrop" @click.stop="requestCloseConnectionForm('backdrop')" />
    <div ref="connectionFormWorkspace" class="connection-form-workspace" @click.stop>
      <header class="connection-form-topbar">
        <div class="connection-form-title-lockup">
          <span class="connection-form-icon" aria-hidden="true">
            <i class="fa-solid fa-link" />
          </span>
          <div>
            <h2 id="connection-form-title">{{ connectionFormTitle }}</h2>
            <p>
              {{
                connectionFormMode === "create"
                  ? t("connections.formCreateSubtitle")
                  : t("connections.formEditSubtitle")
              }}
            </p>
          </div>
        </div>
        <button
          class="connection-form-close"
          type="button"
          :aria-label="t('connections.closeFormAria')"
          :disabled="formSubmitting"
          @click.stop="requestCloseConnectionForm('cancel')"
        >
          <i class="fa-solid fa-xmark" />
        </button>
      </header>
      <div class="connection-form-body">
        <div class="connection-form-single-column">
          <section class="connection-form-section basic" aria-labelledby="connection-basic-title">
            <header class="connection-section-heading">
              <span class="connection-section-icon" aria-hidden="true">
                <i class="fa-solid fa-link" />
              </span>
              <div>
                <h3 id="connection-basic-title">{{ t("connections.basicInfo") }}</h3>
                <p>{{ t("connections.basicInfoHelp") }}</p>
              </div>
            </header>
            <div class="connection-field-grid identity">
              <label class="connection-field">
                <span>{{ t("connections.fieldName") }} <b class="connection-required-mark">*</b></span>
                <input
                  ref="connectionNameInput"
                  v-model="draftConnection.name"
                  :placeholder="t('connections.fieldNamePlaceholder')"
                  :aria-invalid="connectionFormErrors.name ? 'true' : 'false'"
                  :aria-describedby="connectionFormErrors.name ? 'connection-name-error' : undefined"
                  @input="clearConnectionFormError('name')"
                />
                <small v-if="connectionFormErrors.name" id="connection-name-error" class="connection-field-error">{{
                  connectionFormErrors.name
                }}</small>
              </label>
              <div class="connection-field dropdown" @click.stop>
                <span>{{ t("connections.fieldEnvironment") }} <b class="connection-required-mark">*</b></span>
                <button
                  ref="environmentTrigger"
                  data-testid="connection-environment-trigger"
                  data-dropdown-trigger="environment"
                  class="connection-reference-select"
                  type="button"
                  :aria-invalid="connectionFormErrors.environment ? 'true' : 'false'"
                  :aria-describedby="connectionFormErrors.environment ? 'connection-environment-error' : undefined"
                  :aria-label="t('connections.environmentAria', { label: draftEnvironmentLabel })"
                  aria-haspopup="listbox"
                  :aria-expanded="connectionDropdowns.environment ? 'true' : 'false'"
                  aria-controls="connection-environment-menu"
                  @click="toggleConnectionDropdown('environment')"
                  @keydown="openConnectionDropdownFromKeyboard($event, 'environment')"
                >
                  <span>{{ draftEnvironmentLabel }}</span>
                  <i class="fa-solid fa-chevron-down" :class="{ open: connectionDropdowns.environment }" />
                </button>
                <div
                  v-if="connectionDropdowns.environment"
                  id="connection-environment-menu"
                  class="connection-select-menu"
                  role="listbox"
                >
                  <button
                    v-for="(option, index) in environmentOptions"
                    :key="option.value"
                    class="connection-select-option"
                    :class="{ selected: draftConnection.environment === option.value }"
                    type="button"
                    role="option"
                    tabindex="-1"
                    :aria-selected="draftConnection.environment === option.value ? 'true' : 'false'"
                    @click="selectEnvironment(option.value)"
                    @keydown="handleConnectionOptionKeydown($event, 'environment', index)"
                  >
                    {{ option.label }}
                    <i v-if="draftConnection.environment === option.value" class="fa-solid fa-check" />
                  </button>
                </div>
                <small
                  v-if="connectionFormErrors.environment"
                  id="connection-environment-error"
                  class="connection-field-error"
                  >{{ connectionFormErrors.environment }}</small
                >
              </div>
            </div>
            <label class="connection-field select-field">
              <span
                >{{ t("connections.fieldProvider") }}<b class="connection-required-mark">*</b></span
              >
              <AppSelect
                :model-value="draftConnection.providerId"
                :options="providerOptions"
                :placeholder="t('connections.providerPlaceholder')"
                :disabled="connectionFormMode === 'edit'"
                :aria-label="t('connections.fieldProvider')"
                :aria-required="true"
                @update:model-value="selectConnectionProvider(String($event))"
              />
              <small class="connection-field-help">{{ t("connections.providerHelp") }}</small>
            </label>
            <label class="connection-field locked">
              <span>{{ t("connections.providerEndpointReadonly") }}</span>
              <input
                ref="serviceAddressInput"
                :value="draftConnection.protocolConfig.domain || t('connections.notConfigured')"
                class="mono"
                disabled
                readonly
              />
            </label>
            <!-- Dual-mode outbound identity (UI v0.1): only BROKER_OBO | REQUEST_PASSTHROUGH -->
            <div class="connection-outbound-strategy" data-testid="connection-outbound-strategy">
              <header class="connection-outbound-strategy-head">
                <strong
                  >{{ t("connections.outboundStrategy") }} <b class="connection-required-mark">*</b></strong
                >
                <p>{{ t("connections.outboundStrategyHelp") }}</p>
              </header>
              <div
                v-if="isMigrationConnection || draftConnection.migrationState === 'MIGRATION_REQUIRED'"
                class="connection-auth-contract-warning"
                role="status"
                data-testid="connection-migration-wizard-hint"
              >
                <i class="fa-solid fa-triangle-exclamation" />
                <div>
                  <strong>{{ t("connections.migrationWizardTitle") }}</strong>
                  <p>{{ t("connections.migrationWizardBody") }}</p>
                </div>
              </div>
              <div v-if="!hasProviderOutboundContract" class="connection-auth-contract-warning" role="alert">
                <i class="fa-solid fa-circle-exclamation" />
                <div>
                  <strong>{{ t("connections.noOutboundContractTitle") }}</strong>
                  <p>{{ t("connections.noOutboundContractBody") }}</p>
                </div>
              </div>
              <div
                v-else
                class="connection-outbound-cards"
                role="radiogroup"
                :aria-label="t('connections.outboundStrategyAria')"
              >
                <button
                  v-for="mode in OUTBOUND_MODES"
                  :key="mode"
                  type="button"
                  class="connection-outbound-card"
                  role="radio"
                  :aria-checked="draftOutboundMode === mode ? 'true' : 'false'"
                  :class="{ selected: draftOutboundMode === mode, disabled: !providerSupportedModes.includes(mode) }"
                  :disabled="!providerSupportedModes.includes(mode)"
                  :data-testid="`outbound-mode-${mode}`"
                  @click="selectOutboundMode(mode)"
                >
                  <strong>{{ outboundModeCardTitle(mode) }}</strong>
                  <small>{{ outboundModeCardHint(mode) }}</small>
                  <span v-if="!providerSupportedModes.includes(mode)" class="connection-outbound-card-disabled">{{
                    t("connections.providerUnsupported")
                  }}</span>
                </button>
              </div>
              <small v-if="connectionFormErrors.outboundMode" class="connection-field-error">{{
                connectionFormErrors.outboundMode
              }}</small>
              <div
                v-if="switchModePending"
                class="connection-impact-preview"
                data-testid="connection-impact-preview"
                role="dialog"
                :aria-label="t('connections.confirmModeSwitchAria')"
              >
                <strong>{{ t("connections.confirmModeSwitchTitle") }}</strong>
                <p v-if="impactLoading">{{ t("connections.impactLoading") }}</p>
                <template v-else>
                  <p>
                    {{ t("connections.impactBody") }}
                  </p>
                  <p class="connection-impact-stub">
                    {{ t("connections.impactSummary") }}
                  </p>
                  <div class="connection-impact-actions">
                    <button type="button" class="connection-secondary-button" @click="cancelModeSwitch">
                      {{ t("connections.cancel") }}
                    </button>
                    <button
                      type="button"
                      class="connection-primary-button"
                      :disabled="!impactProof"
                      @click="confirmModeSwitch"
                    >
                      {{ t("connections.confirmChange") }}
                    </button>
                  </div>
                </template>
              </div>
              <div
                v-if="draftOutboundMode === 'BROKER_OBO'"
                class="connection-auth-fields"
                data-testid="outbound-broker-fields"
              >
                <p class="connection-field-help">
                  {{ t("connections.brokerHelp") }}
                </p>
                <div class="connection-field-grid two">
                  <label class="connection-field">
                    <span>clientId <b class="connection-required-mark">*</b></span>
                    <input v-model="brokerClientId" class="mono" data-testid="broker-client-id" autocomplete="off" />
                    <small v-if="connectionFormErrors['broker.clientId']" class="connection-field-error">{{
                      connectionFormErrors["broker.clientId"]
                    }}</small>
                  </label>
                  <label class="connection-field">
                    <span>scopes</span>
                    <input
                      v-model="brokerScopesText"
                      class="mono"
                      data-testid="broker-scopes"
                      :placeholder="t('connections.scopesPlaceholder')"
                      autocomplete="off"
                    />
                  </label>
                </div>
                <label class="connection-field">
                  <span
                    >{{ t("connections.machineCredential") }}
                    <b
                      v-if="!draftConnection.machineCredentialConfigured && !draftConnection.credentialConfigured"
                      class="connection-required-mark"
                      >*</b
                    ></span
                  >
                  <input
                    ref="machineCredentialInput"
                    type="password"
                    class="mono"
                    data-testid="broker-machine-credential"
                    autocomplete="new-password"
                    :placeholder="
                      draftConnection.machineCredentialConfigured || draftConnection.credentialConfigured
                        ? t('connections.machineCredentialKeep')
                        : t('connections.machineCredentialOnce')
                    "
                  />
                  <small>{{
                    t("connections.machineCredentialHelp", {
                      value:
                        draftConnection.machineCredentialConfigured || draftConnection.credentialConfigured
                          ? t("connections.yes")
                          : t("connections.no"),
                    })
                  }}</small>
                  <small v-if="connectionFormErrors['broker.machineCredential']" class="connection-field-error">{{
                    connectionFormErrors["broker.machineCredential"]
                  }}</small>
                </label>
              </div>
              <div
                v-else-if="draftOutboundMode === 'REQUEST_PASSTHROUGH'"
                class="connection-auth-fields"
                data-testid="outbound-passthrough-fields"
              >
                <p class="connection-field-help">
                  {{ t("connections.passthroughHelp") }}
                </p>
                <label class="connection-field">
                  <span>maxResidenceSeconds</span>
                  <input
                    v-model.number="passthroughMaxResidence"
                    type="number"
                    min="1"
                    max="3600"
                    class="mono"
                    data-testid="passthrough-max-residence"
                  />
                  <small>{{ t("connections.passthroughResidenceHelp") }}</small>
                </label>
              </div>
            </div>
          </section>
          <section class="connection-form-section connection-verification-section">
            <button
              data-testid="connection-verification-toggle"
              class="connection-disclosure-trigger"
              type="button"
              :aria-expanded="verificationSectionOpen ? 'true' : 'false'"
              aria-controls="connection-verification-fields"
              @click="toggleVerificationSection"
            >
              <span class="connection-disclosure-copy">
                <span class="connection-disclosure-icon verification" aria-hidden="true">
                  <i class="fa-solid fa-vial-circle-check" />
                </span>
                <span>
                  <strong>{{ t("connections.verificationSection") }}</strong>
                  <small data-testid="verification-path-summary">{{ verificationPathDisplay }}</small>
                </span>
              </span>
              <i class="fa-solid fa-chevron-down" :class="{ open: verificationSectionOpen }" />
            </button>
            <div v-if="verificationSectionOpen" id="connection-verification-fields" class="connection-disclosure-body">
              <div class="connection-field-grid two">
                <div class="connection-field dropdown" @click.stop>
                  <span>{{ t("connections.verificationMethod") }}</span>
                  <button
                    data-dropdown-trigger="verificationMethod"
                    class="connection-reference-select mono"
                    type="button"
                    disabled
                    :aria-label="
                      t('connections.verificationMethodAria', {
                        method: draftConnection.protocolConfig.verificationMethod || 'GET',
                      })
                    "
                    aria-haspopup="listbox"
                    :aria-expanded="connectionDropdowns.verificationMethod ? 'true' : 'false'"
                    aria-controls="connection-verification-method-menu"
                    @click="toggleConnectionDropdown('verificationMethod')"
                    @keydown="openConnectionDropdownFromKeyboard($event, 'verificationMethod')"
                  >
                    <span>{{ draftConnection.protocolConfig.verificationMethod || "GET" }}</span>
                    <i class="fa-solid fa-chevron-down" :class="{ open: connectionDropdowns.verificationMethod }" />
                  </button>
                  <div
                    v-if="connectionDropdowns.verificationMethod"
                    id="connection-verification-method-menu"
                    class="connection-select-menu"
                    role="listbox"
                  >
                    <button
                      v-for="(option, index) in verificationMethodOptions"
                      :key="option.value"
                      class="connection-select-option mono"
                      :class="{ selected: draftConnection.protocolConfig.verificationMethod === option.value }"
                      type="button"
                      role="option"
                      tabindex="-1"
                      :aria-selected="
                        draftConnection.protocolConfig.verificationMethod === option.value ? 'true' : 'false'
                      "
                      @click="selectVerificationMethod(option.value)"
                      @keydown="handleConnectionOptionKeydown($event, 'verificationMethod', index)"
                    >
                      {{ option.label }}
                      <i
                        v-if="draftConnection.protocolConfig.verificationMethod === option.value"
                        class="fa-solid fa-check"
                      />
                    </button>
                  </div>
                </div>
                <label class="connection-field"
                  ><span>{{ t("connections.verificationPathReadonly") }}</span
                  ><input :value="draftConnection.protocolConfig.verificationPath" class="mono" disabled readonly
                /></label>
                <label class="connection-field"
                  ><span>{{ t("connections.expectedStatusReadonly") }}</span
                  ><input :value="draftConnection.protocolConfig.expectedStatus" class="mono" disabled readonly
                /></label>
                <label class="connection-field"
                  ><span>{{ t("connections.expectedContainsReadonly") }}</span
                  ><input
                    :value="draftConnection.protocolConfig.expectedResponseContains"
                    class="mono"
                    disabled
                    readonly
                /></label>
              </div>
              <div class="connection-address-preview">
                <i class="fa-solid fa-vial" /><span
                  ><small>{{ t("connections.actualVerificationRequest") }}</small
                  ><b
                    >{{ draftConnection.protocolConfig.verificationMethod || "GET" }}
                    {{ draftConnectionVerificationPreview }}</b
                  ></span
                >
              </div>
              <div
                v-if="connectionVerificationPhase !== 'idle'"
                data-testid="connection-form-verification-result"
                class="connection-form-verification-result"
                :class="{
                  pending: connectionVerificationPhase === 'saving' || connectionVerificationPhase === 'verifying',
                  passed: connectionVerificationPhase === 'passed',
                  failed:
                    connectionVerificationPhase === 'saveFailed' ||
                    connectionVerificationPhase === 'verificationFailed',
                }"
                role="status"
                aria-live="polite"
              >
                <strong>{{ formVerificationResultTitle }}</strong>
                <p v-if="formSubmitError">{{ formSubmitError }}</p>
                <div v-if="formVerificationChecks.length" class="connection-form-checks">
                  <span
                    v-for="check in formVerificationChecks"
                    :key="check.label"
                    :class="{ passed: check.passed, failed: !check.passed }"
                  >
                    <i :class="check.passed ? 'fa-solid fa-circle-check' : 'fa-solid fa-circle-exclamation'" />
                    <b>{{ check.label }}</b>
                    <small>{{ check.desc }}</small>
                  </span>
                </div>
              </div>
            </div>
          </section>
          <section class="connection-form-section connection-advanced-section">
            <button
              data-testid="connection-advanced-toggle"
              class="connection-disclosure-trigger"
              type="button"
              :aria-expanded="advancedSectionOpen ? 'true' : 'false'"
              aria-controls="connection-advanced-fields"
              @click="toggleAdvancedSection"
            >
              <span class="connection-disclosure-copy">
                <span class="connection-disclosure-icon advanced" aria-hidden="true">
                  <i class="fa-solid fa-sliders" />
                </span>
                <span>
                  <strong>{{ t("connections.advancedSettings") }}</strong>
                  <small>{{ t("connections.advancedSettingsHelp") }}</small>
                </span>
              </span>
              <i class="fa-solid fa-chevron-down" :class="{ open: advancedSectionOpen }" />
            </button>
            <div v-if="advancedSectionOpen" id="connection-advanced-fields" class="connection-disclosure-body">
              <div class="connection-field-grid two">
                <label class="connection-field"
                  ><span>{{ t("connections.portReadonly") }}</span
                  ><input :value="draftConnection.protocolConfig.port" class="mono" disabled readonly
                /></label>
                <label class="connection-field"
                  ><span>{{ t("connections.basePathReadonly") }}</span
                  ><input :value="draftConnection.protocolConfig.basePath" class="mono" disabled readonly
                /></label>
              </div>
              <div v-if="showsTokenFieldPaths" class="connection-field-grid two">
                <label class="connection-field"
                  ><span>{{ t("connections.accessTokenField") }}</span
                  ><input
                    v-model="draftConnection.authConfig.accessTokenPath"
                    class="mono"
                    placeholder="access_token / token"
                /></label>
                <label class="connection-field"
                  ><span>{{ t("connections.refreshTokenField") }}</span
                  ><input
                    v-model="draftConnection.authConfig.refreshTokenPath"
                    class="mono"
                    placeholder="refresh_token"
                /></label>
                <label class="connection-field"
                  ><span>{{ t("connections.expiresField") }}</span
                  ><input
                    v-model="draftConnection.authConfig.expiresPath"
                    class="mono"
                    placeholder="expires_in / expires_at"
                /></label>
              </div>
              <div v-if="needsRefreshConfig" class="connection-field dropdown" @click.stop>
                <span>{{ t("connections.afterCredentialExpires") }}</span>
                <button
                  data-dropdown-trigger="refreshMode"
                  class="connection-reference-select"
                  type="button"
                  :aria-label="t('connections.afterCredentialExpiresAria', { label: computedRefreshModeLabel })"
                  aria-haspopup="listbox"
                  :aria-expanded="connectionDropdowns.refreshMode ? 'true' : 'false'"
                  aria-controls="connection-refresh-mode-menu"
                  @click="toggleConnectionDropdown('refreshMode')"
                  @keydown="openConnectionDropdownFromKeyboard($event, 'refreshMode')"
                >
                  <span>{{ computedRefreshModeLabel }}</span>
                  <i class="fa-solid fa-chevron-down" :class="{ open: connectionDropdowns.refreshMode }" />
                </button>
                <div
                  v-if="connectionDropdowns.refreshMode"
                  id="connection-refresh-mode-menu"
                  class="connection-select-menu"
                  role="listbox"
                >
                  <button
                    v-for="(option, index) in refreshModeOptions"
                    :key="option.value"
                    class="connection-select-option"
                    :class="{ selected: draftConnection.authConfig.refreshMode === option.value }"
                    type="button"
                    role="option"
                    tabindex="-1"
                    :aria-selected="draftConnection.authConfig.refreshMode === option.value ? 'true' : 'false'"
                    @click="selectRefreshMode(option.value)"
                    @keydown="handleConnectionOptionKeydown($event, 'refreshMode', index)"
                  >
                    {{ option.label }}
                    <i v-if="draftConnection.authConfig.refreshMode === option.value" class="fa-solid fa-check" />
                  </button>
                </div>
              </div>
              <label v-if="draftConnection.authConfig.refreshMode === 'dedicated'" class="connection-field"
                ><span>{{ t("connections.dedicatedRefreshUrl") }}</span
                ><input
                  v-model="draftConnection.authConfig.refreshUrl"
                  class="mono"
                  :placeholder="t('connections.dedicatedRefreshPlaceholder')"
              /></label>
            </div>
          </section>
        </div>
      </div>
      <ConnectionFormActions />
    </div>
  </section>
</template>
