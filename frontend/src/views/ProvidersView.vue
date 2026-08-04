<script setup lang="ts">
import "./providers-page.css";
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";

import AppSelect from "../components/AppSelect.vue";
import ManagementList, { type ManagementListColumn } from "../components/ManagementList.vue";
import ManagementPageHeader from "../components/ManagementPageHeader.vue";
import ManagementRowActions, { type ManagementRowAction } from "../components/ManagementRowActions.vue";
import ManagementSegmentedFilter from "../components/ManagementSegmentedFilter.vue";
import WorkspaceContextState from "../components/WorkspaceContextState.vue";
import { useProvidersStore } from "../stores/providers";
import { useWorkspaceStore } from "../stores/workspaces";
import type {
  CapabilityProvider,
  ProviderAsset,
  ProviderAuthContract,
  ProviderAuthField,
  ProviderAuthScheme,
} from "../types/domain";
import {
  authSchemeSummary,
  defaultOAuthContract,
  noAuthenticationContract,
  providerAuthContract,
} from "../utils/provider-auth";
import { buildOutboundIdentityContract } from "./provider-outbound-identity";

type ProviderStatusFilter = "ALL" | "ACTIVE" | "ERROR" | "DISABLED";
type AuthEditorMode = "" | "NONE" | "OAUTH2_CLIENT";
type OAuthFieldKind = "TEXT" | "SELECT";
type TokenParameterSource = "FIELD" | "VALUE";
type SortOrder = "asc" | "desc";

interface OAuthFieldDraft {
  id: string;
  key: string;
  label: string;
  kind: OAuthFieldKind;
  required: boolean;
  placeholder: string;
  help: string;
  optionsText: string;
}

interface TokenParameterDraft {
  id: string;
  name: string;
  source: TokenParameterSource;
  field: string;
  value: string;
  required: boolean;
}

interface ProviderFormDraft {
  id: string;
  name: string;
  serviceBaseUrl: string;
  documentUrl: string;
  sourceRevision: string;
  allowedCIDRs: string;
  verificationMethod: "GET" | "HEAD" | "POST";
  verificationPath: string;
  expectedStatuses: string;
  discoveryMode: string;
  /** @deprecated legacy editor; dual-mode uses supportedModes instead. */
  authMode: AuthEditorMode;
  legacyAuthentication: boolean;
  schemeKey: string;
  displayName: string;
  description: string;
  tokenUrlTemplate: string;
  clientAuthMethod: "client_secret_basic" | "client_secret_post";
  extraFields: OAuthFieldDraft[];
  tokenParameters: TokenParameterDraft[];
  accessTokenPath: string;
  tokenTypePath: string;
  expiresInPath: string;
  renewalTokenPath: string;
  injectionHeaderName: string;
  injectionPrefix: string;
  refreshStrategy: "CLIENT_CREDENTIALS" | "REFRESH_TOKEN";
  preservedSchemes: ProviderAuthScheme[];
  /** Dual-mode outbound-identity.v1 (post hard-cutover). */
  supportBrokerObo: boolean;
  supportRequestPassthrough: boolean;
  brokerTokenEndpoint: string;
  brokerAudience: string;
  brokerAllowedScopes: string;
  businessInjectionHeader: string;
  businessInjectionPrefix: string;
}

interface ProviderSyncOutcome {
  id?: string;
  status: string;
  discoveredCount: number;
  changedCount: number;
  errorSummary: Record<string, unknown>;
}

let draftRowSequence = 0;

const { t, locale } = useI18n();
const providerStore = useProvidersStore();
const workspaces = useWorkspaceStore();
const canEditWorkspace = computed(() =>
  workspaces.can(workspaces.activeWorkspaceId || workspaces.items[0]?.id || "", "EDIT"),
);
const hasWorkspaceContext = computed(() => Boolean(workspaces.activeWorkspaceId || workspaces.items[0]?.id));

const loading = ref(false);
const hasLoaded = ref(false);
const loadError = ref<string | null>(null);
const search = ref("");
const statusFilter = ref<ProviderStatusFilter>("ALL");
const page = ref(1);
const pageSize = ref(20);
const sortBy = ref<string>();
const sortOrder = ref<SortOrder>();

const expandedProviderId = ref("");
const assetLoadingProviderIds = ref<string[]>([]);
const assetErrors = ref<Record<string, string>>({});
const materializingAssetId = ref("");
const syncingProviderIds = ref<string[]>([]);
const syncResults = ref<Record<string, ProviderSyncOutcome>>({});

const editorVisible = ref(false);
const editorMode = ref<"create" | "edit">("create");
const editingProvider = ref<CapabilityProvider | null>(null);
const providerDraft = ref<ProviderFormDraft>(emptyProviderDraft());
const providerNameInput = ref<HTMLInputElement | null>(null);
const editorPanel = ref<HTMLElement | null>(null);
const saving = ref(false);
const formError = ref("");
/** Block-level zero-mode error for outbound identity (AC-09). */
const identityModeError = ref("");
const identityModeGroupRef = ref<HTMLElement | null>(null);

const pendingDeleteProvider = ref<CapabilityProvider | null>(null);
const deleteConfirmText = ref("");
const deleteError = ref("");
const deleting = ref(false);

const actionNote = ref("");
const actionTone = ref<"success" | "warning" | "error">("success");

const statusOptions = computed(() => [
  { label: t("providers.statusAll"), value: "ALL" },
  { label: t("providers.statusActive"), value: "ACTIVE" },
  { label: t("providers.statusError"), value: "ERROR" },
  { label: t("providers.statusDisabled"), value: "DISABLED" },
]);
const discoveryModeOptions = computed(() => [
  { label: t("providers.discoveryManual"), value: "MANUAL" },
  { label: t("providers.discoveryOnDemand"), value: "ON_DEMAND" },
  ...(providerDraft.value.discoveryMode === "POLLING"
    ? [{ label: t("providers.discoveryPollingLegacy"), value: "POLLING", disabled: true }]
    : []),
]);
const verificationMethodOptions = ["GET", "HEAD", "POST"].map((value) => ({ label: value, value }));
const _clientAuthMethodOptions = ["client_secret_basic", "client_secret_post"].map((value) => ({
  label: value,
  value,
}));
const _oauthFieldKindOptions = computed(() => [
  { label: t("providers.fieldKindText"), value: "TEXT" },
  { label: t("providers.fieldKindSelect"), value: "SELECT" },
]);
const _tokenParameterSourceOptions = computed(() => [
  { label: t("providers.tokenParamFromField"), value: "FIELD" },
  { label: t("providers.tokenParamFixedValue"), value: "VALUE" },
]);
const _refreshStrategyOptions = computed(() => [
  { label: t("providers.refreshClientCredentials"), value: "CLIENT_CREDENTIALS" },
  { label: t("providers.refreshToken"), value: "REFRESH_TOKEN" },
]);

const providerColumns = computed<ManagementListColumn<CapabilityProvider>[]>(() => [
  {
    key: "name",
    label: t("providers.colProvider"),
    width: 244,
    sortable: true,
    sortKey: "name",
    getValue: (provider) => provider.name,
  },
  {
    key: "status",
    label: t("providers.colStatus"),
    width: 108,
    align: "center",
    headerAlign: "center",
    sortable: true,
    sortKey: "status",
    getValue: (provider) => provider.status,
  },
  {
    key: "serviceBaseUrl",
    label: t("providers.colServiceBaseUrl"),
    width: 250,
    hidable: true,
    getValue: providerServiceBaseUrl,
  },
  {
    key: "documentUrl",
    label: t("providers.colDocumentUrl"),
    width: 270,
    hidable: true,
    getValue: providerDocumentUrl,
  },
  {
    key: "authentication",
    label: t("providers.colAuthentication"),
    width: 190,
    hidable: true,
    getValue: authSchemeSummary,
  },
  {
    key: "lastSyncedAt",
    label: t("providers.colLastSynced"),
    width: 164,
    hidable: true,
    sortable: true,
    sortKey: "lastSyncedAt",
    getValue: (provider) => provider.lastSyncedAt || "",
  },
  { key: "actions", label: t("providers.colActions"), width: 68, align: "right", headerAlign: "center" },
]);

const filteredProviders = computed(() => {
  const needle = search.value.trim().toLocaleLowerCase();
  return providerStore.providers.filter((provider) => {
    if (statusFilter.value !== "ALL" && provider.status !== statusFilter.value) return false;
    if (!needle) return true;
    return [
      provider.name,
      provider.kind,
      provider.status,
      providerServiceBaseUrl(provider),
      providerDocumentUrl(provider),
      authSchemeSummary(provider),
    ].some((value) => value.toLocaleLowerCase().includes(needle));
  });
});

const sortedProviders = computed(() => {
  if (!sortBy.value || !sortOrder.value) return filteredProviders.value;
  const rows = [...filteredProviders.value];
  rows.sort((left, right) => {
    const leftValue = providerSortValue(left, sortBy.value || "");
    const rightValue = providerSortValue(right, sortBy.value || "");
    const comparison = leftValue.localeCompare(rightValue, "zh-Hans");
    return sortOrder.value === "asc" ? comparison : -comparison;
  });
  return rows;
});

const providerRows = computed(() =>
  sortedProviders.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
);
const providerPagination = computed(() => ({
  page: page.value,
  pageSize: pageSize.value,
  total: sortedProviders.value.length,
  pageSizeOptions: [10, 20, 50],
}));
const availableOAuthFieldKeys = computed(() => [
  "clientId",
  "scope",
  ...providerDraft.value.extraFields.map((field) => field.key.trim()).filter(Boolean),
]);
const _availableOAuthFieldOptions = computed(() => [
  { label: t("providers.pleaseSelect"), value: "" },
  ...availableOAuthFieldKeys.value.map((fieldKey) => ({ label: fieldKey, value: fieldKey })),
]);
const deleteConfirmMatches = computed(() => deleteConfirmText.value.trim() === pendingDeleteProvider.value?.name);

onMounted(async () => {
  window.addEventListener("keydown", handleGlobalKeydown);
  try {
    if (!workspaces.items.length) await workspaces.load();
    if (hasWorkspaceContext.value) await loadProviders();
    else hasLoaded.value = true;
  } catch (error) {
    loadError.value = errorMessage(error, t("providers.loadFailed"));
    hasLoaded.value = true;
  }
});

onBeforeUnmount(() => window.removeEventListener("keydown", handleGlobalKeydown));

async function loadProviders() {
  if (!hasWorkspaceContext.value) return;
  loading.value = true;
  loadError.value = null;
  try {
    await providerStore.loadProviders();
    clampPage();
  } catch (error) {
    loadError.value = errorMessage(error, t("providers.loadFailed"));
  } finally {
    loading.value = false;
    hasLoaded.value = true;
  }
}

function updateSearch(value: string) {
  search.value = value;
  page.value = 1;
}

function updateStatusFilter(value: string) {
  statusFilter.value = value as ProviderStatusFilter;
  page.value = 1;
}

function resetFilters() {
  search.value = "";
  statusFilter.value = "ALL";
  page.value = 1;
}

function changePage(value: { page: number; pageSize: number }) {
  page.value = value.pageSize === pageSize.value ? value.page : 1;
  pageSize.value = value.pageSize;
}

function changeSort(value: { sortBy?: string; sortOrder?: SortOrder }) {
  sortBy.value = value.sortBy;
  sortOrder.value = value.sortOrder;
  page.value = 1;
}

