"use strict";

const i18n = window.NetSwitchI18n;
const t = i18n.t;
const sessionTokenKey = "net-switch-session-token";
const networkRefreshInterval = 5000;
const fragment = new URLSearchParams(window.location.hash.slice(1));
const fragmentToken = fragment.get("token");

if (fragmentToken) {
  window.sessionStorage.setItem(sessionTokenKey, fragmentToken);
  window.history.replaceState(null, "", window.location.pathname + window.location.search);
}

const sessionToken = window.sessionStorage.getItem(sessionTokenKey);
const elements = {
  connectionPill: document.querySelector("#connection-pill"),
  connectionLabel: document.querySelector("#connection-label"),
  appVersion: document.querySelector("#app-version"),
  networkSummary: document.querySelector("#network-summary"),
  networkMode: document.querySelector("#network-mode"),
  networkSSID: document.querySelector("#network-ssid"),
  networkInterface: document.querySelector("#network-interface"),
  networkAddress: document.querySelector("#network-address"),
  networkNetmask: document.querySelector("#network-netmask"),
  networkGateway: document.querySelector("#network-gateway"),
  networkDNS: document.querySelector("#network-dns"),
  autoSwitchStatus: document.querySelector("#auto-switch-status"),
  operationStatus: document.querySelector("#operation-status"),
  applyRuleButton: document.querySelector("#apply-rule-button"),
  restoreDHCPButton: document.querySelector("#restore-dhcp-button"),
  ruleCount: document.querySelector("#rule-count"),
  ruleList: document.querySelector("#rule-list"),
  newRuleButton: document.querySelector("#new-rule-button"),
  settingsForm: document.querySelector("#settings-form"),
  autoSwitch: document.querySelector("#auto-switch"),
  unmatchedAction: document.querySelector("#unmatched-action"),
  language: document.querySelector("#language"),
  saveSettingsButton: document.querySelector("#save-settings-button"),
  autoStartToggle: document.querySelector("#autostart-toggle"),
  autoStartStatus: document.querySelector("#autostart-status"),
  refreshLogsButton: document.querySelector("#refresh-logs-button"),
  logOutput: document.querySelector("#log-output"),
  ruleDialog: document.querySelector("#rule-dialog"),
  ruleDialogTitle: document.querySelector("#rule-dialog-title"),
  ruleForm: document.querySelector("#rule-form"),
  ruleID: document.querySelector("#rule-id"),
  ruleName: document.querySelector("#rule-name"),
  ruleSSID: document.querySelector("#rule-ssid"),
  ruleEnabled: document.querySelector("#rule-enabled"),
  ipv4Mode: document.querySelector("#ipv4-mode"),
  staticFields: document.querySelector("#static-fields"),
  ipv4Address: document.querySelector("#ipv4-address"),
  ipv4Netmask: document.querySelector("#ipv4-netmask"),
  ipv4Gateway: document.querySelector("#ipv4-gateway"),
  ipv4DNS: document.querySelector("#ipv4-dns"),
  ruleFormError: document.querySelector("#rule-form-error"),
  saveRuleButton: document.querySelector("#save-rule-button"),
  closeRuleDialog: document.querySelector("#close-rule-dialog"),
  cancelRuleButton: document.querySelector("#cancel-rule-button"),
  deleteDialog: document.querySelector("#delete-dialog"),
  deleteDialogCopy: document.querySelector("#delete-dialog-copy"),
  cancelDeleteButton: document.querySelector("#cancel-delete-button"),
  confirmDeleteButton: document.querySelector("#confirm-delete-button"),
  restoreDHCPDialog: document.querySelector("#restore-dhcp-dialog"),
  cancelRestoreDHCPButton: document.querySelector("#cancel-restore-dhcp-button"),
  confirmRestoreDHCPButton: document.querySelector("#confirm-restore-dhcp-button"),
  toast: document.querySelector("#toast"),
};

