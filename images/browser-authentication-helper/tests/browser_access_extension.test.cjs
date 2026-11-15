const assert = require('node:assert/strict');
const fs = require('node:fs');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(new URL('../browser_access_extension.js', `file://${__filename}`), 'utf8');

function event() {
  const listeners = [];
  return {
    addListener(listener) { listeners.push(listener); },
    async dispatch(...args) {
      for (const listener of listeners) await listener(...args);
    },
  };
}

function extension(config, initialTabs = [], rules = [], dnrApi = undefined) {
  const calls = {removed: [], alarms: [], badges: [], titles: [], sessionRuleUpdates: []};
  const timers = [];
  const storage = new Map();
  const sessionRules = [];
  let now = 1000;
  const chrome = {
    runtime: {
      getURL(path) { return `extension://${path}`; },
      onStartup: event(),
      onInstalled: event(),
    },
    tabs: {
      onCreated: event(),
      onUpdated: event(),
      onRemoved: event(),
      async query() { return initialTabs; },
      async remove(id) {
        calls.removed.push(id);
        const index = initialTabs.findIndex(tab => tab.id === id);
        if (index !== -1) initialTabs.splice(index, 1);
      },
    },
    storage: {session: {
      async set(values) { for (const [key, value] of Object.entries(values)) storage.set(key, value); },
      async get(key) { return {[key]: storage.get(key)}; },
      async remove(key) { storage.delete(key); },
    }},
    alarms: {
      create(name, options) { calls.alarms.push({name, options}); },
      clear() {},
      onAlarm: event(),
    },
    action: {
      async setBadgeText(value) { calls.badges.push(value.text); },
      async setBadgeBackgroundColor() {},
      async setTitle(value) { calls.titles.push(value.title); },
    },
    declarativeNetRequest: dnrApi || {
      async getSessionRules() { return [...sessionRules]; },
      async updateSessionRules({removeRuleIds = [], addRules = []}) {
        calls.sessionRuleUpdates.push({removeRuleIds, addRules});
        for (const id of removeRuleIds) {
          const index = sessionRules.findIndex(rule => rule.id === id);
          if (index !== -1) sessionRules.splice(index, 1);
        }
        sessionRules.push(...addRules);
      },
      async getEnabledRulesets() { return ['blocked-urls']; },
      async isRegexSupported() { return {isSupported: true}; },
    },
  };
  const context = {
    chrome,
    URL,
    Date: class extends Date { static now() { return now; } },
    Number,
    Map,
    setTimeout(callback) { timers.push(callback); return timers.length; },
    fetch: async url => ({json: async () => String(url).endsWith('navigation_rules.json') ? rules : config}),
  };
  vm.runInNewContext(source, context, {filename: 'browser_access_extension.js'});
  return {
    tabs: initialTabs,
    calls,
    chrome,
    sessionRules,
    async created(tab) {
      this.tabs.push(tab);
      await chrome.tabs.onCreated.dispatch(tab);
      await this.flush();
    },
    async updated(tab, changeInfo) {
      await chrome.tabs.onUpdated.dispatch(tab.id, changeInfo, tab);
      await this.flush();
    },
    async expireGrace() {
      now += 15_000;
      await chrome.alarms.onAlarm.dispatch({name: 'aact-popup-2'});
      await this.flush();
    },
    advance(ms) { now += ms; },
    async flush() {
      await Promise.resolve();
      while (timers.length) {
        timers.shift()();
        await new Promise(resolve => setImmediate(resolve));
      }
      await new Promise(resolve => setImmediate(resolve));
      await new Promise(resolve => setImmediate(resolve));
    },
  };
}

test('failed session-rule update shows a visible warning and retains fail-closed barrier', async () => {
  const browser = extension({blocked_urls: ['https://denied.example']}, [], [{
    id: 1,
    condition: {regexFilter: '^https://denied\\.example(?:/|[?#]|$)'},
  }], {
    async getSessionRules() { return [{id: 999, priority: 2}]; },
    async updateSessionRules(options) {
      if (options.addRules?.length) throw new Error('synthetic DNR update failure');
    },
    async getEnabledRulesets() { return ['blocked-urls']; },
    async isRegexSupported() { return {isSupported: true}; },
  });
  await browser.chrome.runtime.onStartup.dispatch();
  assert.deepEqual(browser.calls.badges, ['!']);
  assert.match(browser.calls.titles[0], /fail-closed mode/);
});