function clampPage() {
  const lastPage = Math.max(1, Math.ceil(filteredProviders.value.length / pageSize.value));
  page.value = Math.min(page.value, lastPage);
}

function providerSortValue(provider: CapabilityProvider, key: string) {
  if (key === "name") return provider.name;
  if (key === "status") return provider.status;
  if (key === "lastSyncedAt") return provider.lastSyncedAt || "";
  return "";
}

function providerServiceBaseUrl(provider: CapabilityProvider) {
  const endpoint = asRecord(provider.endpointConfig);
  return firstString(endpoint.serviceBaseUrl, endpoint.baseUrl, endpoint.url) || t("providers.notConfigured");
}

function providerDocumentUrl(provider: CapabilityProvider) {
  const endpoint = asRecord(provider.endpointConfig);
  const discovery = asRecord(endpoint.discovery);
  return firstString(discovery.documentUrl, endpoint.sourceUri) || t("providers.notConfiguredNoDiscovery");
}

function hasProviderDocument(provider: CapabilityProvider) {
  const endpoint = asRecord(provider.endpointConfig);
  const discovery = asRecord(endpoint.discovery);
  return Boolean(firstString(discovery.documentUrl, endpoint.sourceUri));
}

function canSyncProvider(provider: CapabilityProvider) {
  return hasProviderDocument(provider) && provider.discoveryMode !== "MANUAL";
}

function providerSyncTitle(provider: CapabilityProvider) {
  if (!hasProviderDocument(provider)) return t("providers.syncTitleNoDoc");
  if (provider.discoveryMode === "MANUAL") return t("providers.syncTitleManual");
  return t("providers.syncTitleOk");
}

function providerVerificationSummary(provider: CapabilityProvider) {
  const verification = asRecord(asRecord(provider.endpointConfig).verification);
  const statuses = Array.isArray(verification.expectedStatuses) ? verification.expectedStatuses.join(", ") : "200, 204";
  return `${firstString(verification.method) || "GET"} ${firstString(verification.path) || "/"} · ${statuses}`;
}

function statusLabel(status: string) {
  if (status === "ACTIVE") return t("providers.statusActive");
  if (status === "ERROR") return t("providers.statusError");
  if (status === "DISABLED") return t("providers.statusDisabled");
  return status || t("providers.statusUnknown");
}

function statusTone(status: string) {
  if (status === "ACTIVE") return "active";
  if (status === "ERROR") return "error";
  if (status === "DISABLED") return "disabled";
  return "neutral";
}

function formatDate(value?: string) {
  if (!value) return t("providers.neverSynced");
  const parsed = new Date(value);
  const dateLocale = locale.value === "en" ? "en-US" : "zh-CN";
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString(dateLocale);
}

function isSyncing(providerId: string) {
  return syncingProviderIds.value.includes(providerId);
}

function isLoadingAssets(providerId: string) {
  return assetLoadingProviderIds.value.includes(providerId);
}

function providerMenuActions(provider: CapabilityProvider): ManagementRowAction[] {
  const syncing = isSyncing(provider.id);
  const syncAvailable = canSyncProvider(provider);
  return [
    {
      key: "edit",
      label: t("providers.editNamed", { name: provider.name }),
      shortLabel: t("providers.edit"),
      icon: "fa-solid fa-pen-to-square",
    },
    {
      key: "sync",
      label: t("providers.syncNamed", { name: provider.name }),
      shortLabel: t("providers.sync"),
      icon: "fa-solid fa-rotate",
      tone: "primary",
      disabled: !syncAvailable,
      loading: syncing,
      disabledReason: !syncAvailable ? providerSyncTitle(provider) : undefined,
    },
    {
      key: "assets",
      label: t("providers.viewAssetsNamed", { name: provider.name }),
      shortLabel: t("providers.viewAssets"),
      icon: "fa-solid fa-cubes",
    },
    {
      key: "delete",
      label: t("providers.deleteNamed", { name: provider.name }),
      shortLabel: t("providers.delete"),
      icon: "fa-solid fa-trash-can",
      tone: "danger",
    },
  ];
}

function handleProviderRowAction(actionKey: string, provider: CapabilityProvider) {
  if (actionKey === "edit") {
    openEditEditor(provider);
    return;
  }
  if (actionKey === "sync") {
    void syncProvider(provider);
    return;
  }
  if (actionKey === "assets") {
    void toggleAssets(provider);
    return;
  }
  if (actionKey === "delete") requestDeleteProvider(provider);
}

async function syncProvider(provider: CapabilityProvider) {
  if (isSyncing(provider.id)) return;
  if (!canSyncProvider(provider)) {
    showActionNote(t("providers.syncDisabledNote", { name: provider.name }), "warning");
    return;
  }
  syncingProviderIds.value = [...syncingProviderIds.value, provider.id];
  delete syncResults.value[provider.id];
  try {
    const raw = (await providerStore.syncProvider(provider.id)) as ProviderSyncOutcome;
    const result = normalizeSyncOutcome(raw);
    syncResults.value = { ...syncResults.value, [provider.id]: result };
    if (result.status === "SUCCEEDED") {
      showActionNote(
        t("providers.syncSucceeded", {
          name: provider.name,
          discovered: result.discoveredCount,
          changed: result.changedCount,
        }),
      );
      if (expandedProviderId.value === provider.id) await loadAssets(provider.id);
    } else {
      showActionNote(t("providers.syncFailed", { name: provider.name, message: syncErrorText(result) }), "error");
    }
  } catch (error) {
    const message = errorMessage(error, t("providers.syncRequestFailed"));
    syncResults.value = {
      ...syncResults.value,
      [provider.id]: { status: "FAILED", discoveredCount: 0, changedCount: 0, errorSummary: { message } },
    };
    showActionNote(t("providers.syncFailed", { name: provider.name, message }), "error");
  } finally {
    syncingProviderIds.value = syncingProviderIds.value.filter((id) => id !== provider.id);
  }
}

async function toggleAssets(provider: CapabilityProvider) {
  if (expandedProviderId.value === provider.id) {
    expandedProviderId.value = "";
    return;
  }
  expandedProviderId.value = provider.id;
  await loadAssets(provider.id);
}

async function loadAssets(providerId: string) {
  if (isLoadingAssets(providerId)) return;
  assetLoadingProviderIds.value = [...assetLoadingProviderIds.value, providerId];
  assetErrors.value = { ...assetErrors.value, [providerId]: "" };
  try {
    await providerStore.loadProviderAssets(providerId);
  } catch (error) {
    assetErrors.value = { ...assetErrors.value, [providerId]: errorMessage(error, t("providers.loadAssetsFailed")) };
  } finally {
    assetLoadingProviderIds.value = assetLoadingProviderIds.value.filter((id) => id !== providerId);
  }
}

async function materializeAsset(provider: CapabilityProvider, asset: ProviderAsset) {
  if (materializingAssetId.value || asset.materializedCapabilityId) return;
  materializingAssetId.value = asset.id;
  try {
    await providerStore.materializeProviderAsset(provider.id, asset.id);
    showActionNote(t("providers.materializedOk", { name: asset.name }));
  } catch (error) {
    showActionNote(errorMessage(error, t("providers.materializeFailed", { name: asset.name })), "error");
  } finally {
    materializingAssetId.value = "";
  }
}

function openCreateEditor() {
  identityModeError.value = "";
  editorMode.value = "create";
  editingProvider.value = null;
  providerDraft.value = emptyProviderDraft();
  formError.value = "";
  editorVisible.value = true;
  focusProviderName();
}

function openEditEditor(provider: CapabilityProvider) {
  editorMode.value = "edit";
  editingProvider.value = provider;
  providerDraft.value = draftFromProvider(provider);
  formError.value = "";
  identityModeError.value = "";
  editorVisible.value = true;
  focusProviderName();
}

function closeEditor() {
  if (saving.value) return;
  dismissEditor();
}

function dismissEditor() {
  editorVisible.value = false;
  editingProvider.value = null;
  formError.value = "";
  identityModeError.value = "";
}

function focusProviderName() {
  void nextTick(() => providerNameInput.value?.focus());
}

function _handleAuthModeChange() {
  formError.value = "";
  if (providerDraft.value.authMode !== "OAUTH2_CLIENT") return;
  if (!providerDraft.value.schemeKey) providerDraft.value.schemeKey = "oauth2-client";
  if (!providerDraft.value.displayName) providerDraft.value.displayName = "OAuth2 Client Credentials";
  if (!providerDraft.value.accessTokenPath) providerDraft.value.accessTokenPath = "access_token";
  if (!providerDraft.value.injectionHeaderName) providerDraft.value.injectionHeaderName = "Authorization";
}

function _addOAuthField() {
  providerDraft.value.extraFields.push({
    id: nextDraftRowId("field"),
    key: "",
    label: "",
    kind: "TEXT",
    required: false,
    placeholder: "",
    help: "",
    optionsText: "",
  });
}

function _removeOAuthField(id: string) {
  providerDraft.value.extraFields = providerDraft.value.extraFields.filter((field) => field.id !== id);
  providerDraft.value.tokenParameters.forEach((parameter) => {
    if (parameter.field && !availableOAuthFieldKeys.value.includes(parameter.field)) parameter.field = "";
  });
}

function _addTokenParameter() {
  providerDraft.value.tokenParameters.push({
    id: nextDraftRowId("parameter"),
    name: "",
    source: "FIELD",
    field: "",
    value: "",
    required: false,
  });
}

function _removeTokenParameter(id: string) {
  providerDraft.value.tokenParameters = providerDraft.value.tokenParameters.filter((parameter) => parameter.id !== id);
}

function _changeTokenParameterSource(parameter: TokenParameterDraft) {
  if (parameter.source === "FIELD") parameter.value = "";
  else parameter.field = "";
}

async function saveProvider() {
  if (saving.value) return;
  const validationError = validateProviderDraft(providerDraft.value);
  if (validationError) {
    formError.value = validationError;
    if (!providerDraft.value.supportBrokerObo && !providerDraft.value.supportRequestPassthrough) {
      identityModeError.value = t("providers.selectAtLeastOne");
      await nextTick();
      const focusTarget =
        identityModeGroupRef.value?.querySelector<HTMLInputElement>('input[type="checkbox"]') ||
        identityModeGroupRef.value;
      focusTarget?.focus?.();
    } else {
      identityModeError.value = "";
    }
    return;
  }
  saving.value = true;
  formError.value = "";
  identityModeError.value = "";
  try {
    const provider = providerFromDraft(providerDraft.value, editingProvider.value);
    const saved =
      editorMode.value === "edit"
        ? await providerStore.updateProvider(provider)
        : await providerStore.createProvider(provider);
    showActionNote(
      t("providers.providerSaved", {
        name: saved.name,
        action: editorMode.value === "edit" ? t("providers.actionUpdated") : t("providers.actionCreated"),
      }),
    );
    dismissEditor();
  } catch (error) {
    formError.value = errorMessage(error, t("providers.saveFailed"));
  } finally {
    saving.value = false;
  }
}

function clearIdentityModeErrorIfResolved() {
  if (providerDraft.value.supportBrokerObo || providerDraft.value.supportRequestPassthrough) {
    identityModeError.value = "";
  }
}

function requestDeleteProvider(provider: CapabilityProvider) {
  pendingDeleteProvider.value = provider;
  deleteConfirmText.value = "";
  deleteError.value = "";
}