let rules = [];
let pendingDeleteID = "";
let toastTimer = 0;
let networkRefreshTimer = 0;
let networkRefreshPending = false;
let networkOperationPending = false;
let latestRuntimeState = null;
let autoStartAvailable = false;
let autoStartEnabled = false;
let autoStartPending = false;
let logsPending = false;
let latestAutoStartState = null;
let latestAppInfo = null;
let latestLogEntries = null;

class APIError extends Error {
  constructor(message, code = "request_failed", field = "") {
    super(message);
    this.name = "APIError";
    this.code = code;
    this.field = field;
  }
}

async function apiFetch(path, options = {}) {
  if (!sessionToken) {
    throw new APIError(t("common.missingSession"), "missing_session");
  }

  const requestOptions = {...options};
  const headers = new Headers(requestOptions.headers || {});
  headers.set("Accept", "application/json");
  headers.set("X-Net-Switch-Token", sessionToken);
  if (requestOptions.body !== undefined && requestOptions.body !== null) {
    headers.set("Content-Type", "application/json");
    if (typeof requestOptions.body !== "string") {
      requestOptions.body = JSON.stringify(requestOptions.body);
    }
  }
  requestOptions.headers = headers;

  const response = await window.fetch(path, requestOptions);
  if (response.status === 204) {
    return null;
  }

  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new APIError(t("common.invalidResponse", {status: response.status}));
  }
  if (!response.ok) {
    const code = payload?.error?.code || "request_failed";
    throw new APIError(
      i18n.apiError(code, payload?.error?.message || t("common.requestFailed", {status: response.status})),
      code,
      payload?.error?.field,
    );
  }
  return payload;
}

window.netSwitchAPI = apiFetch;

function displayValue(value, fallback = "—") {
  return typeof value === "string" && value.length > 0 ? value : fallback;
}

function setConnection(connected, message) {
  elements.connectionPill.classList.toggle("offline", !connected);
  elements.connectionLabel.textContent = message;
}

function renderNetwork(runtimeState) {
  latestRuntimeState = runtimeState;
  const current = runtimeState?.network || {};
  const modeLabels = {dhcp: "DHCP", static: t("network.modeStatic"), unknown: t("network.modeUnknown")};
  const interfaceParts = [current.service, current.interface].filter(Boolean);

  elements.networkSSID.textContent = displayValue(current.ssid, t("network.noWiFi"));
  elements.networkInterface.textContent = interfaceParts.length > 0 ? interfaceParts.join(" / ") : "—";
  elements.networkAddress.textContent = displayValue(current.ipv4_address);
  elements.networkNetmask.textContent = displayValue(current.netmask);
  elements.networkGateway.textContent = displayValue(current.gateway);
  elements.networkDNS.textContent = Array.isArray(current.dns) && current.dns.length > 0 ? current.dns.join(", ") : "—";
  elements.networkMode.textContent = modeLabels[current.mode] || t("network.modeUnknown");

  if (current.status === "unavailable") {
    elements.networkSummary.textContent = i18n.message(current, "network.unavailable");
  } else if (current.status === "disconnected") {
    elements.networkSummary.textContent = i18n.message(current, "network.disconnected");
  } else if (current.ssid) {
    const comparison = runtimeState.target_comparison;
    if (!runtimeState.matched_rule_id) {
      elements.networkSummary.textContent = t("network.connectedNoRule", {ssid: current.ssid});
    } else if (!comparison?.comparable) {
      elements.networkSummary.textContent = t("network.connectedUnknown", {ssid: current.ssid, rule: runtimeState.matched_rule_id});
    } else if (comparison.matches) {
      elements.networkSummary.textContent = t("network.connectedMatched", {ssid: current.ssid, rule: runtimeState.matched_rule_id});
    } else {
      elements.networkSummary.textContent = t("network.connectedDifferent", {ssid: current.ssid, rule: runtimeState.matched_rule_id});
    }
  } else if (current.message) {
    elements.networkSummary.textContent = i18n.message(current);
  } else {
    elements.networkSummary.textContent = t("network.noSSID");
  }

  renderAutoSwitchStatus(runtimeState?.last_auto_switch);
  renderOperationStatus(runtimeState?.last_operation);
  updateNetworkActionAvailability();
}