test('unsupported configured regex leaves all navigation blocked', async () => {
  const browser = extension({blocked_urls: ['https://denied.example']}, [], [{
    id: 1,
    condition: {regexFilter: '^https://denied\\.example(?:/|[?#]|$)'},
  }], {
    async getSessionRules() { return []; },
    async updateSessionRules() {},
    async getEnabledRulesets() { return ['blocked-urls']; },
    async isRegexSupported() { return {isSupported: false}; },
  });
  await browser.chrome.runtime.onStartup.dispatch();
  assert.deepEqual(browser.calls.badges, ['!']);
  assert.deepEqual(browser.calls.sessionRuleUpdates, []);
});

test('session barrier keeps policy active when Chromium does not enable the static ruleset', async () => {
  const rules = [];
  const calls = [];
  const browser = extension({blocked_urls: ['https://denied.example']}, [], [
    {id: 1000, priority: 2, action: {type: 'allow'}, condition: {regexFilter: '^https?://'}},
    {id: 2001, priority: 3, action: {type: 'block'}, condition: {regexFilter: '^https://denied\\.example(?:/|[?#]|$)'}},
  ], {
    async getSessionRules() { return [...rules]; },
    async updateSessionRules({removeRuleIds = [], addRules = []}) {
      calls.push({removeRuleIds, addRules});
      for (const id of removeRuleIds) {
        const index = rules.findIndex(rule => rule.id === id);
        if (index !== -1) rules.splice(index, 1);
      }
      rules.push(...addRules);
    },
    async getEnabledRulesets() { return []; },
    async isRegexSupported() { return {isSupported: true}; },
  });
  await browser.chrome.runtime.onStartup.dispatch();
  assert.deepEqual(browser.calls.badges, []);
  assert.equal(rules.find(rule => rule.id === 900)?.action.type, 'block');
  assert.equal(rules.find(rule => rule.id === 1000)?.action.type, 'allow');
  assert.equal(rules.find(rule => rule.id === 2001)?.priority, 3);
  assert.equal(calls.length, 2);
});

test('shows an unrestricted-policy warning if neither static nor session barrier can install', async () => {
  const browser = extension({blocked_urls: ['https://denied.example']}, [], [], {
    async getSessionRules() { return []; },
    async updateSessionRules() { throw new Error('synthetic DNR unavailable'); },
    async getEnabledRulesets() { return []; },
    async isRegexSupported() { return {isSupported: true}; },
  });
  await browser.chrome.runtime.onStartup.dispatch();
  assert.deepEqual(browser.calls.badges, ['!']);
  assert.match(browser.calls.titles[0], /navigation may be unrestricted/);
});

test('blocked URL rules install validated allow then higher-priority deny session rules', async () => {
  const browser = extension({blocked_urls: ['https://denied.example']}, [], [
    {id: 1000, priority: 2, action: {type: 'allow'}, condition: {regexFilter: '^https?://'}},
    {id: 2001, priority: 3, action: {type: 'block'}, condition: {regexFilter: '^https://denied\\.example(?:/|[?#]|$)'}},
  ]);
  await browser.chrome.runtime.onStartup.dispatch();
  assert.deepEqual(browser.calls.badges, []);
  assert.equal(browser.calls.sessionRuleUpdates.length, 2);
  const rules = browser.calls.sessionRuleUpdates[1].addRules;
  assert.deepEqual(rules.map(rule => [rule.action.type, rule.priority]), [['allow', 2], ['block', 3]]);
  assert.deepEqual(browser.sessionRules.map(rule => rule.id), [1000, 2001]);
});

test('popup remains open while blank, then through a matching redirect within its exemption grace', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 1,
    hard_max_tabs: 3,
    per_pattern: [],
    popup_exemptions: [{pattern: 'https://login.example', max_tabs: 1, grace_seconds: 15}],
  }}, [{id: 1, url: 'https://app.example', windowId: 1}]);

  const popup = {id: 2, openerTabId: 1, pendingUrl: 'about:blank', windowId: 1};
  await browser.created(popup);
  assert.deepEqual(browser.calls.removed, []);

  popup.url = 'https://login.example/authorize';
  popup.pendingUrl = popup.url;
  await browser.updated(popup, {url: popup.url});
  assert.deepEqual(browser.calls.removed, []);
});

