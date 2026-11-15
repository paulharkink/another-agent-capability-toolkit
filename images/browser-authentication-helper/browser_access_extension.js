/* Generic best-effort tab limits. URL policy remains the navigation control. */
const alarmPrefix = "aact-popup-";
let config = {tab_limits: null};
let createdAt = new Map();
const configReady = fetch(chrome.runtime.getURL("access-config.json"))
  .then(response => response.json())
  .then(value => { config = value; })
  .catch(() => {});

function globRegex(pattern) {
  const match = /^(https?):\/\/([^/?#@]+)(\/[^?#@]*)?$/.exec(pattern);
  if (!match) return null;
  const [, scheme, authority, path = ""] = match;
  let host = authority;
  let expectedPort = null;
  if (authority.startsWith("[")) {
    const closing = authority.indexOf("]");
    if (closing < 0) return null;
    host = authority.slice(0, closing + 1);
    const suffix = authority.slice(closing + 1);
    if (suffix) expectedPort = suffix.slice(1);
  } else {
    const separator = authority.lastIndexOf(":");
    if (separator >= 0) {
      host = authority.slice(0, separator);
      expectedPort = authority.slice(separator + 1);
    }
  }
  const exactHost = host.startsWith(".");
  if (exactHost) host = host.slice(1);
  return {scheme, host: host.toLowerCase(), exactHost, expectedPort, path};
}

function matches(url, pattern) {
  if (typeof url !== "string") return false;
  const parsedPattern = globRegex(pattern);
  if (!parsedPattern) return false;
  let parsedURL;
  try { parsedURL = new URL(url); } catch { return false; }
  if (parsedURL.protocol.slice(0, -1).toLowerCase() !== parsedPattern.scheme) return false;
  const host = parsedURL.hostname.toLowerCase();
  const ipLiteral = parsedPattern.host.startsWith("[") || /^\d+\.\d+\.\d+\.\d+$/.test(parsedPattern.host);
  if (parsedPattern.host !== "*" && (parsedPattern.exactHost || ipLiteral
      ? host !== parsedPattern.host
      : host !== parsedPattern.host && !host.endsWith(`.${parsedPattern.host}`))) return false;
  if (parsedPattern.expectedPort && parsedPattern.expectedPort !== "*") {
    const actualPort = parsedURL.port || (parsedPattern.scheme === "https" ? "443" : "80");
    if (actualPort !== parsedPattern.expectedPort) return false;
  }
  return parsedURL.pathname.startsWith(parsedPattern.path || "/");
}

async function persistCreated(tabId, openerTabId) {
  const current = Date.now();
  createdAt.set(tabId, {created: current, opener: Number.isInteger(openerTabId)});
  await chrome.storage.session.set({[`aact-tab-${tabId}`]: {created: current, opener: Number.isInteger(openerTabId)}});
  if (!Number.isInteger(openerTabId)) return;
  const grace = Math.max(0, ...config.tab_limits.popup_exemptions.map(rule => rule.grace_seconds));
  chrome.alarms.create(`${alarmPrefix}${tabId}`, {when: current + grace * 1000});
}

async function getCreation(tab) {
  const cache = createdAt.get(tab.id);
  if (cache) return cache;
  const key = `aact-tab-${tab.id}`;
  const saved = (await chrome.storage.session.get(key))[key];
  if (saved) createdAt.set(tab.id, saved);
  return saved || null;
}

function isPopupExempt(tab, creation, exemptions) {
  if (!Number.isInteger(tab.openerTabId) || !creation?.opener) return false;
  const url = tab.pendingUrl || tab.url || "";
  const now = Date.now();
  return exemptions.some(rule => {
    if (matches(url, rule.pattern)) return true;
    return (url === "" || url === "about:blank") && now < creation.created + rule.grace_seconds * 1000;
  });
}

async function warnBlockedRulesInactive(enforced) {
  await chrome.action.setBadgeText({text: "!"});
  await chrome.action.setBadgeBackgroundColor({color: "#b3261e"});
  const title = enforced
    ? "Blocked URL policy is active in fail-closed mode; configured navigation rules could not be installed."
    : "Blocked URL policy could not be enforced; navigation may be unrestricted.";
  await chrome.action.setTitle({title});
}

async function installBlockedRules() {
  await configReady;
  if (!config.blocked_urls?.length) return;
  let fallbackBarrierInstalled = false;
  try {
    const currentRules = await chrome.declarativeNetRequest.getSessionRules();
    await chrome.declarativeNetRequest.updateSessionRules({
      removeRuleIds: currentRules.map(rule => rule.id),
      addRules: [{
        id: 900,
        priority: 1,
        action: {type: "block"},
        condition: {regexFilter: "^https?://", resourceTypes: ["main_frame", "sub_frame"]},
      }],
    });
    fallbackBarrierInstalled = true;
    const [enabled, ruleResponse] = await Promise.all([
      chrome.declarativeNetRequest.getEnabledRulesets(),
      fetch(chrome.runtime.getURL("navigation_rules.json")),
    ]);
    const rules = await ruleResponse.json();
    const checks = await Promise.all(rules.map(rule =>
      chrome.declarativeNetRequest.isRegexSupported({regex: rule.condition.regexFilter})
    ));
    if (!checks.every(check => check.isSupported)) {
      await warnBlockedRulesInactive(fallbackBarrierInstalled || enabled.includes("blocked-urls"));
      return;
    }
    const keepFallbackBarrier = !enabled.includes("blocked-urls");
    await chrome.declarativeNetRequest.updateSessionRules({
      removeRuleIds: keepFallbackBarrier ? [] : [900],
      addRules: rules,
    });
    fallbackBarrierInstalled = keepFallbackBarrier;
    const installed = await chrome.declarativeNetRequest.getSessionRules();
    const installedIds = new Set(installed.map(rule => rule.id));
    const expectedCount = rules.length + (keepFallbackBarrier ? 1 : 0);
    if (rules.some(rule => !installedIds.has(rule.id)) || installed.length !== expectedCount
      || (keepFallbackBarrier && !installedIds.has(900))) {
      await chrome.declarativeNetRequest.updateSessionRules({
        removeRuleIds: installed.map(rule => rule.id),
        addRules: [{
          id: 900,
          priority: 1,
          action: {type: "block"},
          condition: {regexFilter: "^https?://", resourceTypes: ["main_frame", "sub_frame"]},
        }],
      });
      fallbackBarrierInstalled = true;
      await warnBlockedRulesInactive(true);
    }
  } catch {
    // The static barrier or the session fallback remains active until exceptions install.
    let staticBarrierEnabled = false;
    try {
      staticBarrierEnabled = (await chrome.declarativeNetRequest.getEnabledRulesets()).includes("blocked-urls");
    } catch {
      // The warning below reports an unenforced policy when neither barrier can be confirmed.
    }
    await warnBlockedRulesInactive(fallbackBarrierInstalled || staticBarrierEnabled);
  }
}

async function checkLimits(candidateId = null) {
  const limits = config.tab_limits;
  if (!limits) return;
  while (true) {
    const tabs = await chrome.tabs.query({});
    const records = [];
    for (const tab of tabs) {
      const creation = await getCreation(tab);
      records.push({tab, creation, exempt: isPopupExempt(tab, creation, limits.popup_exemptions)});
    }

    let eligible = null;
    const created = records.filter(item => item.creation);
    if (records.length > limits.hard_max_tabs) {
      eligible = created;
    } else if (records.filter(item => !item.exempt).length > limits.max_tabs) {
      eligible = created.filter(item => !item.exempt);
    } else {
      for (const rule of limits.per_pattern) {
        const matching = records.filter(item => matches(item.tab.pendingUrl || item.tab.url, rule.pattern));
        if (matching.length > rule.max_tabs) {
          eligible = matching.filter(item => item.creation);
          break;
        }
      }
      if (!eligible) {
        for (const rule of limits.popup_exemptions) {
          const matching = records.filter(item => item.exempt
            && matches(item.tab.pendingUrl || item.tab.url, rule.pattern));
          if (matching.length > rule.max_tabs) {
            eligible = matching.filter(item => item.creation?.opener);
            break;
          }
        }
      }
    }
    if (!eligible) return;
    eligible.sort((left, right) => right.creation.created - left.creation.created
      || Number(right.tab.id === candidateId) - Number(left.tab.id === candidateId)
      || right.tab.id - left.tab.id);
    const candidate = eligible[0]?.tab;
    if (!candidate) return; // Existing tabs are never removed to repair a limit.
    try {
      await chrome.tabs.remove(candidate.id);
    } catch {
      return;
    }
    await chrome.action.setBadgeText({text: "!"});
    await chrome.action.setBadgeBackgroundColor({color: "#b3261e"});
    await chrome.action.setTitle({title: "A browser tab limit was reached; the newest eligible tab was closed."});
  }
}

chrome.runtime.onStartup.addListener(async () => {
  await configReady;
  await installBlockedRules();
});

chrome.runtime.onInstalled.addListener(async () => {
  await configReady;
  await installBlockedRules();
});

chrome.tabs.onCreated.addListener(async tab => {
  await configReady;
  if (!config.tab_limits) return;
  await persistCreated(tab.id, tab.openerTabId);
  setTimeout(() => checkLimits(tab.id).catch(() => {}), 250);
});

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  configReady.then(() => {
  if (!config.tab_limits || !(changeInfo.url || changeInfo.status === "complete")) return;
  setTimeout(() => checkLimits(tabId).catch(() => {}), 100);
  });
});

chrome.tabs.onRemoved.addListener(async tabId => {
  createdAt.delete(tabId);
  await chrome.storage.session.remove(`aact-tab-${tabId}`);
  chrome.alarms.clear(`${alarmPrefix}${tabId}`);
});

chrome.alarms.onAlarm.addListener(alarm => {
  if (alarm.name.startsWith(alarmPrefix)) {
    const tabId = Number(alarm.name.slice(alarmPrefix.length));
    configReady.then(() => checkLimits(Number.isInteger(tabId) ? tabId : null).catch(() => {}));
  }
});