function renderAutoSwitchStatus(status) {
  elements.autoSwitchStatus.classList.toggle("error", status?.decision === "failed");
  if (!status) {
    elements.autoSwitchStatus.textContent = t("network.autoPending");
    return;
  }
  const checkedAt = new Date(status.checked_at);
  const timeLabel = Number.isNaN(checkedAt.getTime()) ? "" : ` · ${checkedAt.toLocaleTimeString(i18n.language, {hour: "2-digit", minute: "2-digit"})}`;
  const ruleLabel = status.matched_rule ? ` · ${status.matched_rule}` : "";
  elements.autoSwitchStatus.textContent = `${i18n.message(status, "network.autoDone")}${ruleLabel}${timeLabel}`;
}

function renderOperationStatus(operation) {
  elements.operationStatus.classList.remove("success", "error");
  if (!operation) {
    elements.operationStatus.textContent = t("network.operationNone");
    return;
  }

  const actionLabels = {apply_rule: t("network.actionApply"), restore_dhcp: t("network.actionRestore")};
  const triggerLabel = operation.trigger === "automatic" ? t("network.triggerAuto") : t("network.triggerManual");
  const ruleLabel = operation.rule_name ? ` · ${operation.rule_name}` : "";
  const completedAt = new Date(operation.completed_at);
  const timeLabel = Number.isNaN(completedAt.getTime()) ? "" : ` · ${completedAt.toLocaleTimeString(i18n.language, {hour: "2-digit", minute: "2-digit"})}`;
  const dryRunLabel = operation.dry_run ? " · dry-run" : "";
  elements.operationStatus.textContent = `${triggerLabel} ${actionLabels[operation.action] || t("network.actionGeneric")}: ${i18n.message(operation, "network.operationDone")}${ruleLabel}${dryRunLabel}${timeLabel}`;
  elements.operationStatus.classList.add(operation.success ? "success" : "error");
}

function updateNetworkActionAvailability() {
  const current = latestRuntimeState?.network || {};
  const hasReadableConfiguration = ["dhcp", "static"].includes(current.mode)
    && ["automatic", "manual"].includes(current.dns_mode);
  elements.applyRuleButton.disabled = networkOperationPending
    || current.status !== "connected"
    || !current.ssid
    || !latestRuntimeState?.matched_rule_id;
  elements.restoreDHCPButton.disabled = networkOperationPending
    || !current.service
    || !current.interface
    || !hasReadableConfiguration;
}

function setNetworkOperationBusy(button, busy, label = t("common.processing")) {
  networkOperationPending = busy;
  if (busy) {
    button.dataset.originalLabel = button.textContent;
    button.textContent = `${label}…`;
  } else {
    button.textContent = button.dataset.originalLabel || button.textContent;
    delete button.dataset.originalLabel;
  }
  updateNetworkActionAvailability();
}

async function runNetworkOperation(path, button, busyLabel) {
  if (networkOperationPending) {
    return;
  }
  setNetworkOperationBusy(button, true, busyLabel);
  try {
    const result = await apiFetch(path, {method: "POST"});
    renderOperationStatus(result);
    showToast(i18n.message(result, "network.operationCompleted"));
  } catch (error) {
    showToast(error.message, true);
  } finally {
    setNetworkOperationBusy(button, false);
    await refreshNetworkState();
    await refreshLogs(false);
  }
}

