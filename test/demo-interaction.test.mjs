import test from "node:test";
import assert from "node:assert/strict";
import { Window } from "happy-dom";
import { overlayKeyCommand } from "../docs/demo/model.mjs";

const appUrl = new URL("../docs/demo/app.mjs", import.meta.url);
const { mount } = await import(appUrl);
const windows = [];

function createDemo() {
  const window = new Window({ url: "http://localhost/" });
  windows.push(window);
  globalThis.window = window;
  globalThis.document = window.document;
  globalThis.CSS = window.CSS;
  const root = window.document.createElement("main");
  window.document.body.append(root);
  mount(root);
  const key = (target, name, options = {}) => {
    const accepted = target.dispatchEvent(new window.KeyboardEvent("keydown", {
      key: name, bubbles: true, cancelable: true, ...options,
    }));
    // Happy DOM does not supply the browser's default Enter activation for buttons.
    if (accepted && name === "Enter" && target.matches("button:not(:disabled)")) target.click();
    return accepted;
  };
  const click = target => target.dispatchEvent(new window.MouseEvent("click", { bubbles: true, cancelable: true }));
  return { window, root, key, click };
}

test.afterEach(async () => {
  for (const window of windows) {
    await window.happyDOM.abort();
    await window.happyDOM.close();
  }
  windows.length = 0;
});

test.after(() => {
  delete globalThis.window;
  delete globalThis.document;
  delete globalThis.CSS;
});

function selectProfileAndOpenActions(root, key) {
  key(root.querySelector('[data-select="capability"].selected'), "Enter");
  key(root.querySelector('[data-select="homeDetail"].selected'), "ArrowDown");
  key(root.querySelector('[data-select="homeDetail"].selected'), "ArrowDown");
  key(root.querySelector('[data-select="homeDetail"].selected'), "Enter");
}

test("Enter moves from layer 1 into layer 2, then opens the selected profile actions", async () => {
  const { root, key } = await createDemo();
  key(root.querySelector('[data-select="capability"].selected'), "Enter");
  assert.equal(root.querySelector(".base-panes .pane:last-child").classList.contains("focused"), true);
  assert.ok(root.querySelector('[data-select="homeDetail"].selected'));
  key(root.querySelector('[data-select="homeDetail"].selected'), "ArrowDown");
  key(root.querySelector('[data-select="homeDetail"].selected'), "ArrowDown");
  key(root.querySelector('[data-select="homeDetail"].selected'), "Enter");
  assert.equal(root.querySelector(".dialog").getAttribute("data-layer"), "3");
  assert.match(root.querySelector(".dialog-title").textContent, /home \/ target-a/);
});

test("Escape walks back from a contextual overlay through layer 2 to layer 1", () => {
  const { root, key } = createDemo();
  key(root.querySelector('[data-select="capability"].selected'), "Enter");
  key(root.querySelector('[data-select="homeDetail"].selected'), "ArrowDown");
  key(root.querySelector('[data-select="homeDetail"].selected'), "ArrowDown");
  key(root.querySelector('[data-select="homeDetail"].selected'), "Enter");
  assert.equal(root.querySelector(".dialog").getAttribute("data-layer"), "3");
  key(root, "Escape");
  assert.equal(root.querySelector(".dialog"), null);
  assert.ok(root.querySelector('[data-select="homeDetail"].selected'));
  key(root, "Escape");
  assert.ok(root.querySelector('[data-select="capability"].selected'));
});

test("keyboard opens registration and endpoint has an independent connection check", async () => {
  const { root, key } = await createDemo();
  selectProfileAndOpenActions(root, key);
  for (let attempts = 0; attempts < 10 && !root.querySelector('[data-action="registrations"]').classList.contains("active"); attempts++) {
    key(root.querySelector(".menu-item.active"), "ArrowDown");
  }
  const registrationAction = root.querySelector('[data-action="registrations"]');
  assert.equal(registrationAction.classList.contains("active"), true, "registration action is keyboard reachable");
  key(registrationAction, "Enter");
  assert.equal(root.querySelector(".dialog").getAttribute("data-layer"), "3-4");
  const rows = [...root.querySelectorAll('[data-select="registrationItem"]')];
  assert.match(rows[0].textContent, /Endpoint URI/);
  assert.match(root.querySelector('[data-pane="registration-detail"]').textContent, /Check connection/);
  assert.equal(root.querySelector('[data-toggle-registration="codex"]'), null);
  key(rows[0], "ArrowDown");
  assert.ok(root.querySelector('[data-toggle-registration="codex"]'));
  assert.equal(root.querySelector('[data-pane="registration-detail"]').textContent.includes("Check connection"), false);
});