test('popup opener tab id zero is treated as a valid opener id', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 1,
    hard_max_tabs: 3,
    per_pattern: [],
    popup_exemptions: [{pattern: 'https://login.example', max_tabs: 1, grace_seconds: 15}],
  }}, [{id: 0, url: 'https://app.example', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 0, pendingUrl: 'https://login.example/authorize', windowId: 1});
  assert.deepEqual(browser.calls.removed, []);
});

test('hard cap closes the newly created popup even when it qualifies for an exemption', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 1,
    hard_max_tabs: 2,
    per_pattern: [],
    popup_exemptions: [{pattern: 'https://login.example', max_tabs: 2, grace_seconds: 15}],
  }}, [{id: 1, url: 'https://app.example', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 1, pendingUrl: 'https://login.example/authorize', windowId: 1});
  assert.deepEqual(browser.calls.removed, []);

  await browser.created({id: 3, openerTabId: 1, pendingUrl: 'https://login.example/authorize', windowId: 1});
  assert.deepEqual(browser.calls.removed, [3]);
});

test('hard cap closes the newest of multiple eligible created tabs', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 1,
    hard_max_tabs: 2,
    per_pattern: [],
    popup_exemptions: [{pattern: 'https://login.example', max_tabs: 3, grace_seconds: 15}],
  }}, [{id: 1, url: 'https://app.example', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 1, pendingUrl: 'https://login.example/first', windowId: 1});
  browser.advance(1000);
  await browser.created({id: 3, openerTabId: 1, pendingUrl: 'https://login.example/second', windowId: 1});
  assert.deepEqual(browser.calls.removed, [3]);
});

test('popup exemption does not bypass a matching per-pattern limit', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 3,
    hard_max_tabs: 4,
    per_pattern: [{pattern: 'https://login.example', max_tabs: 1}],
    popup_exemptions: [{pattern: 'https://login.example', max_tabs: 2, grace_seconds: 15}],
  }}, [{id: 1, url: 'https://login.example/first', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 1, pendingUrl: 'https://login.example/authorize', windowId: 1});
  assert.deepEqual(browser.calls.removed, [2]);
});

test('popup-exemption max is enforced independently of global and per-pattern limits', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 2,
    hard_max_tabs: 4,
    per_pattern: [{pattern: 'https://other.example', max_tabs: 2}],
    popup_exemptions: [{pattern: 'https://login.example', max_tabs: 1, grace_seconds: 15}],
  }}, [{id: 1, url: 'https://app.example', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 1, pendingUrl: 'https://login.example/first', windowId: 1});
  await browser.created({id: 3, openerTabId: 1, pendingUrl: 'https://login.example/second', windowId: 1});
  assert.deepEqual(browser.calls.removed, [3]);
});

test('path matching follows Chrome path-prefix behavior without a path segment boundary', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 3,
    hard_max_tabs: 4,
    per_pattern: [{pattern: 'https://app.example/foo', max_tabs: 1}],
    popup_exemptions: [],
  }}, [{id: 1, url: 'https://app.example/foo', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 1, pendingUrl: 'https://app.example/foobar', windowId: 1});
  assert.deepEqual(browser.calls.removed, [2]);
});

test('popup that remains blank is removed when its exemption grace expires over the ordinary cap', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 1,
    hard_max_tabs: 3,
    per_pattern: [],
    popup_exemptions: [{pattern: 'https://login.example', max_tabs: 1, grace_seconds: 15}],
  }}, [{id: 1, url: 'https://app.example', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 1, pendingUrl: 'about:blank', windowId: 1});
  assert.deepEqual(browser.calls.removed, []);

  await browser.expireGrace();
  assert.deepEqual(browser.calls.removed, [2]);
});

test('blank popup grace uses the longest configured exemption grace', async () => {
  const browser = extension({tab_limits: {
    max_tabs: 1,
    hard_max_tabs: 3,
    per_pattern: [],
    popup_exemptions: [
      {pattern: 'https://login.example', max_tabs: 1, grace_seconds: 5},
      {pattern: 'https://identity.example', max_tabs: 1, grace_seconds: 15},
    ],
  }}, [{id: 1, url: 'https://app.example', windowId: 1}]);

  await browser.created({id: 2, openerTabId: 1, pendingUrl: 'about:blank', windowId: 1});
  assert.deepEqual(browser.calls.alarms.map(({name, options}) => [name, options.when]), [['aact-popup-2', 16_000]]);
});