function renderConfiguration(configuration) {
  const general = configuration?.general || {};
  i18n.setLanguage(general.language);
  elements.autoSwitch.checked = Boolean(general.auto_switch);
  elements.unmatchedAction.value = general.unmatched_action === "dhcp" ? "dhcp" : "keep";
  elements.language.value = i18n.normalize(general.language);
  rules = Array.isArray(configuration?.rules) ? configuration.rules : [];
  renderRules();
}

function renderAppInfo(info) {
  latestAppInfo = info;
  const version = typeof info?.version === "string" ? info.version.trim() : "";
  elements.appVersion.textContent = version ? `v${version}` : t("version.unknown");
}

function renderAutoStart(state) {
  latestAutoStartState = state;
  autoStartAvailable = Boolean(state?.available);
  autoStartEnabled = autoStartAvailable && Boolean(state?.enabled);
  elements.autoStartToggle.checked = autoStartEnabled;
  elements.autoStartToggle.disabled = !autoStartAvailable || autoStartPending;
  if (!autoStartAvailable) {
    elements.autoStartStatus.textContent = i18n.message(state, "autostart.unavailable");
  } else if (autoStartPending) {
    elements.autoStartStatus.textContent = t("autostart.updating");
  } else {
    elements.autoStartStatus.textContent = autoStartEnabled
      ? t("autostart.enabled")
      : t("autostart.disabled");
  }
}

async function updateAutoStart() {
  if (!autoStartAvailable || autoStartPending) {
    return;
  }
  const desired = elements.autoStartToggle.checked;
  const previous = autoStartEnabled;
  autoStartPending = true;
  renderAutoStart({available: true, enabled: desired});
  try {
    const state = await apiFetch("/api/v1/autostart", {
      method: "PUT",
      body: {enabled: desired},
    });
    autoStartPending = false;
    renderAutoStart(state);
    showToast(state.enabled ? t("autostart.enabledToast") : t("autostart.disabledToast"));
  } catch (error) {
    autoStartPending = false;
    renderAutoStart({available: true, enabled: previous});
    showToast(error.message, true);
  }
}

function renderLogs(entries) {
  latestLogEntries = Array.isArray(entries) ? entries : [];
  elements.logOutput.classList.remove("error");
  elements.logOutput.textContent = latestLogEntries.length > 0
    ? latestLogEntries.join("\n")
    : t("logs.empty");
  elements.logOutput.scrollTop = elements.logOutput.scrollHeight;
}

async function refreshLogs(showFeedback = true) {
  if (logsPending) {
    return;
  }
  logsPending = true;
  setButtonBusy(elements.refreshLogsButton, true, t("logs.refreshing"));
  try {
    const response = await apiFetch("/api/v1/logs?limit=100");
    renderLogs(response?.entries);
    if (showFeedback) {
      showToast(t("logs.refreshed"));
    }
  } catch (error) {
    elements.logOutput.classList.add("error");
    elements.logOutput.textContent = error.message;
    if (showFeedback) {
      showToast(error.message, true);
    }
  } finally {
    logsPending = false;
    setButtonBusy(elements.refreshLogsButton, false);
  }
}

async function refreshNetworkState() {
  if (networkRefreshPending || document.hidden) {
    return;
  }
  networkRefreshPending = true;
  try {
    renderNetwork(await apiFetch("/api/v1/state"));
    setConnection(true, t("connection.connected"));
  } catch (error) {
    setConnection(false, t("connection.failed"));
    elements.networkSummary.textContent = error.message;
  } finally {
    networkRefreshPending = false;
  }
}

function startNetworkRefresh() {
  if (networkRefreshTimer) {
    return;
  }
  networkRefreshTimer = window.setInterval(refreshNetworkState, networkRefreshInterval);
}