function closeDeleteDialog() {
  if (deleting.value) return;
  dismissDeleteDialog();
}

function dismissDeleteDialog() {
  pendingDeleteProvider.value = null;
  deleteConfirmText.value = "";
  deleteError.value = "";
}

async function confirmDeleteProvider() {
  const provider = pendingDeleteProvider.value;
  if (!provider || deleting.value || !deleteConfirmMatches.value) return;
  deleting.value = true;
  deleteError.value = "";
  try {
    await providerStore.deleteProvider(provider.id);
    if (expandedProviderId.value === provider.id) expandedProviderId.value = "";
    showActionNote(t("providers.providerDeleted", { name: provider.name }), "warning");
    dismissDeleteDialog();
    clampPage();
  } catch (error) {
    deleteError.value = errorMessage(error, t("providers.deleteFailed"));
  } finally {
    deleting.value = false;
  }
}

function handleGlobalKeydown(event: KeyboardEvent) {
  if (event.key !== "Escape") return;
  if (pendingDeleteProvider.value) {
    event.preventDefault();
    closeDeleteDialog();
    return;
  }
  if (editorVisible.value) {
    event.preventDefault();
    closeEditor();
  }
}

function trapEditorFocus(event: KeyboardEvent) {
  if (event.key !== "Tab" || !editorPanel.value) return;
  const focusable = Array.from(
    editorPanel.value.querySelectorAll<HTMLElement>(
      "button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])",
    ),
  ).filter((element) => element.offsetParent !== null);
  if (!focusable.length) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

function emptyProviderDraft(): ProviderFormDraft {
  const oauth = defaultOAuthContract("").schemes[0];
  return {
    id: "",
    name: "",
    serviceBaseUrl: "",
    documentUrl: "",
    sourceRevision: "",
    allowedCIDRs: "",
    verificationMethod: "GET",
    verificationPath: "",
    expectedStatuses: "200, 204",
    discoveryMode: "MANUAL",
    authMode: "NONE",
    legacyAuthentication: false,
    schemeKey: oauth.key,
    displayName: oauth.displayName,
    description: oauth.description || "",
    tokenUrlTemplate: oauth.oauth2?.tokenUrlTemplate || "",
    clientAuthMethod: oauth.oauth2?.clientAuthMethod || "client_secret_basic",
    extraFields: [],
    tokenParameters: [],
    accessTokenPath: oauth.oauth2?.response.accessTokenPath || "access_token",
    tokenTypePath: oauth.oauth2?.response.tokenTypePath || "token_type",
    expiresInPath: oauth.oauth2?.response.expiresInPath || "expires_in",
    renewalTokenPath: oauth.oauth2?.response.renewalTokenPath || "",
    injectionHeaderName: oauth.oauth2?.injection.headerName || "Authorization",
    injectionPrefix: oauth.oauth2?.injection.prefix || "Bearer",
    refreshStrategy: oauth.oauth2?.refreshStrategy || "CLIENT_CREDENTIALS",
    preservedSchemes: [],
    supportBrokerObo: false,
    supportRequestPassthrough: true,
    brokerTokenEndpoint: "",
    brokerAudience: "",
    brokerAllowedScopes: "",
    businessInjectionHeader: "Authorization",
    businessInjectionPrefix: "Bearer",
  };
}

function draftFromProvider(provider: CapabilityProvider): ProviderFormDraft {
  const draft = emptyProviderDraft();
  const endpoint = asRecord(provider.endpointConfig);
  const discovery = asRecord(endpoint.discovery);
  const verification = asRecord(endpoint.verification);
  const egress = asRecord(endpoint.egress);
  draft.id = provider.id;
  draft.name = provider.name;
  draft.serviceBaseUrl = firstString(endpoint.serviceBaseUrl, endpoint.baseUrl, endpoint.url);
  draft.documentUrl = firstString(discovery.documentUrl, endpoint.sourceUri);
  draft.sourceRevision = firstString(discovery.sourceRevision);
  const configuredCIDRs = Array.isArray(egress.allowedCIDRs)
    ? egress.allowedCIDRs
    : Array.isArray(egress.AllowedCIDRs)
      ? egress.AllowedCIDRs
      : [];
  draft.allowedCIDRs = configuredCIDRs.filter((value): value is string => typeof value === "string").join("\n");
  draft.verificationMethod = (
    ["GET", "HEAD", "POST"].includes(firstString(verification.method).toUpperCase())
      ? firstString(verification.method).toUpperCase()
      : "GET"
  ) as ProviderFormDraft["verificationMethod"];
  draft.verificationPath = firstString(verification.path);
  draft.expectedStatuses =
    Array.isArray(verification.expectedStatuses) && verification.expectedStatuses.length
      ? verification.expectedStatuses.join(", ")
      : "200, 204";
  draft.discoveryMode = provider.discoveryMode || "ON_DEMAND";

  const outbound = asRecord((provider.driverConfig as Record<string, unknown> | undefined)?.outboundIdentity);
  if (outbound.schemaVersion === "outbound-identity.v1" || Array.isArray(outbound.supportedModes)) {
    const modes = Array.isArray(outbound.supportedModes)
      ? outbound.supportedModes.map((m) => String(m).toUpperCase())
      : [];
    draft.supportBrokerObo = modes.includes("BROKER_OBO");
    draft.supportRequestPassthrough = modes.includes("REQUEST_PASSTHROUGH");
    const broker = asRecord(outbound.brokerObo);
    const passthrough = asRecord(outbound.requestPassthrough);
    const injection = asRecord(broker.businessInjection || passthrough.businessInjection);
    draft.brokerTokenEndpoint = firstString(broker.tokenEndpoint);
    draft.brokerAudience = firstString(broker.audience);
    draft.brokerAllowedScopes = Array.isArray(broker.allowedScopes) ? broker.allowedScopes.map(String).join(" ") : "";
    draft.businessInjectionHeader = firstString(injection.headerName) || "Authorization";
    draft.businessInjectionPrefix = firstString(injection.prefix) || "Bearer";
    draft.legacyAuthentication = false;
    draft.authMode = "";
    return draft;
  }

  const contract = providerAuthContract(provider);
  if (!contract) {
    draft.authMode = "";
    draft.legacyAuthentication = true;
    return draft;
  }
  const scheme = contract.schemes.find((item) => item.key === contract.defaultSchemeKey) || contract.schemes[0];
  draft.preservedSchemes = contract.schemes.filter((item) => item.key !== scheme.key);
  draft.legacyAuthentication = true;
  if (scheme.type === "NONE") {
    draft.authMode = "NONE";
    return draft;
  }
  hydrateOAuthDraft(draft, scheme);
  return draft;
}

function hydrateOAuthDraft(draft: ProviderFormDraft, scheme: ProviderAuthScheme) {
  const oauth = scheme.oauth2;
  draft.authMode = "OAUTH2_CLIENT";
  draft.schemeKey = scheme.key;
  draft.displayName = scheme.displayName;
  draft.description = scheme.description || "";
  if (!oauth) return;
  draft.tokenUrlTemplate = oauth.tokenUrlTemplate;
  draft.clientAuthMethod = oauth.clientAuthMethod;
  draft.accessTokenPath = oauth.response.accessTokenPath;
  draft.tokenTypePath = oauth.response.tokenTypePath || "";
  draft.expiresInPath = oauth.response.expiresInPath || "";
  draft.renewalTokenPath = oauth.response.renewalTokenPath || "";
  draft.injectionHeaderName = oauth.injection.headerName;
  draft.injectionPrefix = oauth.injection.prefix || "";
  draft.refreshStrategy = oauth.refreshStrategy || "CLIENT_CREDENTIALS";
  const reserved = new Set([oauth.clientIdField, oauth.credentialField, oauth.scopeField].filter(Boolean));
  draft.extraFields = scheme.fields.filter((field) => !reserved.has(field.key)).map(fieldToDraft);
  draft.tokenParameters = (oauth.tokenParameters || []).map((parameter) => ({
    id: nextDraftRowId("parameter"),
    name: parameter.name,
    source: parameter.field ? "FIELD" : "VALUE",
    field: parameter.field || "",
    value: parameter.value || "",
    required: Boolean(parameter.required),
  }));
}

function fieldToDraft(field: ProviderAuthField): OAuthFieldDraft {
  return {
    id: nextDraftRowId("field"),
    key: field.key,
    label: field.label,
    kind: field.kind === "SELECT" ? "SELECT" : "TEXT",
    required: Boolean(field.required),
    placeholder: field.placeholder || "",
    help: field.help || "",
    optionsText: (field.options || []).map((option) => `${option.label}=${option.value}`).join("\n"),
  };
}

function providerFromDraft(draft: ProviderFormDraft, existing: CapabilityProvider | null): CapabilityProvider {
  const expectedStatuses = parseStatuses(draft.expectedStatuses);
  const documentUrl = draft.documentUrl.trim();
  const preservedEndpoint: Record<string, unknown> = { ...(existing?.endpointConfig || {}) };
  for (const key of [
    "schemaVersion",
    "serviceBaseUrl",
    "discovery",
    "verification",
    "sourceUri",
    "sourceRevision",
    "baseUrl",
    "url",
  ]) {
    delete preservedEndpoint[key];
  }
  const existingEgress = asRecord(preservedEndpoint.egress);
  const nextEgress: Record<string, unknown> = { ...existingEgress };
  delete nextEgress.allowedCIDRs;
  delete nextEgress.AllowedCIDRs;
  const allowedCIDRs = parseCIDRList(draft.allowedCIDRs);
  if (allowedCIDRs.length) nextEgress.allowedCIDRs = allowedCIDRs;
  if (Object.keys(nextEgress).length) preservedEndpoint.egress = nextEgress;
  else delete preservedEndpoint.egress;
  const endpointConfig: Record<string, unknown> = {
    ...preservedEndpoint,
    schemaVersion: 2,
    serviceBaseUrl: draft.serviceBaseUrl.trim().replace(/\/+$/, ""),
    ...(documentUrl
      ? {
          discovery: {
            documentUrl,
            ...(draft.sourceRevision.trim() ? { sourceRevision: draft.sourceRevision.trim() } : {}),
          },
        }
      : {}),
    verification: {
      method: draft.verificationMethod,
      ...(draft.verificationPath.trim() ? { path: normalizeVerificationPath(draft.verificationPath) } : {}),
      expectedStatuses,
    },
  };
  const driverConfig: Record<string, unknown> = { ...(existing?.driverConfig || {}) };
  // Hard-cutover: write outbound-identity.v1 only; reject legacy authentication for HTTP.
  delete driverConfig.authentication;
  driverConfig.outboundIdentity = buildOutboundIdentityContract(draft);
  return {
    id: existing?.id || "",
    name: draft.name.trim(),
    kind: "HTTP_OPENAPI",
    driverKey: existing?.driverKey || "http_openapi",
    transport: "HTTP",
    endpointConfig,
    driverConfig,
    discoveryMode: draft.discoveryMode || "ON_DEMAND",
    status: existing?.status || "ACTIVE",
    lastSyncedAt: existing?.lastSyncedAt,
    lastErrorCode: existing?.lastErrorCode,
    createdBy: existing?.createdBy || "",
    updatedBy: existing?.updatedBy || "",
    lockVersion: existing?.lockVersion || 0,
  };
}

function _buildAuthenticationContract(draft: ProviderFormDraft): ProviderAuthContract {
  if (draft.authMode === "NONE") {
    const contract = noAuthenticationContract();
    contract.schemes.push(...draft.preservedSchemes.filter((scheme) => scheme.key !== contract.defaultSchemeKey));
    return contract;
  }
  const fields: ProviderAuthField[] = [
    {
      key: "clientId",
      label: "Client ID",
      kind: "TEXT",
      required: true,
      placeholder: t("providers.oauthClientIdPlaceholder"),
    },
    {
      key: "clientSecret",
      label: "Client Secret",
      kind: "SECRET",
      required: true,
      help: t("providers.oauthClientSecretHelp"),
    },
    { key: "scope", label: "Scope", kind: "TEXT", placeholder: t("providers.oauthScopePlaceholder") },
    ...draft.extraFields.map(
      (field) =>
        ({
          key: field.key.trim(),
          label: field.label.trim(),
          kind: field.kind,
          required: field.required,
          ...(field.placeholder.trim() ? { placeholder: field.placeholder.trim() } : {}),
          ...(field.help.trim() ? { help: field.help.trim() } : {}),
          ...(field.kind === "SELECT" ? { options: parseOptions(field.optionsText) } : {}),
        }) as ProviderAuthField,
    ),
  ];
  return {
    version: "service-auth.v1",
    defaultSchemeKey: draft.schemeKey.trim(),
    schemes: [
      {
        key: draft.schemeKey.trim(),
        type: "OAUTH2_CLIENT",
        displayName: draft.displayName.trim(),
        ...(draft.description.trim() ? { description: draft.description.trim() } : {}),
        fields,
        oauth2: {
          tokenUrlTemplate: draft.tokenUrlTemplate.trim(),
          clientIdField: "clientId",
          credentialField: "clientSecret",
          clientAuthMethod: draft.clientAuthMethod,
          scopeField: "scope",
          tokenParameters: draft.tokenParameters.map((parameter) => ({
            name: parameter.name.trim(),
            ...(parameter.source === "FIELD" ? { field: parameter.field.trim() } : { value: parameter.value.trim() }),
            ...(parameter.required ? { required: true } : {}),
          })),
          response: {
            accessTokenPath: draft.accessTokenPath.trim(),
            ...(draft.tokenTypePath.trim() ? { tokenTypePath: draft.tokenTypePath.trim() } : {}),
            ...(draft.expiresInPath.trim() ? { expiresInPath: draft.expiresInPath.trim() } : {}),
            ...(draft.renewalTokenPath.trim() ? { renewalTokenPath: draft.renewalTokenPath.trim() } : {}),
          },
          injection: {
            headerName: draft.injectionHeaderName.trim(),
            ...(draft.injectionPrefix.trim() ? { prefix: draft.injectionPrefix.trim() } : {}),
          },
          refreshStrategy: draft.refreshStrategy,
        },
      },
      ...draft.preservedSchemes.filter((scheme) => scheme.key !== draft.schemeKey.trim()),
    ],
  };
}

function validateProviderDraft(draft: ProviderFormDraft) {
  if (!draft.name.trim()) return t("providers.validationNameRequired");
  if (!validServiceBaseURL(draft.serviceBaseUrl)) return t("providers.validationServiceBaseUrl");
  if (draft.documentUrl.trim() && !validHTTPURL(draft.documentUrl)) return t("providers.validationDocumentUrl");
  if (draft.discoveryMode !== "MANUAL" && !draft.documentUrl.trim()) return t("providers.validationDiscoveryNeedsDoc");
  if (
    draft.verificationPath.trim() &&
    (/^https?:\/\//i.test(draft.verificationPath.trim()) || draft.verificationPath.trim().startsWith("//"))
  ) {
    return t("providers.validationVerificationPath");
  }
  try {
    parseStatuses(draft.expectedStatuses);
  } catch {
    return t("providers.validationExpectedStatuses");
  }
  let allowedCIDRs: string[];
  try {
    allowedCIDRs = parseCIDRList(draft.allowedCIDRs, true);
  } catch {
    return t("providers.validationCidr");
  }
  const privateAddress = privateLiteralAddress(draft.serviceBaseUrl);
  const privateAddressAllowed = privateAddress.includes(":")
    ? allowedCIDRs.some((cidr) => cidr.includes(":"))
    : allowedCIDRs.some((cidr) => cidrContainsIPv4(cidr, privateAddress));
  if (privateAddress && !privateAddressAllowed) {
    const singleHostCIDR = privateAddress.includes(":") ? `${privateAddress}/128` : `${privateAddress}/32`;
    return t("providers.validationPrivateAddress", { address: privateAddress, cidr: singleHostCIDR });
  }
  if (!draft.supportBrokerObo && !draft.supportRequestPassthrough) {
    return t("providers.selectAtLeastOne");
  }
  if (!validHeaderName(draft.businessInjectionHeader || "Authorization")) {
    return t("providers.validationInjectionHeader");
  }
  if (draft.supportBrokerObo) {
    if (!validHTTPURL(draft.brokerTokenEndpoint)) return t("providers.validationBrokerEndpoint");
    if (!draft.brokerAudience.trim()) return t("providers.validationBrokerAudience");
  }
  return "";
}

function validHTTPURL(value: string) {
  try {
    const url = new URL(value.trim());
    return (
      ["http:", "https:"].includes(url.protocol) && Boolean(url.host) && !url.username && !url.password && !url.hash
    );
  } catch {
    return false;
  }
}

function validServiceBaseURL(value: string) {
  if (!validHTTPURL(value)) return false;
  const url = new URL(value.trim());
  return !url.search;
}

function _validTokenURLTemplate(value: string) {
  const trimmed = value.trim();
  if (!/^https?:\/\//i.test(trimmed) || /[\r\n]/.test(trimmed)) return false;
  const authority = trimmed.replace(/^https?:\/\//i, "").split(/[/?#]/, 1)[0];
  if (!authority || authority.includes("{{") || authority.includes("}}")) return false;
  const placeholders = [...trimmed.matchAll(/\{\{\s*([A-Za-z][A-Za-z0-9._-]{0,63})\s*\}\}/g)].map((match) => match[1]);
  const stripped = trimmed.replace(/\{\{\s*[A-Za-z][A-Za-z0-9._-]{0,63}\s*\}\}/g, "value");
  return !/[{}]/.test(stripped) && placeholders.every((key) => availableOAuthFieldKeys.value.includes(key));
}

function validContractKey(value: string) {
  return /^[A-Za-z][A-Za-z0-9._-]{0,63}$/.test(value.trim());
}

function _validJSONPath(value: string) {
  const parts = value.trim().split(".");
  return parts.length > 0 && parts.length <= 16 && parts.every(validContractKey);
}

function validHeaderName(value: string) {
  return /^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,128}$/.test(value.trim());
}

function parseStatuses(value: string) {
  const statuses = value
    .split(/[\s,]+/)
    .filter(Boolean)
    .map(Number);
  if (
    !statuses.length ||
    statuses.some((status) => !Number.isInteger(status) || status < 100 || status > 599) ||
    new Set(statuses).size !== statuses.length
  ) {
    throw new Error("invalid statuses");
  }
  return statuses;
}

function parseCIDRList(value: string, validate = false) {
  const cidrs = [
    ...new Set(
      value
        .split(/[\s,]+/)
        .map((item) => item.trim())
        .filter(Boolean),
    ),
  ];
  if (validate && cidrs.some((cidr) => !validCIDR(cidr))) throw new Error("invalid CIDR");
  return cidrs;
}

function validCIDR(value: string) {
  const [address, prefixText, extra] = value.split("/");
  if (extra !== undefined || prefixText === undefined || !/^\d+$/.test(prefixText)) return false;
  const prefix = Number(prefixText);
  if (address.includes(":")) return /^[0-9A-Fa-f:.]+$/.test(address) && prefix >= 0 && prefix <= 128;
  return ipv4Number(address) !== null && prefix >= 0 && prefix <= 32;
}

function privateLiteralAddress(value: string) {
  try {
    const hostname = new URL(value.trim()).hostname.replace(/^\[|\]$/g, "");
    const number = ipv4Number(hostname);
    if (number === null) {
      const normalized = hostname.toLowerCase();
      return normalized === "::1" || /^(fc|fd)/.test(normalized) || /^fe[89ab]/.test(normalized) ? hostname : "";
    }
    const first = number >>> 24;
    const second = (number >>> 16) & 255;
    return first === 10 ||
      first === 127 ||
      (first === 169 && second === 254) ||
      (first === 172 && second >= 16 && second <= 31) ||
      (first === 192 && second === 168)
      ? hostname
      : "";
  } catch {
    return "";
  }
}

function cidrContainsIPv4(cidr: string, address: string) {
  const [networkText, prefixText] = cidr.split("/");
  const network = ipv4Number(networkText);
  const target = ipv4Number(address);
  const prefix = Number(prefixText);
  if (network === null || target === null || !Number.isInteger(prefix) || prefix < 0 || prefix > 32) return false;
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
  return (network & mask) === (target & mask);
}

function ipv4Number(value: string) {
  const parts = value.split(".");
  if (parts.length !== 4 || parts.some((part) => !/^\d{1,3}$/.test(part) || Number(part) > 255)) return null;
  return parts.reduce((result, part) => ((result << 8) | Number(part)) >>> 0, 0);
}

function parseOptions(value: string) {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const separator = line.indexOf("=");
      if (separator < 0) return { label: line, value: line };
      return { label: line.slice(0, separator).trim(), value: line.slice(separator + 1).trim() };
    })
    .filter((option) => option.label && option.value);
}

function normalizeVerificationPath(value: string) {
  const trimmed = value.trim();
  return trimmed.startsWith("/") ? trimmed : `/${trimmed}`;
}

function normalizeSyncOutcome(value: ProviderSyncOutcome): ProviderSyncOutcome {
  return {
    id: value?.id,
    status: String(value?.status || "FAILED"),
    discoveredCount: Number(value?.discoveredCount) || 0,
    changedCount: Number(value?.changedCount) || 0,
    errorSummary: asRecord(value?.errorSummary),
  };
}

function syncErrorText(result: ProviderSyncOutcome) {
  const summary = Object.entries(result.errorSummary)
    .map(([key, value]) => `${key}: ${String(value)}`)
    .join("; ");
  return summary || result.status;
}

function showActionNote(message: string, tone: "success" | "warning" | "error" = "success") {
  actionNote.value = message;
  actionTone.value = tone;
}

function nextDraftRowId(prefix: string) {
  draftRowSequence += 1;
  return `${prefix}-${draftRowSequence}`;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function firstString(...values: unknown[]) {
  return values.find((value): value is string => typeof value === "string" && Boolean(value.trim())) || "";
}

function errorMessage(error: unknown, fallback: string) {
  if (error instanceof Error && error.message) return error.message;
  return fallback;
}
</script>

<template>
  <div class="providers-page management-page-grid management-page-grid--two-rows">
    <ManagementPageHeader
      class="providers-page-header"
      :title="t('providers.title')"
      :description="t('providers.subtitle')"
      icon="fa-solid fa-cloud-arrow-down"
      eyebrow="Integration Registry"
    >
      <template #actions>
        <div class="providers-header-actions">
          <RouterLink class="ghost-button" to="/connections">
            <i class="fa-solid fa-plug-circle-bolt" aria-hidden="true" />{{ t("providers.serviceConnections") }}
          </RouterLink>
          <button
            v-if="canEditWorkspace"
            data-testid="provider-create"
            class="primary-button"
            type="button"
            :disabled="!hasWorkspaceContext"
            @click="openCreateEditor"
          >
            <i class="fa-solid fa-circle-plus" aria-hidden="true" />{{ t("providers.create") }}
          </button>
        </div>
      </template>
    </ManagementPageHeader>

    <section class="providers-list-card management-list-card">
      <ManagementList
        class="providers-management-list"
        :rows="hasWorkspaceContext ? providerRows : []"
        :columns="providerColumns"
        row-key="id"
        :sticky-left-keys="['name']"
        :sticky-right-keys="['actions']"
        storage-key="actweave:providers:columns"
        :selectable="false"
        :expanded-row-key="expandedProviderId"
        :loading="hasWorkspaceContext && loading"
        :error="hasWorkspaceContext ? loadError : undefined"
        :has-loaded="hasWorkspaceContext ? hasLoaded : true"
        :search="search"
        :pagination="providerPagination"
        :sort-by="sortBy"
        :sort-order="sortOrder"
        :search-placeholder="t('providers.searchPlaceholder')"
        :search-aria-label="t('providers.searchAria')"
        :clear-search-aria-label="t('providers.clearSearchAria')"
        :reset-disabled="!search && statusFilter === 'ALL'"
        @update:search="updateSearch"
        @reset="resetFilters"
        @page-change="changePage"
        @sort-change="changeSort"
      >
        <template #filters>
          <ManagementSegmentedFilter
            :model-value="statusFilter"
            :options="statusOptions"
            :ariaLabel="t('providers.statusFilterAria')"
            @update:model-value="updateStatusFilter"
          />
        </template>

        <template #cell-name="{ row: provider }">
          <div class="provider-name-cell">
            <span class="provider-icon"><i class="fa-solid fa-cloud" aria-hidden="true" /></span>
            <span>
              <strong class="aw-table-title">{{ provider.name }}</strong>
              <small class="aw-table-subtitle">{{ provider.kind }}</small>
            </span>
          </div>
        </template>
        <template #cell-status="{ row: provider }">
          <span class="provider-status-pill aw-table-pill" :class="statusTone(provider.status)"
            ><i aria-hidden="true" />{{ statusLabel(provider.status) }}</span
          >
        </template>
        <template #cell-serviceBaseUrl="{ row: provider }"
          ><code class="provider-address aw-table-mono">{{ providerServiceBaseUrl(provider) }}</code></template
        >
        <template #cell-documentUrl="{ row: provider }"
          ><code class="provider-address aw-table-mono">{{ providerDocumentUrl(provider) }}</code></template
        >
        <template #cell-authentication="{ row: provider }"
          ><span class="provider-auth-summary aw-table-meta">{{ authSchemeSummary(provider) }}</span></template
        >
        <template #cell-lastSyncedAt="{ row: provider }"
          ><span class="provider-muted aw-table-meta">{{ formatDate(provider.lastSyncedAt) }}</span></template
        >
        <template #cell-actions="{ row: provider }">
          <ManagementRowActions
            :menu-actions="providerMenuActions(provider)"
            :menu-label="t('providers.moreActions', { name: provider.name })"
            @action="handleProviderRowAction($event, provider)"
          />
        </template>

        <template #row-detail="{ row: provider }">
          <section class="provider-detail-row" :aria-label="t('providers.detailAria', { name: provider.name })">
            <div class="provider-contract-summary">
              <div>
                <span>{{ t("providers.runtimeEndpoint") }}</span
                ><code>{{ providerServiceBaseUrl(provider) }}</code>
              </div>
              <div>
                <span>{{ t("providers.openapiDiscovery") }}</span
                ><code>{{ providerDocumentUrl(provider) }}</code>
              </div>
              <div>
                <span>{{ t("providers.connectionVerification") }}</span
                ><strong>{{ providerVerificationSummary(provider) }}</strong>
              </div>
              <div>
                <span>{{ t("providers.authContract") }}</span
                ><strong>{{ authSchemeSummary(provider) }}</strong>
              </div>
            </div>
            <div
              v-if="syncResults[provider.id]"
              :data-testid="`provider-sync-result-${provider.id}`"
              class="provider-sync-result"
              :class="syncResults[provider.id].status === 'SUCCEEDED' ? 'succeeded' : 'failed'"
              role="status"
            >
              <i
                :class="
                  syncResults[provider.id].status === 'SUCCEEDED'
                    ? 'fa-solid fa-circle-check'
                    : 'fa-solid fa-circle-exclamation'
                "
              />
              <span>
                <strong>{{
                  syncResults[provider.id].status === "SUCCEEDED"
                    ? t("providers.lastSyncSucceeded")
                    : t("providers.lastSyncFailed")
                }}</strong>
                <small v-if="syncResults[provider.id].status === 'SUCCEEDED'">{{
                  t("providers.syncCounts", {
                    discovered: syncResults[provider.id].discoveredCount,
                    changed: syncResults[provider.id].changedCount,
                  })
                }}</small>
                <small v-else>{{ syncErrorText(syncResults[provider.id]) }}</small>
              </span>
            </div>
            <div class="provider-assets-heading">
              <div>
                <h3>{{ t("providers.capabilityAssets") }}</h3>
                <p>
                  {{
                    hasProviderDocument(provider) ? t("providers.assetsHintWithDoc") : t("providers.assetsHintNoDoc")
                  }}
                </p>
              </div>
              <button type="button" :disabled="isLoadingAssets(provider.id)" @click="loadAssets(provider.id)">
                <i
                  :class="isLoadingAssets(provider.id) ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-rotate'"
                />{{ t("providers.refreshAssets") }}
              </button>
            </div>
            <p v-if="assetErrors[provider.id]" class="provider-inline-error" role="alert">
              {{ assetErrors[provider.id] }}
            </p>
            <div
              v-if="isLoadingAssets(provider.id) && !(providerStore.providerAssetsByProvider[provider.id] || []).length"
              class="provider-assets-state"
              role="status"
            >
              {{ t("providers.loadingAssets") }}
            </div>
            <div
              v-else-if="!(providerStore.providerAssetsByProvider[provider.id] || []).length"
              class="provider-assets-state"
            >
              {{
                hasProviderDocument(provider) ? t("providers.noAssetsSyncFirst") : t("providers.noAssetsContinue")
              }}
            </div>
            <div v-else class="provider-assets-list">
              <article v-for="asset in providerStore.providerAssetsByProvider[provider.id]" :key="asset.id">
                <span class="asset-method">{{
                  String(asset.metadata?.method || asset.kind || "API").toUpperCase()
                }}</span>
                <div>
                  <strong>{{ asset.name }}</strong
                  ><small>{{ asset.description || asset.externalId }}</small>
                </div>
                <span class="asset-status">{{
                  asset.materializedCapabilityId ? t("providers.materialized") : asset.status
                }}</span>
                <button
                  :data-testid="`provider-materialize-${asset.id}`"
                  type="button"
                  :disabled="Boolean(asset.materializedCapabilityId) || Boolean(materializingAssetId)"
                  @click="materializeAsset(provider, asset)"
                >
                  <i
                    :class="
                      materializingAssetId === asset.id
                        ? 'fa-solid fa-spinner fa-spin'
                        : asset.materializedCapabilityId
                          ? 'fa-solid fa-circle-check'
                          : 'fa-solid fa-wand-magic-sparkles'
                    "
                  />
                  {{ asset.materializedCapabilityId ? t("providers.toolCreated") : t("providers.materializeToTool") }}
                </button>
              </article>
            </div>
          </section>
        </template>

        <template #card="{ row: provider }">
          <article class="provider-mobile-card">
            <header>
              <div class="provider-name-cell">
                <span class="provider-icon"><i class="fa-solid fa-cloud" /></span
                ><span
                  ><strong>{{ provider.name }}</strong
                  ><small>{{ provider.kind }}</small></span
                >
              </div>
              <span class="provider-status-pill" :class="statusTone(provider.status)"
                ><i />{{ statusLabel(provider.status) }}</span
              >
            </header>
            <dl>
              <div>
                <dt>{{ t("providers.mobileRuntimeUrl") }}</dt>
                <dd>
                  <code>{{ providerServiceBaseUrl(provider) }}</code>
                </dd>
              </div>
              <div>
                <dt>{{ t("providers.mobileDiscoveryUrl") }}</dt>
                <dd>
                  <code>{{ providerDocumentUrl(provider) }}</code>
                </dd>
              </div>
              <div>
                <dt>{{ t("providers.mobileAuth") }}</dt>
                <dd>{{ authSchemeSummary(provider) }}</dd>
              </div>
              <div>
                <dt>{{ t("providers.mobileLastSync") }}</dt>
                <dd>{{ formatDate(provider.lastSyncedAt) }}</dd>
              </div>
            </dl>
            <div class="provider-actions mobile">
              <button type="button" @click="openEditEditor(provider)"
                ><i class="fa-solid fa-pen" />{{ t("providers.edit") }}</button
              ><button
                type="button"
                :title="providerSyncTitle(provider)"
                :disabled="isSyncing(provider.id) || !canSyncProvider(provider)"
                @click="syncProvider(provider)"
              >
                <i class="fa-solid fa-rotate" />{{ t("providers.sync") }}</button
              ><button type="button" @click="toggleAssets(provider)"
                ><i class="fa-solid fa-cubes" />{{ t("providers.assets") }}</button
              ><button class="danger" type="button" @click="requestDeleteProvider(provider)">
                <i class="fa-solid fa-trash-can" />{{ t("providers.delete") }}
              </button>
            </div>
          </article>
        </template>

        <template #empty>
          <WorkspaceContextState
            v-if="!hasWorkspaceContext"
            embedded-in-list
            :feature="t('providers.featureName')"
            icon="fa-solid fa-cloud-arrow-down"
            @retry="loadProviders"
          />
          <div v-else class="providers-empty-state">
            <span><i class="fa-solid fa-cloud-arrow-down" /></span>
            <h2>
              {{
                search || statusFilter !== "ALL" ? t("providers.noMatchTitle") : t("providers.emptyTitle")
              }}
            </h2>
            <p>
              {{
                search || statusFilter !== "ALL" ? t("providers.noMatchBody") : t("providers.emptyBody")
              }}
            </p>
            <button
              v-if="canEditWorkspace && !search && statusFilter === 'ALL'"
              type="button"
              @click="openCreateEditor"
            >
              {{ t("providers.create") }}
            </button>
          </div>
        </template>
        <template #error="{ error }"
          ><div class="providers-error-state" role="alert">
            <strong>{{ t("providers.loadFailedTitle") }}</strong>
            <p>{{ error }}</p>
            <button type="button" @click="loadProviders">{{ t("providers.reload") }}</button>
          </div></template
        >
      </ManagementList>
    </section>

    <div v-if="editorVisible" class="provider-modal-backdrop" @click.self="closeEditor">
      <section
        ref="editorPanel"
        class="provider-editor"
        role="dialog"
        aria-modal="true"
        aria-labelledby="provider-editor-title"
        @keydown="trapEditorFocus"
      >
        <header>
          <div>
            <span class="provider-editor-icon"><i class="fa-solid fa-cloud" /></span
            ><span
              ><small>Provider Registry</small>
              <h2 id="provider-editor-title">
                {{
                  editorMode === "create" ? t("providers.editorCreateTitle") : t("providers.editorEditTitle")
                }}
              </h2></span
            >
          </div>
          <button
            type="button"
            :aria-label="t('providers.closeEditorAria')"
            :disabled="saving"
            @click="closeEditor"
          >
            <i class="fa-solid fa-xmark" />
          </button>
        </header>
        <form @submit.prevent="saveProvider">
          <div class="provider-editor-body">
            <p v-if="formError" class="provider-form-error" role="alert">
              <i class="fa-solid fa-circle-exclamation" />{{ formError }}
            </p>
            <section class="provider-form-section">
              <div class="provider-section-heading">
                <span>1</span>
                <div>
                  <h3>{{ t("providers.sectionServiceDiscovery") }}</h3>
                  <p>{{ t("providers.sectionServiceDiscoveryHint") }}</p>
                </div>
              </div>
              <div class="provider-form-grid two">
                <label
                  ><span>{{ t("providers.fieldName") }} <b>*</b></span
                  ><input
                    ref="providerNameInput"
                    v-model="providerDraft.name"
                    data-testid="provider-name"
                    required
                    :placeholder="t('providers.namePlaceholder')"
                /></label>
                <label
                  ><span>{{ t("providers.fieldDiscoveryMode") }}</span
                  ><AppSelect
                    v-model="providerDraft.discoveryMode"
                    class="provider-form-select"
                    data-testid="provider-discovery-mode"
                    :options="discoveryModeOptions"
                    :ariaLabel="t('providers.discoveryModeAria')"
                /></label>
              </div>
              <label
                ><span>{{ t("providers.fieldServiceBaseUrl") }} <b>*</b></span
                ><input
                  v-model="providerDraft.serviceBaseUrl"
                  data-testid="provider-service-base-url"
                  class="mono"
                  required
                  placeholder="https://api.example.com/v1"
                /><small>{{ t("providers.serviceBaseUrlHint") }}</small></label
              >
              <label
                ><span>{{ t("providers.fieldDocumentUrl") }} <em>{{ t("providers.optional") }}</em></span
                ><input
                  v-model="providerDraft.documentUrl"
                  data-testid="provider-document-url"
                  class="mono"
                  inputmode="url"
                  placeholder="https://api.example.com/openapi.json"
                /><small>{{ t("providers.documentUrlHint") }}</small></label
              >
              <label
                ><span>{{ t("providers.fieldAllowedCidrs") }} <em>{{ t("providers.asNeeded") }}</em></span
                ><textarea
                  v-model="providerDraft.allowedCIDRs"
                  data-testid="provider-allowed-cidrs"
                  class="mono"
                  rows="2"
                  placeholder="192.168.10.0/24"
                /><small>{{ t("providers.allowedCidrsHint") }}</small></label
              >
            </section>

            <section class="provider-form-section">
              <div class="provider-section-heading">
                <span>2</span>
                <div>
                  <h3>{{ t("providers.sectionVerification") }}</h3>
                  <p>{{ t("providers.sectionVerificationHint") }}</p>
                </div>
              </div>
              <div class="provider-form-grid verification">
                <label
                  ><span>{{ t("providers.fieldMethod") }}</span
                  ><AppSelect
                    v-model="providerDraft.verificationMethod"
                    class="provider-form-select"
                    data-testid="provider-verification-method"
                    :options="verificationMethodOptions"
                    :ariaLabel="t('providers.verificationMethodAria')"
                /></label>
                <label
                  ><span>{{ t("providers.fieldRelativePath") }}</span
                  ><input
                    v-model="providerDraft.verificationPath"
                    data-testid="provider-verification-path"
                    class="mono"
                    placeholder="/health"
                /></label>
                <label
                  ><span>{{ t("providers.fieldExpectedStatuses") }}</span
                  ><input
                    v-model="providerDraft.expectedStatuses"
                    data-testid="provider-expected-statuses"
                    class="mono"
                    placeholder="200, 204"
                /></label>
              </div>
            </section>

            <section
              class="provider-form-section authentication provider-identity-section"
              data-testid="provider-outbound-identity"
              :class="{ 'has-identity-error': Boolean(identityModeError) }"
              :aria-invalid="identityModeError ? 'true' : undefined"
            >
              <div class="provider-section-heading">
                <span>3</span>
                <div>
                  <h3>{{ t("providers.sectionIdentity") }}</h3>
                  <p>{{ t("providers.sectionIdentityHint") }}</p>
                </div>
              </div>
              <p v-if="providerDraft.legacyAuthentication" class="provider-migration-note">
                <i class="fa-solid fa-triangle-exclamation" />
                {{ t("providers.legacyAuthNote", { schema: "outbound-identity.v1" }) }}
              </p>
              <p
                v-if="identityModeError"
                class="provider-identity-error"
                data-testid="provider-identity-mode-error"
                role="alert"
              >
                {{ identityModeError }}
              </p>
              <div
                ref="identityModeGroupRef"
                class="provider-auth-choice provider-identity-choice"
                role="group"
                :aria-label="t('providers.identityModesAria')"
                :aria-describedby="identityModeError ? 'provider-identity-mode-error-text' : undefined"
              >
                <label class="provider-identity-card" :class="{ selected: providerDraft.supportBrokerObo }">
                  <input
                    v-model="providerDraft.supportBrokerObo"
                    data-testid="provider-mode-broker"
                    type="checkbox"
                    :disabled="saving"
                    @change="clearIdentityModeErrorIfResolved"
                  />
                  <span class="provider-identity-card-body">
                    <b v-if="providerDraft.supportBrokerObo" class="provider-identity-badge" aria-hidden="true">
                      <i class="fa-solid fa-check" />{{ t("providers.modeSupported") }}
                    </b>
                    <i class="fa-solid fa-key provider-identity-icon" aria-hidden="true" />
                    <span class="provider-identity-copy">
                      <strong>{{ t("providers.modeBrokerTitle") }}</strong>
                      <small>{{ t("providers.modeBrokerDesc") }}</small>
                    </span>
                  </span>
                </label>
                <label class="provider-identity-card" :class="{ selected: providerDraft.supportRequestPassthrough }">
                  <input
                    v-model="providerDraft.supportRequestPassthrough"
                    data-testid="provider-mode-passthrough"
                    type="checkbox"
                    :disabled="saving"
                    @change="clearIdentityModeErrorIfResolved"
                  />
                  <span class="provider-identity-card-body">
                    <b
                      v-if="providerDraft.supportRequestPassthrough"
                      class="provider-identity-badge"
                      aria-hidden="true"
                    >
                      <i class="fa-solid fa-check" />{{ t("providers.modeSupported") }}
                    </b>
                    <i class="fa-solid fa-right-left provider-identity-icon" aria-hidden="true" />
                    <span class="provider-identity-copy">
                      <strong>{{ t("providers.modePassthroughTitle") }}</strong>
                      <small>{{ t("providers.modePassthroughDesc") }}</small>
                    </span>
                  </span>
                </label>
              </div>
              <p v-if="identityModeError" id="provider-identity-mode-error-text" class="sr-only">
                {{ identityModeError }}
              </p>
              <div class="provider-form-grid two">
                <label
                  ><span>{{ t("providers.fieldInjectionHeader") }} <b>*</b></span
                  ><input
                    v-model="providerDraft.businessInjectionHeader"
                    data-testid="provider-injection-header"
                    class="mono"
                    placeholder="Authorization"
                /></label>
                <label
                  ><span>{{ t("providers.fieldPrefix") }}</span
                  ><input
                    v-model="providerDraft.businessInjectionPrefix"
                    data-testid="provider-injection-prefix"
                    class="mono"
                    placeholder="Bearer"
                /></label>
              </div>
              <div
                v-if="providerDraft.supportBrokerObo"
                class="provider-oauth-editor"
                data-testid="provider-broker-fields"
              >
                <div class="provider-form-grid two">
                  <label
                    ><span>{{ t("providers.fieldBrokerTokenEndpoint") }} <b>*</b></span
                    ><input
                      v-model="providerDraft.brokerTokenEndpoint"
                      data-testid="provider-broker-token-endpoint"
                      class="mono"
                      placeholder="https://broker.example.com/oauth/token"
                  /></label>
                  <label
                    ><span>{{ t("providers.fieldAudience") }} <b>*</b></span
                    ><input
                      v-model="providerDraft.brokerAudience"
                      data-testid="provider-broker-audience"
                      class="mono"
                      placeholder="api://resource"
                  /></label>
                </div>
                <label
                  ><span>{{ t("providers.fieldAllowedScopes") }}</span
                  ><input
                    v-model="providerDraft.brokerAllowedScopes"
                    data-testid="provider-broker-scopes"
                    class="mono"
                    placeholder="orders.read inventory.read"
                  /><small>{{ t("providers.allowedScopesHint") }}</small></label
                >
                <p class="provider-field-help">{{ t("providers.brokerHelp") }}</p>
              </div>
              <p
                v-if="providerDraft.supportRequestPassthrough"
                class="provider-field-help"
                data-testid="provider-passthrough-summary"
              >
                {{ t("providers.passthroughHelp") }}
              </p>
              <details class="provider-identity-tech" data-testid="provider-identity-tech-details">
                <summary>{{ t("providers.techConstraints") }}</summary>
                <ul>
                  <li>
                    {{
                      t("providers.techContract", {
                        schema: "outbound-identity.v1",
                        broker: "BROKER_OBO",
                        passthrough: "REQUEST_PASSTHROUGH",
                      })
                    }}
                  </li>
                  <li>{{ t("providers.techSubject", { user: "USER" }) }}</li>
                  <li>{{ t("providers.techBrokerAuth", { method: "private_key_jwt" }) }}</li>
                  <li>
                    {{ t("providers.techPassthroughCred", { type: "ACCESS_TOKEN", expiresAt: "expiresAt" }) }}
                  </li>
                  <li>{{ t("providers.techTokenBoundary") }}</li>
                </ul>
              </details>
            </section>
          </div>
          <footer>
            <button class="ghost-button" type="button" :disabled="saving" @click="closeEditor">{{
              t("providers.cancel")
            }}</button
            ><button data-testid="provider-save" class="primary-button" type="submit" :disabled="saving">
              <i :class="saving ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-floppy-disk'" />{{
                saving ? t("providers.saving") : t("providers.saveProvider")
              }}
            </button>
          </footer>
        </form>
      </section>
    </div>

    <div v-if="pendingDeleteProvider" class="provider-modal-backdrop" @click.self="closeDeleteDialog">
      <section class="provider-delete-dialog" role="dialog" aria-modal="true" aria-labelledby="provider-delete-title">
        <span class="provider-delete-icon"><i class="fa-solid fa-trash-can" /></span>
        <h2 id="provider-delete-title">{{ t("providers.deleteTitle") }}</h2>
        <p>
          <i18n-t keypath="providers.deleteBody" tag="span">
            <template #name>
              <strong>{{ pendingDeleteProvider.name }}</strong>
            </template>
          </i18n-t>
        </p>
        <label
          ><span>{{ t("providers.deleteConfirmLabel") }}</span
          ><input v-model="deleteConfirmText" data-testid="provider-delete-confirm-input" autocomplete="off"
        /></label>
        <p v-if="deleteError" class="provider-form-error" role="alert">{{ deleteError }}</p>
        <footer>
          <button type="button" :disabled="deleting" @click="closeDeleteDialog">{{ t("providers.cancel") }}</button
          ><button
            data-testid="provider-delete-confirm"
            class="danger"
            type="button"
            :disabled="deleting || !deleteConfirmMatches"
            @click="confirmDeleteProvider"
          >
            <i :class="deleting ? 'fa-solid fa-spinner fa-spin' : 'fa-solid fa-trash-can'" />{{
              t("providers.confirmDelete")
            }}
          </button>
        </footer>
      </section>
    </div>

    <div v-if="actionNote" class="provider-action-toast" :class="actionTone" role="status" aria-live="polite">
      <i
        :class="
          actionTone === 'error'
            ? 'fa-solid fa-circle-exclamation'
            : actionTone === 'warning'
              ? 'fa-solid fa-triangle-exclamation'
              : 'fa-solid fa-circle-check'
        "
      />{{ actionNote
      }}<button type="button" :aria-label="t('providers.closeNoteAria')" @click="actionNote = ''"
        ><i class="fa-solid fa-xmark"
      /></button>
    </div>
  </div>
</template>

<style scoped>
.providers-page {
  min-width: 0;
  min-height: 0;
}
.providers-page-header {
  min-width: 0;
}
.providers-header-actions,
.provider-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.providers-header-actions .ghost-button {
  text-decoration: none;
}
/* Transparent shell — ManagementList owns table/toolbar/footer chrome. */
.providers-list-card.management-list-card {
  min-width: 0;
  min-height: 0;
  overflow: visible;
  border: 0;
  border-radius: 0;
  background: transparent;
  box-shadow: none;
}
.providers-management-list {
  height: 100%;
}
.providers-management-list :deep(.data-table tbody tr:hover .provider-name-cell strong) {
  color: #4f46e5;
}
.provider-name-cell {
  display: flex;
  min-width: 0;
  max-width: 100%;
  align-items: center;
  gap: 12px;
  overflow: hidden;
}
.provider-name-cell > span:last-child {
  display: grid;
  min-width: 0;
  max-width: 100%;
  flex: 1 1 auto;
  gap: 2px;
  overflow: hidden;
}
.provider-name-cell strong,
.provider-name-cell small {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.provider-name-cell strong,
.provider-name-cell .aw-table-title {
  color: var(--aw-table-title-color, #111827);
  font-size: var(--aw-table-title-size, 0.9rem);
  font-weight: var(--aw-table-title-weight, 600);
  line-height: 1.35;
  transition: color 0.15s ease;
}
.provider-name-cell small,
.provider-name-cell .aw-table-subtitle {
  color: var(--aw-table-subtitle-color, #6b7280);
  font-size: var(--aw-table-subtitle-size, 0.8125rem);
  font-weight: var(--aw-table-subtitle-weight, 400);
  line-height: 1.35;
}
.provider-icon {
  width: 32px;
  height: 32px;
  display: grid;
  flex: 0 0 32px;
  place-items: center;
  border: 1px solid #d1fae5;
  border-radius: 8px;
  background: #ecfdf5;
  color: #059669;
  font-size: var(--aw-table-pill-size, 0.75rem);
}
.provider-status-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 9px;
  border-radius: 999px;
  font-size: var(--aw-table-pill-size, 0.75rem);
  font-weight: var(--aw-table-pill-weight, 600);
  white-space: nowrap;
}
.provider-status-pill i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.provider-status-pill.active {
  background: #eaf8f4;
  color: #07806e;
}
.provider-status-pill.error {
  background: #fff1f2;
  color: #dc2626;
}
.provider-status-pill.disabled,
.provider-status-pill.neutral {
  background: #f1f5f9;
  color: #64748b;
}
.provider-address {
  display: block;
  overflow: hidden;
  color: var(--aw-table-body-color, #374151);
  font-family: var(--aw-table-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: var(--aw-table-mono-size, 0.82rem);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.provider-auth-summary,
.provider-muted {
  color: var(--aw-table-meta-color, #6b7280);
  font-size: var(--aw-table-meta-size, 0.8125rem);
  font-weight: var(--aw-table-meta-weight, 400);
}
.provider-actions {
  justify-content: flex-end;
}
.provider-actions button {
  min-width: 36px;
  height: 34px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  padding: 0 10px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #fff;
  color: #64748b;
  font: inherit;
  cursor: pointer;
}
.provider-actions button:hover {
  border-color: #99d8ca;
  color: #0d9488;
}
.provider-actions button.danger:hover,
.provider-actions button.danger {
  color: #dc2626;
}
.provider-actions button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}
.provider-detail-row {
  display: grid;
  gap: 14px;
  padding: 18px 20px 20px;
  background: #f8fafc;
}
.provider-contract-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}
.provider-contract-summary > div {
  min-width: 0;
  display: grid;
  gap: 5px;
  padding: 12px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
}
.provider-contract-summary span {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 700;
  text-transform: uppercase;
}
.provider-contract-summary code,
.provider-contract-summary strong {
  overflow: hidden;
  color: #334155;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.provider-sync-result {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 13px;
  border-radius: 9px;
}
.provider-sync-result > span {
  display: grid;
  gap: 2px;
}
.provider-sync-result small {
  font-size: 11px;
}
.provider-sync-result.succeeded {
  background: #eaf8f4;
  color: #07806e;
}
.provider-sync-result.failed {
  background: #fff1f2;
  color: #b91c1c;
}
.provider-assets-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.provider-assets-heading h3 {
  margin: 0;
  color: #0f172a;
  font-size: 14px;
}
.provider-assets-heading p {
  margin: 3px 0 0;
  color: #64748b;
  font-size: 11px;
}
.provider-assets-heading button,
.provider-assets-list button {
  min-height: 34px;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0 11px;
  border: 1px solid #dbe3ef;
  border-radius: 8px;
  background: #fff;
  color: #475569;
  font: inherit;
  font-size: 11px;
  font-weight: 700;
  cursor: pointer;
}
.provider-assets-list {
  display: grid;
  gap: 8px;
}
.provider-assets-list article {
  min-width: 0;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 12px;
  padding: 11px 12px;
  border: 1px solid #e8edf4;
  border-radius: 10px;
  background: #fff;
}
.provider-assets-list article > div {
  min-width: 0;
  display: grid;
  gap: 3px;
}
.provider-assets-list strong,
.provider-assets-list small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.provider-assets-list strong {
  color: #1e293b;
  font-size: 12px;
}
.provider-assets-list small {
  color: #94a3b8;
  font-size: 10px;
}
.asset-method {
  padding: 4px 7px;
  border-radius: 6px;
  background: #eef6ff;
  color: #2563eb;
  font:
    700 10px ui-monospace,
    monospace;
}
.asset-status {
  color: #64748b;
  font-size: 10px;
}
.provider-assets-state {
  padding: 22px;
  border: 1px dashed #cbd5e1;
  border-radius: 10px;
  color: #94a3b8;
  text-align: center;
  font-size: 12px;
}
.provider-inline-error {
  margin: 0;
  color: #b91c1c;
  font-size: 12px;
}
.provider-mobile-card {
  display: grid;
  gap: 14px;
  padding: 16px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
}
.provider-mobile-card > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.provider-mobile-card dl {
  display: grid;
  gap: 8px;
  margin: 0;
}
.provider-mobile-card dl > div {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr);
  gap: 10px;
}
.provider-mobile-card dt {
  color: #94a3b8;
  font-size: 10px;
}
.provider-mobile-card dd {
  min-width: 0;
  margin: 0;
  overflow: hidden;
  color: #475569;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.provider-actions.mobile {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
}
.providers-empty-state,
.providers-error-state {
  min-height: 330px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 36px;
  text-align: center;
}
.providers-empty-state > span {
  width: 58px;
  height: 58px;
  display: grid;
  place-items: center;
  border-radius: 18px;
  background: #eff8f6;
  color: #0d9488;
  font-size: 24px;
}
.providers-empty-state h2,
.providers-error-state strong {
  margin: 16px 0 0;
  color: #0f172a;
  font-size: 18px;
}
.providers-empty-state p,
.providers-error-state p {
  max-width: 520px;
  margin: 8px 0 0;
  color: #64748b;
  font-size: 12px;
  line-height: 1.7;
}
.providers-empty-state button,
.providers-error-state button {
  min-height: 37px;
  margin-top: 18px;
  padding: 0 13px;
  border: 1px solid var(--aw-green, #0f9f6e);
  border-radius: 10px;
  background: var(--aw-green, #0f9f6e);
  color: #fff;
  font: inherit;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 6px 14px rgba(15, 159, 110, 0.14);
}
.provider-modal-backdrop {
  position: fixed;
  z-index: 3000;
  inset: 0;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(15, 23, 42, 0.55);
  backdrop-filter: blur(3px);
}
.provider-editor {
  width: min(960px, calc(100vw - 32px));
  max-height: calc(100vh - 32px);
  display: flex;
  overflow: hidden;
  flex-direction: column;
  border: 1px solid rgba(255, 255, 255, 0.5);
  border-radius: 16px;
  background: #fff;
  box-shadow: 0 30px 80px rgba(15, 23, 42, 0.28);
}
.provider-editor > header {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 22px;
  border-bottom: 1px solid #e9eef5;
}
.provider-editor > header > div {
  display: flex;
  align-items: center;
  gap: 12px;
}
.provider-editor > header > div > span:last-child {
  display: grid;
  gap: 2px;
}
.provider-editor > header small {
  color: #0d9488;
  font-size: 9px;
  font-weight: 800;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
.provider-editor h2 {
  margin: 0;
  color: #0f172a;
  font-size: 19px;
}
.provider-editor > header > button {
  width: 38px;
  height: 38px;
  border: 1px solid #e2e8f0;
  border-radius: 9px;
  background: #fff;
  color: #64748b;
  cursor: pointer;
}
.provider-editor-icon {
  width: 40px;
  height: 40px;
  display: grid;
  place-items: center;
  border-radius: 11px;
  background: #eaf8f4;
  color: #0d9488;
}
.provider-editor form {
  min-height: 0;
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
}
.provider-editor-body {
  min-height: 0;
  display: grid;
  gap: 14px;
  overflow: auto;
  padding: 18px 22px 24px;
  background: #f8fafc;
}
.provider-editor form > footer {
  display: flex;
  flex: 0 0 auto;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 22px;
  border-top: 1px solid #e9eef5;
  background: #fff;
}
.provider-form-section {
  display: grid;
  gap: 14px;
  padding: 18px;
  border: 1px solid #e5ebf3;
  border-radius: 13px;
  background: #fff;
}
.provider-section-heading {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}
.provider-section-heading > span {
  width: 25px;
  height: 25px;
  display: grid;
  flex: 0 0 25px;
  place-items: center;
  border-radius: 8px;
  background: #eaf8f4;
  color: #07806e;
  font-size: 11px;
  font-weight: 800;
}
.provider-section-heading h3 {
  margin: 0;
  color: #0f172a;
  font-size: 14px;
}
.provider-section-heading p {
  margin: 3px 0 0;
  color: #64748b;
  font-size: 11px;
  line-height: 1.5;
}
.provider-form-section label,
.provider-delete-dialog label {
  min-width: 0;
  display: grid;
  gap: 6px;
  color: #334155;
  font-size: 11px;
  font-weight: 700;
}
.provider-form-section label > span b {
  color: #dc2626;
}
.provider-form-section input,
.provider-form-section textarea,
.provider-delete-dialog input {
  width: 100%;
  box-sizing: border-box;
  border: 1px solid #dbe3ef;
  border-radius: 8px;
  outline: none;
  background-color: #f8fafc;
  color: #1e293b;
  font-family: inherit;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.4;
}
.provider-form-section input:not([type="radio"]):not([type="checkbox"]),
.provider-delete-dialog input {
  height: 44px;
  min-height: 44px;
  margin: 0;
  padding: 0 12px;
}
.provider-form-select :deep(.el-select__wrapper) {
  min-height: 44px;
  border-radius: 8px;
}
.provider-form-section textarea {
  min-height: 76px;
  padding: 10px 12px;
  resize: vertical;
}
.provider-form-section input:focus,
.provider-form-section textarea:focus,
.provider-delete-dialog input:focus {
  border-color: rgba(13, 148, 136, 0.6);
  background: #fff;
  box-shadow: 0 0 0 2px rgba(13, 148, 136, 0.14);
}
.provider-form-section label small {
  color: #94a3b8;
  font-size: 10px;
  font-weight: 500;
  line-height: 1.5;
}
.provider-form-section label > span em {
  margin-left: 5px;
  color: #0d9488;
  font-size: 9px;
  font-style: normal;
  font-weight: 750;
}
.provider-form-grid {
  display: grid;
  gap: 12px;
}
.provider-form-grid > label {
  align-self: start;
}
.provider-form-grid.two {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}
.provider-form-grid.three,
.provider-form-grid.verification {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace !important;
}
.provider-form-error {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 0;
  padding: 11px 13px;
  border: 1px solid #fecdd3;
  border-radius: 9px;
  background: #fff1f2;
  color: #b91c1c;
  font-size: 12px;
  line-height: 1.55;
}
.provider-migration-note {
  display: flex;
  gap: 8px;
  margin: 0;
  padding: 10px 12px;
  border: 1px solid #fde68a;
  border-radius: 8px;
  background: #fffbeb;
  color: #92400e;
  font-size: 11px;
  line-height: 1.55;
}
.provider-auth-choice,
.provider-identity-choice {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.provider-identity-card {
  position: relative;
  display: block;
  cursor: pointer;
}
/* Visually hidden but focusable native checkbox — never pointer-events:none. */
.provider-identity-card > input[type="checkbox"] {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
.provider-identity-card-body {
  position: relative;
  min-height: 92px;
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 14px 13px;
  border: 1px solid #dbe3ef;
  border-radius: 10px;
  background: #f8fafc;
  transition:
    border-color 0.16s ease,
    background-color 0.16s ease,
    box-shadow 0.16s ease;
}
.provider-identity-icon {
  flex: 0 0 34px;
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  border-radius: 9px;
  background: #fff;
  color: #64748b;
}
/* Title + description stack; keep clear of the absolute badge. */
.provider-identity-copy {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  flex-direction: column;
  gap: 4px;
  padding-right: 68px;
}
.provider-identity-copy > strong {
  display: block;
  color: #0f172a;
  font-size: 13px;
  font-weight: 800;
  line-height: 1.35;
}
.provider-identity-copy > small {
  display: block;
  color: #64748b;
  font-size: 12px;
  font-weight: 500;
  line-height: 1.45;
}
.provider-identity-badge {
  position: absolute;
  top: 10px;
  right: 10px;
  z-index: 1;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(13, 148, 136, 0.12);
  color: #0d9488;
  font-size: 11px;
  font-weight: 800;
  line-height: 1.3;
  white-space: nowrap;
  pointer-events: none;
}
.provider-identity-badge i {
  font-size: 10px;
}
.provider-identity-card:hover .provider-identity-card-body {
  border-color: #b6c3d6;
}
.provider-identity-card.selected .provider-identity-card-body {
  border-color: #80cbbb;
  background: #effaf7;
  box-shadow: 0 0 0 2px rgba(13, 148, 136, 0.08);
}
.provider-identity-card.selected .provider-identity-icon {
  color: #0d9488;
}
.provider-identity-card > input[type="checkbox"]:focus-visible + .provider-identity-card-body {
  outline: 2px solid rgba(13, 148, 136, 0.55);
  outline-offset: 2px;
}
.provider-identity-error {
  margin: 0 0 10px;
  padding: 8px 10px;
  border: 1px solid rgba(220, 38, 38, 0.24);
  border-radius: 8px;
  background: #fff1f2;
  color: #b91c1c;
  font-size: 12px;
  font-weight: 700;
}
.provider-identity-section.has-identity-error .provider-identity-card-body {
  border-color: rgba(220, 38, 38, 0.28);
}
.provider-identity-tech {
  margin-top: 10px;
  padding: 10px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #475569;
  font-size: 12px;
}
.provider-identity-tech summary {
  cursor: pointer;
  font-weight: 700;
  color: #0f172a;
}
.provider-identity-tech summary:focus-visible {
  outline: 2px solid rgba(13, 148, 136, 0.55);
  outline-offset: 2px;
}
.provider-identity-tech ul {
  margin: 8px 0 0;
  padding-left: 18px;
  display: grid;
  gap: 4px;
  line-height: 1.5;
}
.provider-identity-tech code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
/* Legacy selector kept for any residual markup. */
.provider-auth-choice > label {
  position: relative;
  cursor: pointer;
}
.provider-auth-choice label > span {
  min-height: 76px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 13px;
  border: 1px solid #dbe3ef;
  border-radius: 10px;
  background: #f8fafc;
}
.provider-auth-choice label > span > i {
  width: 34px;
  height: 34px;
  display: grid;
  flex: 0 0 34px;
  place-items: center;
  border-radius: 9px;
  background: #fff;
  color: #64748b;
}
.provider-auth-choice label > span > strong,
.provider-auth-choice label > span > small {
  display: block;
}
.provider-auth-choice label > span > small {
  margin-top: 3px;
  color: #94a3b8;
  font-weight: 500;
}
.provider-auth-choice label.selected > span {
  border-color: #80cbbb;
  background: #effaf7;
  box-shadow: 0 0 0 2px rgba(13, 148, 136, 0.08);
}
.provider-auth-choice label.selected > span > i {
  color: #0d9488;
}
.provider-oauth-editor {
  display: grid;
  gap: 14px;
  padding-top: 4px;
}
.provider-built-in-fields {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
  padding: 10px 12px;
  border-radius: 9px;
  background: #f1f5f9;
  color: #64748b;
  font-size: 10px;
}
.provider-built-in-fields > span {
  margin-right: auto;
  font-weight: 700;
}
.provider-built-in-fields code {
  padding: 4px 7px;
  border-radius: 6px;
  background: #fff;
  color: #475569;
}
.provider-repeatable-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding-top: 5px;
}
.provider-repeatable-heading h4 {
  margin: 0;
  color: #1e293b;
  font-size: 12px;
}
.provider-repeatable-heading p {
  margin: 3px 0 0;
  color: #94a3b8;
  font-size: 10px;
  line-height: 1.5;
}
.provider-repeatable-heading button {
  min-height: 34px;
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 6px;
  padding: 0 10px;
  border: 1px solid #dbe3ef;
  border-radius: 8px;
  background: #fff;
  color: #0d9488;
  font: inherit;
  font-size: 10px;
  font-weight: 700;
  cursor: pointer;
}
.provider-repeatable-empty {
  padding: 14px;
  border: 1px dashed #cbd5e1;
  border-radius: 9px;
  color: #94a3b8;
  text-align: center;
  font-size: 10px;
}
.provider-repeatable-card {
  display: grid;
  gap: 11px;
  padding: 13px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fbfdff;
}
.provider-repeatable-card > header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  color: #475569;
  font-size: 11px;
}
.provider-repeatable-card > header button {
  width: 30px;
  height: 30px;
  border: 0;
  border-radius: 7px;
  background: #fff1f2;
  color: #dc2626;
  cursor: pointer;
}
.provider-checkbox {
  display: flex !important;
  min-height: 42px;
  align-items: center;
  align-self: end;
  gap: 8px !important;
  padding: 0 4px;
}
.provider-checkbox input {
  width: 16px;
  min-height: 16px;
}
.provider-form-section details {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fbfdff;
}
.provider-form-section summary {
  padding: 12px 13px;
  color: #334155;
  font-size: 11px;
  font-weight: 750;
  cursor: pointer;
}
.provider-form-section details > div {
  padding: 0 13px 13px;
}
.provider-delete-dialog {
  width: min(460px, calc(100vw - 32px));
  display: grid;
  justify-items: center;
  gap: 12px;
  padding: 24px;
  border-radius: 15px;
  background: #fff;
  box-shadow: 0 30px 80px rgba(15, 23, 42, 0.3);
  text-align: center;
}
.provider-delete-icon {
  width: 50px;
  height: 50px;
  display: grid;
  place-items: center;
  border-radius: 15px;
  background: #fff1f2;
  color: #dc2626;
  font-size: 19px;
}
.provider-delete-dialog h2 {
  margin: 0;
  color: #0f172a;
  font-size: 19px;
}
.provider-delete-dialog > p {
  margin: 0;
  color: #64748b;
  font-size: 12px;
  line-height: 1.65;
}
.provider-delete-dialog label {
  width: 100%;
  text-align: left;
}
.provider-delete-dialog footer {
  width: 100%;
  display: flex;
  justify-content: flex-end;
  gap: 9px;
  margin-top: 4px;
}
.provider-delete-dialog footer button {
  min-height: 40px;
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 0 14px;
  border: 1px solid #dbe3ef;
  border-radius: 8px;
  background: #fff;
  color: #475569;
  font: inherit;
  font-size: 11px;
  font-weight: 700;
  cursor: pointer;
}
.provider-delete-dialog footer button.danger {
  border-color: #dc2626;
  background: #dc2626;
  color: #fff;
}
.provider-delete-dialog footer button:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}
.provider-action-toast {
  position: fixed;
  z-index: 3200;
  right: 24px;
  bottom: 24px;
  max-width: min(520px, calc(100vw - 48px));
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 12px 14px;
  border: 1px solid #bfe9df;
  border-radius: 10px;
  background: #fff;
  color: #07806e;
  box-shadow: 0 14px 38px rgba(15, 23, 42, 0.16);
  font-size: 12px;
}
.provider-action-toast.error {
  border-color: #fecdd3;
  color: #b91c1c;
}
.provider-action-toast.warning {
  border-color: #fde68a;
  color: #92400e;
}
.provider-action-toast button {
  margin-left: auto;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
@media (max-width: 1180px) {
  .provider-contract-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 820px) {
  .providers-page-header {
    align-items: stretch;
    flex-direction: column;
  }
  .providers-header-actions {
    justify-content: stretch;
  }
  .providers-header-actions > * {
    flex: 1 1 0;
  }
  .provider-form-grid.two,
  .provider-form-grid.three,
  .provider-form-grid.verification,
  .provider-auth-choice {
    grid-template-columns: 1fr;
  }
  .provider-modal-backdrop {
    padding: 8px;
  }
  .provider-editor {
    max-height: calc(100vh - 16px);
  }
}
@media (max-width: 620px) {
  .provider-contract-summary {
    grid-template-columns: 1fr;
  }
  .provider-assets-list article {
    grid-template-columns: auto minmax(0, 1fr);
  }
  .provider-assets-list article > button,
  .provider-assets-list article > .asset-status {
    grid-column: 2;
    justify-self: start;
  }
  .provider-actions.mobile {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
