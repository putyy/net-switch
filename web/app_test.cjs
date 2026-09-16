const assert = require("node:assert/strict");
const {readFileSync} = require("node:fs");
const {test} = require("node:test");
const vm = require("node:vm");

function dashboard(fetch) {
  const nodes = new Map();
  const node = () => ({
    checked: false, disabled: false, dataset: {}, textContent: "",
    listeners: {}, classList: {toggle() {}, add() {}, remove() {}},
    addEventListener(event, handler) { this.listeners[event] = handler; },
    replaceChildren() {}, setAttribute() {}, append() {},
  });
  const document = {
    querySelector(selector) {
      if (!nodes.has(selector)) nodes.set(selector, node());
      return nodes.get(selector);
    },
    createElement: node, createTextNode: node, addEventListener() {},
  };
  const context = vm.createContext({
    document, URLSearchParams, Headers,
    window: {
      // Skip initialization requests so each test controls loading explicitly.
      location: {hostname: "", hash: ""},
      sessionStorage: {getItem: () => "test-token"},
      NetSwitchI18n: {t: key => key, setLanguage() {}, normalize: value => value, apiError: (_, message) => message},
      fetch, setTimeout() {}, clearTimeout() {},
    },
  });
  vm.runInContext(readFileSync(__dirname + "/app.js", "utf8"), context);
  return {
    get: selector => document.querySelector(selector),
    load(general) {
      context.configuration = {general, rules: []};
      vm.runInContext("renderConfiguration(configuration)", context);
    },
  };
}

const initialSettings = {exit_after_login: false, auto_switch: true, unmatched_action: "dhcp", language: "zh-CN"};
const changes = [
  ["#auto-switch", "checked", "auto_switch", false],
  ["#unmatched-action", "value", "unmatched_action", "keep"],
  ["#language", "value", "language", "en"],
  ["#exit-after-login", "checked", "exit_after_login", true],
];

test("all general settings save on change and survive reload", async () => {
  let saved = {...initialSettings};
  let requests = 0;
  const fetch = async (path, options) => {
    requests++;
    assert.equal(path, "/api/v1/settings");
    assert.equal(options.method, "PUT");
    saved = JSON.parse(options.body);
    return {ok: true, status: 200, json: async () => ({...saved})};
  };
  const ui = dashboard(fetch);
  ui.load(saved);
  for (const [selector, property, field, value] of changes) {
    const control = ui.get(selector);
    control[property] = value;
    const saving = control.listeners.change();
    for (const [other] of changes) assert.equal(ui.get(other).disabled, true);
    assert.equal(ui.get("#settings-status").textContent, "settings.saving");
    await saving;
    for (const [other] of changes) assert.equal(ui.get(other).disabled, false);
    assert.equal(saved[field], value);
    assert.equal(ui.get("#settings-status").textContent, "settings.saved");
    const refreshed = dashboard(fetch);
    refreshed.load(saved);
    for (const [other, prop, key] of changes) {
      assert.equal(refreshed.get(other)[prop], saved[key]);
    }
  }
  assert.equal(requests, changes.length);
});

for (const [selector, property, field, value] of changes) {
  test(`failed ${field} save restores the previous value and prevents overlapping writes`, async () => {
    let fail;
    let requests = 0;
    const ui = dashboard(() => {
      requests++;
      return new Promise((_, reject) => { fail = reject; });
    });
    ui.load({...initialSettings});
    const control = ui.get(selector);
    control[property] = value;
    const saving = control.listeners.change();
    for (const [other] of changes) await ui.get(other).listeners.change();
    assert.equal(requests, 1);
    fail(new Error("Could not save configuration"));
    await saving;
    assert.equal(control[property], initialSettings[field]);
    for (const [other] of changes) assert.equal(ui.get(other).disabled, false);
    assert.equal(ui.get("#settings-status").textContent, "settings.saveFailed");
    assert.equal(ui.get("#toast").textContent, "Could not save configuration");
  });
}

test("settings cannot save before configuration has loaded", async () => {
  let requests = 0;
  const ui = dashboard(() => { requests++; });
  for (const [selector] of changes) {
    assert.equal(ui.get(selector).disabled, true);
    await ui.get(selector).listeners.change();
  }
  assert.equal(requests, 0);
});