function renderRules() {
  elements.ruleCount.textContent = String(rules.length);
  elements.ruleList.replaceChildren();
  elements.ruleList.setAttribute("aria-busy", "false");

  if (rules.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty-state";
    const mark = document.createElement("span");
    mark.className = "empty-mark";
    mark.setAttribute("aria-hidden", "true");
    mark.textContent = "⌁";
    const copy = document.createElement("p");
    const title = document.createElement("strong");
    title.textContent = t("rules.emptyTitle");
    copy.append(title, document.createElement("br"), document.createTextNode(t("rules.emptyCopy")));
    empty.append(mark, copy);
    elements.ruleList.append(empty);
    return;
  }

  for (const configuredRule of rules) {
    elements.ruleList.append(createRuleCard(configuredRule));
  }
}

function createRuleCard(configuredRule) {
  const item = document.createElement("article");
  item.className = `rule-item${configuredRule.enabled ? "" : " disabled"}`;
  item.setAttribute("role", "listitem");

  const heading = document.createElement("div");
  heading.className = "rule-heading";
  const titleGroup = document.createElement("div");
  const titleRow = document.createElement("div");
  titleRow.className = "rule-title-row";
  const title = document.createElement("h3");
  title.textContent = configuredRule.name;
  const status = document.createElement("span");
  status.className = `rule-status${configuredRule.enabled ? "" : " off"}`;
  status.textContent = configuredRule.enabled ? t("rules.enabled") : t("rules.disabled");
  titleRow.append(title, status);
  const ssid = document.createElement("p");
  ssid.className = "rule-meta";
  ssid.textContent = `SSID · ${configuredRule.ssid}`;
  titleGroup.append(titleRow, ssid);
  const mode = document.createElement("span");
  mode.className = "mode-label";
  mode.textContent = configuredRule.ipv4?.mode === "static" ? t("rules.staticShort") : "DHCP";
  heading.append(titleGroup, mode);

  const details = document.createElement("div");
  details.className = "rule-details";
  if (configuredRule.ipv4?.mode === "static") {
    details.append(
      createRuleDetail(t("rules.address"), configuredRule.ipv4.address),
      createRuleDetail(t("rules.gateway"), configuredRule.ipv4.gateway),
      createRuleDetail("DNS", Array.isArray(configuredRule.ipv4.dns) ? configuredRule.ipv4.dns.join(", ") : "—"),
    );
  } else {
    details.append(createRuleDetail(t("rules.addressMode"), t("rules.dhcpManaged")));
  }

  const actions = document.createElement("div");
  actions.className = "rule-actions";
  actions.append(
    createActionButton(configuredRule.enabled ? t("rules.disable") : t("rules.enable"), "toggle", configuredRule.id),
    createActionButton(t("rules.edit"), "edit", configuredRule.id),
    createActionButton(t("rules.delete"), "delete", configuredRule.id, true),
  );
  item.append(heading, details, actions);
  return item;
}

function createRuleDetail(label, value) {
  const detail = document.createElement("span");
  const labelElement = document.createElement("strong");
  labelElement.textContent = `${label} `;
  detail.append(labelElement, document.createTextNode(displayValue(value)));
  return detail;
}

function createActionButton(label, action, id, dangerous = false) {
  const button = document.createElement("button");
  button.className = `text-button${dangerous ? " delete" : ""}`;
  button.type = "button";
  button.dataset.action = action;
  button.dataset.id = id;
  button.textContent = label;
  return button;
}

function showRulesError(message) {
  elements.ruleList.replaceChildren();
  elements.ruleList.setAttribute("aria-busy", "false");
  const state = document.createElement("div");
  state.className = "empty-state";
  const title = document.createElement("strong");
  title.textContent = t("rules.loadFailed");
  const copy = document.createElement("p");
  copy.append(title, document.createElement("br"), document.createTextNode(message));
  state.append(copy);
  elements.ruleList.append(state);
}

function updateStaticFields() {
  const isStatic = elements.ipv4Mode.value === "static";
  elements.staticFields.hidden = !isStatic;
  for (const input of [elements.ipv4Address, elements.ipv4Netmask, elements.ipv4Gateway]) {
    input.required = isStatic;
  }
}