test("setup supports Tab pane navigation", () => {
  const { root, key } = createDemo();
  key(root, "F4");
  key(root, "F4");
  assert.equal(root.querySelector(".dialog").getAttribute("data-layer"), "3-4");
  assert.match(root.querySelector(".dialog-title").textContent, /Install/);
  key(root.ownerDocument.activeElement, "Tab");
  assert.equal(root.ownerDocument.activeElement.closest(".overlay-panes .pane").dataset.pane, "setup-detail");
  key(root.ownerDocument.activeElement, "Tab");
  assert.ok(root.ownerDocument.activeElement.closest(".dialog-actions"));
  key(root, "Escape");
  assert.equal(root.querySelector(".dialog"), null);
});

test("skill-only L2 contains only skill actions and distinct noninteractive headings", () => {
  const { root, click } = createDemo();
  const skill = [...root.querySelectorAll('[data-select="capability"]')].find(row => row.textContent.includes('Find Session'));
  click(skill);
  const detail = root.querySelector('[data-pane="profiles"]');
  assert.equal(detail.textContent.includes('Related MCP profiles'), false);
  assert.equal(detail.textContent.includes('No MCP profiles'), false);
  assert.equal(detail.querySelectorAll('[data-select="homeDetail"]').length, 2);
  assert.ok(detail.querySelector('.list-heading'));
  assert.equal(detail.querySelector('.list-heading').matches('button'), false);
});

test("home shows installation status without capability checkboxes or batch action", () => {
  const { root, key } = createDemo();
  const home = root.querySelector('.base-panes');
  assert.match(home.textContent, /Installed|Partial|Not installed/);
  assert.doesNotMatch(home.textContent, /Apply marked|Marked capabilities|\[ \]|\[x\]/);
  assert.match(home.textContent, /AACT records/);
  const selected = root.querySelector('[data-select="capability"].selected');
  const before = selected.textContent;
  key(selected, ' ');
  assert.equal(root.querySelector('[data-select="capability"].selected').textContent, before);
});

test("setup Up and Down stay in the focused overlay pane", () => {
  const { root, key } = createDemo();
  key(root, "F4");
  key(root, "F4");
  const document = root.ownerDocument;
  const firstSection = root.querySelector('.section-row.selected');
  key(firstSection, "ArrowDown");
  assert.equal(document.activeElement.closest('.overlay-panes .pane').dataset.pane, 'setup-sections');
  const selectedSection = root.querySelector('.section-row.selected').textContent;
  key(document.activeElement, "ArrowRight");
  const right = root.querySelector('[data-pane="setup-detail"]');
  const controls = [...right.querySelectorAll('button:not(:disabled), input:not(:disabled), select:not(:disabled)')];
  const last = controls.at(-1);
  last.focus();
  key(last, "ArrowDown");
  assert.equal(document.activeElement.closest('.overlay-panes .pane').dataset.pane, 'setup-detail');
  assert.equal(root.querySelector('.section-row.selected').textContent, selectedSection);
  const first = right.querySelector('button:not(:disabled), input:not(:disabled), select:not(:disabled)');
  first.focus();
  key(first, "ArrowUp");
  assert.equal(document.activeElement.closest('.overlay-panes .pane').dataset.pane, 'setup-detail');
  assert.equal(root.querySelector('.section-row.selected').textContent, selectedSection);
});

test("Ctrl+S is mapped to save inside an overlay", () => {
  assert.equal(overlayKeyCommand({ key: "s", ctrlKey: true, area: "right", split: true }), "save");
});

test("Escape from setup detail returns to its section list before closing", () => {
  const { root, key } = createDemo();
  key(root, "F4");
  key(root, "F4");
  const section = root.querySelector(".section-row.selected");
  key(section, "ArrowRight");
  assert.equal(root.ownerDocument.activeElement.closest(".overlay-panes .pane").dataset.pane, "setup-detail");
  key(root.ownerDocument.activeElement, "Escape");
  assert.equal(root.ownerDocument.activeElement.closest(".overlay-panes .pane").dataset.pane, "setup-sections");
  assert.ok(root.querySelector(".dialog"));
  key(root.ownerDocument.activeElement, "Escape");
  assert.equal(root.querySelector(".dialog"), null);
});

test("Escape cancels setup", () => {
  const { root, key } = createDemo();
  key(root, "F4");
  key(root, "F4");
  assert.ok(root.querySelector(".dialog"));
  key(root, "Escape");
  assert.equal(root.querySelector(".dialog"), null);
});
