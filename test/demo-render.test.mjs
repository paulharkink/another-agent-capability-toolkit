import test from 'node:test';
import assert from 'node:assert/strict';
import { createInitialState, transition } from '../docs/demo/model.mjs';
import { render } from '../docs/demo/app.mjs';

test('home renders the Mac scope and separates profile observations', () => {
  let state = transition(createInitialState(), { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 2 });
  const html = render(state);
  assert.match(html, /Checkout: agent-skills/);
  assert.match(html, /Managing: macOS \/ arm64/);
  assert.match(html, /data-pane="capabilities"/);
  assert.match(html, /data-pane="profiles"/);
  assert.match(html, /data-pane="profiles" tabindex="-1"/);
  assert.match(html, /Selected capability · Cluster Inspector/);
  assert.match(html, /data-select="homeDetail" data-index="0"/);
  assert.match(html, /Configure \/ install/);
  assert.match(html, /data-select="homeDetail" data-index="2"/);
  assert.match(html, /home \/ pms15/);
  assert.match(html, /Runtime: Not observed/);
  assert.match(html, /Owner: Unknown/);
  assert.match(html, /Connection: Unreachable/);
  assert.doesNotMatch(html, /Open WSL AACT|Open Windows AACT/);
});

test('setup overlay has its own left and right panes and direct credential fields', () => {
  const state = transition(createInitialState(), { type: 'openSetup' });
  const html = render(state);
  assert.match(html, /data-layer="3-4"/);
  assert.match(html, /data-area="actions"/);
  assert.match(html, /Ctrl\/Cmd\+S Install/);
  assert.match(html, /data-section="Authentication"/);
  assert.match(html, /data-section="Databases"/);
  assert.match(html, /data-section="Destinations"/);
  assert.match(html, /data-field="token"/);
  assert.match(html, /data-field="kubeconfig"/);
  assert.match(html, /Source kubeconfig/);
  assert.match(html, /inactive-credential/);
  assert.doesNotMatch(html, /<select[^>]*data-field="auth"/);
});

test('skill-only setup omits MCP sections and shows All as a destination', () => {
  const initial = createInitialState();
  const skillIndex = 4;
  let state = transition(initial, { type: 'selectCapability', index: skillIndex });
  state = transition(state, { type: 'openSetup' });
  const html = render(state);
  assert.match(html, /data-section="Inputs"/);
  assert.match(html, /data-section="Destinations"/);
  assert.doesNotMatch(html, /data-section="Authentication"/);
  assert.doesNotMatch(html, /data-section="Databases"/);
  assert.doesNotMatch(html, /data-field="token"/);
  state = transition(state, { type: 'selectSetupSection', section: 'Destinations' });
  assert.match(render(state), /All — ~\/\.agents\/skills/);
});

test('another MCP does not inherit Cluster Inspector inputs', () => {
  let state = transition(createInitialState(), { type: 'selectCapability', index: 1 });
  state = transition(state, { type: 'openSetup' });
  const html = render(state);
  assert.match(html, /Install · Grafana Inspector/);
  assert.match(html, /data-section="Inputs"/);
  assert.doesNotMatch(html, /data-section="Databases"/);
  assert.doesNotMatch(html, /data-field="kubeconfig"/);
});

test('database and destination sections render inline details without generic MCP destination', () => {
  let state = transition(createInitialState(), { type: 'openSetup' });
  state = transition(state, { type: 'selectSetupSection', section: 'Databases' });
  const databases = render(state);
  assert.match(databases, /homeassistant_postgres\/homeassistant/);
  assert.match(databases, /shared_postgres\/plane/);
  assert.doesNotMatch(databases, /data-layer="5"/);

  state = transition(state, { type: 'selectSetupSection', section: 'Destinations' });
  const destinations = render(state);
  assert.match(destinations, /~\/.codex\/config.toml/);
  assert.match(destinations, /~\/.config\/opencode\/opencode.json/);
  assert.doesNotMatch(destinations, /All — ~\/.agents\/skills/);
  assert.doesNotMatch(destinations, /data-toggle-destination="generic"/);
});

test('unknown-owner actions expose diagnosis and recovery paths', () => {
  let state = transition(createInitialState(), { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 2 });
  state = transition(state, { type: 'openActions' });
  const html = render(state);
  assert.match(html, /data-layer="3"/);
  assert.match(html, /Runtime owner evidence is missing/);
  assert.match(html, /Check connection/);
  assert.match(html, /Refresh observation/);
  assert.match(html, /Configure local agent registrations/);
  assert.match(html, /View details/);
});

test('registration detail belongs to the selected endpoint or agent', () => {
  let state = transition(createInitialState(), { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 2 });
  state = transition(state, { type: 'openRegistrations' });
  let html = render(state);
  assert.match(html, /data-select="registrationItem" data-index="0"/);
  assert.match(html, /Check connection/);
  assert.match(html, /Endpoint URI/);
  assert.doesNotMatch(html, /Register this endpoint for Codex/);

  state = transition(state, { type: 'selectRegistrationItem', index: 1 });
  html = render(state);
  assert.match(html, /Register this endpoint for Codex/);
  assert.match(html, /~\/\.codex\/config\.toml/);
  assert.doesNotMatch(html, /data-action="check"/);
});

test('result remains a blue-design popup with a route back to edited answers', () => {
  let state = transition(createInitialState(), { type: 'openSetup' });
  state = transition(state, { type: 'editInput', name: 'token', value: 'temporary sample' });
  state = transition(state, { type: 'editInput', name: 'listenPort', value: '9999' });
  state = transition(state, { type: 'saveSetup' });
  const html = render(state);
  assert.match(html, /data-layer="3"/);
  assert.match(html, /Operation result/);
  assert.match(html, /address already in use/);
  assert.match(html, /data-action="editAnswers"/);
});

test('validation error stays inside the split setup form', () => {
  let state = transition(createInitialState(), { type: 'openSetup' });
  state = transition(state, { type: 'saveSetup' });
  const html = render(state);
  assert.match(html, /data-layer="3-4"/);
  assert.match(html, /Enter a Token or Source kubeconfig/);
  assert.doesNotMatch(html, /Operation result/);
});