function openNewRuleDialog() {
  elements.ruleForm.reset();
  elements.ruleID.value = "";
  elements.ruleEnabled.checked = true;
  const current = latestRuntimeState?.network || {};
  const currentSSID = typeof current.ssid === "string" ? current.ssid : "";
  const hasCurrentIPv4 = Boolean(current.ipv4_address && current.netmask && current.gateway);
  elements.ruleName.value = currentSSID ? t("rules.defaultName", {ssid: currentSSID}) : "";
  elements.ruleSSID.value = currentSSID;
  elements.ipv4Mode.value = hasCurrentIPv4 ? "static" : "dhcp";
  elements.ipv4Address.value = current.ipv4_address || "";
  elements.ipv4Netmask.value = current.netmask || "";
  elements.ipv4Gateway.value = current.gateway || "";
  elements.ipv4DNS.value = Array.isArray(current.dns) ? current.dns.join(", ") : "";
  elements.ruleDialogTitle.textContent = t("rules.dialogNew");
  elements.ruleFormError.hidden = true;
  updateStaticFields();
  showDialog(elements.ruleDialog);
  (currentSSID ? elements.ruleName : elements.ruleSSID).focus();
}

function openEditRuleDialog(id) {
  const configuredRule = rules.find((item) => item.id === id);
  if (!configuredRule) {
    showToast(t("rules.notFoundEdit"), true);
    return;
  }

  elements.ruleForm.reset();
  elements.ruleID.value = configuredRule.id;
  elements.ruleName.value = configuredRule.name;
  elements.ruleSSID.value = configuredRule.ssid;
  elements.ruleEnabled.checked = Boolean(configuredRule.enabled);
  elements.ipv4Mode.value = configuredRule.ipv4?.mode === "static" ? "static" : "dhcp";
  elements.ipv4Address.value = configuredRule.ipv4?.address || "";
  elements.ipv4Netmask.value = configuredRule.ipv4?.netmask || "";
  elements.ipv4Gateway.value = configuredRule.ipv4?.gateway || "";
  elements.ipv4DNS.value = Array.isArray(configuredRule.ipv4?.dns) ? configuredRule.ipv4.dns.join(", ") : "";
  elements.ruleDialogTitle.textContent = t("rules.dialogEdit");
  elements.ruleFormError.hidden = true;
  updateStaticFields();
  showDialog(elements.ruleDialog);
  elements.ruleName.focus();
}

function buildRulePayload() {
  const ipv4 = {mode: elements.ipv4Mode.value};
  if (elements.ipv4Mode.value === "static") {
    ipv4.address = elements.ipv4Address.value.trim();
    ipv4.netmask = elements.ipv4Netmask.value.trim();
    ipv4.gateway = elements.ipv4Gateway.value.trim();
    const dns = elements.ipv4DNS.value.split(",").map((value) => value.trim()).filter(Boolean);
    if (dns.length > 0) {
      ipv4.dns = dns;
    }
  }
  return {
    name: elements.ruleName.value.trim(),
    ssid: elements.ruleSSID.value,
    enabled: elements.ruleEnabled.checked,
    ipv4,
  };
}

async function saveRule(event) {
  event.preventDefault();
  elements.ruleFormError.hidden = true;
  if (!elements.ruleForm.reportValidity()) {
    return;
  }

  const id = elements.ruleID.value;
  const editing = id.length > 0;
  setButtonBusy(elements.saveRuleButton, true, editing ? t("rules.saving") : t("rules.creating"));
  try {
    const saved = await apiFetch(editing ? `/api/v1/rules/${encodeURIComponent(id)}` : "/api/v1/rules", {
      method: editing ? "PUT" : "POST",
      body: buildRulePayload(),
    });
    if (editing) {
      rules = rules.map((item) => item.id === saved.id ? saved : item);
    } else {
      rules = [...rules, saved];
    }
    renderRules();
    closeDialog(elements.ruleDialog);
    showToast(editing ? t("rules.saved") : t("rules.created"));
  } catch (error) {
    elements.ruleFormError.textContent = error.message;
    elements.ruleFormError.hidden = false;
    focusErrorField(error.field);
  } finally {
    setButtonBusy(elements.saveRuleButton, false);
  }
}

function focusErrorField(field) {
  const fieldMap = {
    name: elements.ruleName,
    ssid: elements.ruleSSID,
    address: elements.ipv4Address,
    netmask: elements.ipv4Netmask,
    gateway: elements.ipv4Gateway,
    dns: elements.ipv4DNS,
  };
  const suffix = String(field || "").split(".").pop()?.replace(/\[\d+\]$/, "");
  fieldMap[suffix]?.focus();
}

async function handleRuleAction(event) {
  const button = event.target.closest("button[data-action]");
  if (!button || !elements.ruleList.contains(button)) {
    return;
  }
  const {action, id} = button.dataset;
  if (action === "edit") {
    openEditRuleDialog(id);
    return;
  }
  if (action === "delete") {
    const configuredRule = rules.find((item) => item.id === id);
    pendingDeleteID = id;
    elements.deleteDialogCopy.textContent = configuredRule
      ? t("rules.deleteNamed", {name: configuredRule.name})
      : t("rules.deleteCopy");
    showDialog(elements.deleteDialog);
    return;
  }
  if (action !== "toggle") {
    return;
  }

  const configuredRule = rules.find((item) => item.id === id);
  if (!configuredRule) {
    showToast(t("rules.notFound"), true);
    return;
  }
  button.disabled = true;
  try {
    const updated = await apiFetch(`/api/v1/rules/${encodeURIComponent(id)}/enabled`, {
      method: "PUT",
      body: {enabled: !configuredRule.enabled},
    });
    rules = rules.map((item) => item.id === updated.id ? updated : item);
    renderRules();
    showToast(updated.enabled ? t("rules.activated") : t("rules.deactivated"));
  } catch (error) {
    showToast(error.message, true);
    button.disabled = false;
  }
}

async function deleteRule() {
  if (!pendingDeleteID) {
    closeDialog(elements.deleteDialog);
    return;
  }
  const id = pendingDeleteID;
  setButtonBusy(elements.confirmDeleteButton, true, t("rules.deleting"));
  try {
    await apiFetch(`/api/v1/rules/${encodeURIComponent(id)}`, {method: "DELETE"});
    rules = rules.filter((item) => item.id !== id);
    pendingDeleteID = "";
    renderRules();
    closeDialog(elements.deleteDialog);
    showToast(t("rules.deleted"));
  } catch (error) {
    showToast(error.message, true);
  } finally {
    setButtonBusy(elements.confirmDeleteButton, false);
  }
}

async function saveSettings(event) {
  event.preventDefault();
  setButtonBusy(elements.saveSettingsButton, true, t("settings.saving"));
  try {
    const updated = await apiFetch("/api/v1/settings", {
      method: "PUT",
      body: {
        auto_switch: elements.autoSwitch.checked,
        unmatched_action: elements.unmatchedAction.value,
        language: elements.language.value,
      },
    });
    i18n.setLanguage(updated.language);
    elements.saveSettingsButton.dataset.originalLabel = t("settings.save");
    elements.language.value = i18n.normalize(updated.language);
    renderRules();
    if (latestRuntimeState) renderNetwork(latestRuntimeState);
    if (latestAutoStartState) renderAutoStart(latestAutoStartState);
    if (latestAppInfo) renderAppInfo(latestAppInfo);
    if (latestLogEntries !== null) renderLogs(latestLogEntries);
    setConnection(true, t("connection.connected"));
    showToast(t("settings.saved"));
  } catch (error) {
    showToast(error.message, true);
  } finally {
    setButtonBusy(elements.saveSettingsButton, false);
  }
}

function setButtonBusy(button, busy, busyLabel = t("common.processing")) {
  if (busy) {
    button.dataset.originalLabel = button.textContent;
    button.textContent = `${busyLabel}…`;
    button.disabled = true;
    return;
  }
  button.textContent = button.dataset.originalLabel || button.textContent;
  button.disabled = false;
  delete button.dataset.originalLabel;
}

function showDialog(dialog) {
  if (typeof dialog.showModal === "function") {
    dialog.showModal();
  } else {
    dialog.setAttribute("open", "");
  }
}

function closeDialog(dialog) {
  if (typeof dialog.close === "function") {
    dialog.close();
  } else {
    dialog.removeAttribute("open");
  }
}

function showToast(message, isError = false) {
  window.clearTimeout(toastTimer);
  elements.toast.textContent = message;
  elements.toast.classList.toggle("error", isError);
  elements.toast.hidden = false;
  toastTimer = window.setTimeout(() => {
    elements.toast.hidden = true;
  }, 3600);
}

async function initialize() {
  if (window.location.hostname !== "127.0.0.1" || !sessionToken) {
    setConnection(false, t("connection.pending"));
    showRulesError(t("common.reopenDashboard"));
    elements.networkSummary.textContent = t("common.sessionUnavailable");
    elements.newRuleButton.disabled = true;
    elements.saveSettingsButton.disabled = true;
    return;
  }

  try {
    const [configuration, runtimeState, autoStartState, appInfo] = await Promise.all([
      apiFetch("/api/v1/config"),
      apiFetch("/api/v1/state"),
      apiFetch("/api/v1/autostart"),
      apiFetch("/api/v1/info"),
    ]);
    renderConfiguration(configuration);
    renderNetwork(runtimeState);
    renderAutoStart(autoStartState);
    renderAppInfo(appInfo);
    elements.newRuleButton.disabled = false;
    elements.saveSettingsButton.disabled = false;
    elements.refreshLogsButton.disabled = false;
    setConnection(true, t("connection.connected"));
    await refreshLogs(false);
  } catch (error) {
    setConnection(false, t("connection.failed"));
    showRulesError(error.message);
    elements.networkSummary.textContent = error.message;
    showToast(error.message, true);
  }
  startNetworkRefresh();
}

elements.newRuleButton.addEventListener("click", openNewRuleDialog);
elements.applyRuleButton.addEventListener("click", () => {
  runNetworkOperation("/api/v1/network/apply", elements.applyRuleButton, t("network.applying"));
});
elements.restoreDHCPButton.addEventListener("click", () => showDialog(elements.restoreDHCPDialog));
elements.ruleList.addEventListener("click", handleRuleAction);
elements.ipv4Mode.addEventListener("change", updateStaticFields);
elements.ruleForm.addEventListener("submit", saveRule);
elements.settingsForm.addEventListener("submit", saveSettings);
elements.autoStartToggle.addEventListener("change", updateAutoStart);
elements.refreshLogsButton.addEventListener("click", () => refreshLogs(true));
elements.closeRuleDialog.addEventListener("click", () => closeDialog(elements.ruleDialog));
elements.cancelRuleButton.addEventListener("click", () => closeDialog(elements.ruleDialog));
elements.cancelDeleteButton.addEventListener("click", () => {
  pendingDeleteID = "";
  closeDialog(elements.deleteDialog);
});
elements.confirmDeleteButton.addEventListener("click", deleteRule);
elements.cancelRestoreDHCPButton.addEventListener("click", () => closeDialog(elements.restoreDHCPDialog));
elements.confirmRestoreDHCPButton.addEventListener("click", () => {
  closeDialog(elements.restoreDHCPDialog);
  runNetworkOperation("/api/v1/network/restore-dhcp", elements.restoreDHCPButton, t("network.restoring"));
});
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) {
    refreshNetworkState();
  }
});

i18n.setLanguage("zh-CN");
initialize();
