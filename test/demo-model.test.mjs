import test from 'node:test';
import assert from 'node:assert/strict';
import {
  capabilities,
  createInitialState,
  destinationsFor,
  profileActions,
  scrollCues,
  selectedProfile,
  setupSectionsFor,
  nextOverlayControlIndex,
  nextOverlayArea,
  overlayKeyCommand,
  transition,
  visibleProfiles,
} from '../docs/demo/model.mjs';

test('Mac sample starts in the two-pane home view with only related MCP profiles', () => {
  const state = createInitialState();
  assert.equal(state.scope.platform, 'macOS / arm64');
  assert.equal(state.scope.checkout, 'agent-skills');
  assert.equal(state.focus, 'capabilities');
  assert.deepEqual(visibleProfiles(state).map(profile => profile.name), ['home / pms15']);

  const skillIndex = capabilities.findIndex(capability => capability.name === 'Find Session');
  const selected = transition(state, { type: 'selectCapability', index: skillIndex });
  assert.deepEqual(visibleProfiles(selected), []);
  assert.equal(selected.focus, 'capabilities');
  assert.equal(state.capabilityIndex, 0);
});

test('home Enter moves from layer 1 to layer 2 before opening deeper content', () => {
  let state = createInitialState();
  state = transition(state, { type: 'enterHome' });
  assert.equal(state.focus, 'profiles');
  assert.equal(state.homeDetailIndex, 0);
  assert.equal(state.overlay, null);
  assert.equal(selectedProfile(state), null);

  state = transition(state, { type: 'enterHome' });
  assert.equal(state.overlay.kind, 'setup');

  state = transition(createInitialState(), { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 1 });
  state = transition(state, { type: 'enterHome' });
  assert.equal(state.overlay.kind, 'details');

  state = transition(createInitialState(), { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 2 });
  assert.equal(selectedProfile(state).name, 'home / pms15');
  state = transition(state, { type: 'enterHome' });
  assert.equal(state.overlay.kind, 'actions');
});

test('Back from the home detail pane returns to the capability pane', () => {
  let state = transition(createInitialState(), { type: 'enterHome' });
  assert.equal(state.focus, 'profiles');
  state = transition(state, { type: 'backHome' });
  assert.equal(state.focus, 'capabilities');
  assert.equal(state.overlay, null);
});

test('skill-only capability still has layer 2 items to enter', () => {
  const skillIndex = capabilities.findIndex(capability => capability.name === 'Find Session');
  let state = transition(createInitialState(), { type: 'selectCapability', index: skillIndex });
  state = transition(state, { type: 'enterHome' });
  assert.equal(state.focus, 'profiles');
  assert.equal(state.overlay, null);
  state = transition(state, { type: 'enterHome' });
  assert.equal(state.overlay.kind, 'setup');
});

test('MCP destinations are named agents only, while skill-only setup offers All', () => {
  const state = createInitialState();
  assert.deepEqual(destinationsFor(state).map(destination => destination.id), ['codex', 'opencode', 'claude']);
  const skillIndex = capabilities.findIndex(capability => capability.name === 'Find Session');
  const skillState = transition(state, { type: 'selectCapability', index: skillIndex });
  assert.equal(destinationsFor(skillState)[0].id, 'all');
  assert.equal(destinationsFor(skillState)[0].selected, true);
  const withoutAll = transition(skillState, { type: 'toggleDestination', id: 'all' });
  assert.equal(destinationsFor(withoutAll)[0].selected, false);
});

test('skill-only setup presents its own inputs and destinations', () => {
  const skillIndex = capabilities.findIndex(capability => capability.name === 'Find Session');
  let state = transition(createInitialState(), { type: 'selectCapability', index: skillIndex });
  state = transition(state, { type: 'openSetup' });
  assert.deepEqual(setupSectionsFor(state), ['Inputs', 'Destinations']);
  assert.equal(state.setup.section, 'Inputs');
  state = transition(state, { type: 'saveSetup' });
  assert.equal(state.overlay.kind, 'result');
  assert.equal(state.overlay.status, 'success');
});

test('arrow navigation stays inside the current overlay pane', () => {
  const groups = ['left', 'left', 'left', 'right', 'right', 'footer'];
  assert.equal(nextOverlayControlIndex(groups, 1, 1), 2);
  assert.equal(nextOverlayControlIndex(groups, 2, 1), 2);
  assert.equal(nextOverlayControlIndex(groups, 3, -1), 3);
  assert.equal(nextOverlayControlIndex(groups, 4, -1), 3);
});

test('Tab moves through overlay sections, details, and action bar as three areas', () => {
  const areas = ['left', 'right', 'actions'];
  assert.equal(nextOverlayArea(areas, 'left', 1), 'right');
  assert.equal(nextOverlayArea(areas, 'right', 1), 'actions');
  assert.equal(nextOverlayArea(areas, 'actions', 1), 'left');
  assert.equal(nextOverlayArea(areas, 'actions', -1), 'right');
});

test('save shortcut and pane-return keys coexist with text editing', () => {
  assert.equal(overlayKeyCommand({ key: 's', ctrlKey: true, area: 'right' }), 'save');
  assert.equal(overlayKeyCommand({ key: 'S', metaKey: true, area: 'right', editing: true }), 'save');
  assert.equal(overlayKeyCommand({ key: 'ArrowLeft', area: 'right', editing: false }), 'left');
  assert.equal(overlayKeyCommand({ key: 'ArrowLeft', area: 'right', editing: true, atTextStart: true }), 'left');
  assert.equal(overlayKeyCommand({ key: 'ArrowLeft', area: 'right', editing: true, atTextStart: false }), null);
  assert.equal(overlayKeyCommand({ key: 'ArrowDown', area: 'right', atControlEnd: true }), null);
  assert.equal(overlayKeyCommand({ key: 'ArrowUp', area: 'actions' }), null);
  assert.equal(overlayKeyCommand({ key: 'Escape', area: 'right', split: true }), 'left');
  assert.equal(overlayKeyCommand({ key: 'Escape', area: 'actions', split: true }), 'right');
  assert.equal(overlayKeyCommand({ key: 'Escape', area: 'left', split: true }), 'cancel');
  assert.equal(overlayKeyCommand({ key: 'Escape', area: 'menu', split: false }), 'cancel');
});

test('authentication changes when an inactive credential receives input', () => {
  let state = createInitialState();
  state = transition(state, { type: 'openSetup' });
  assert.equal(state.overlay.kind, 'setup');
  assert.equal(state.overlay.layout, 'split');
  assert.equal(state.setup.authMode, 'token');

  state = transition(state, { type: 'editInput', name: 'token', value: 'temporary sample' });
  state = transition(state, { type: 'focusInput', name: 'kubeconfig' });
  assert.equal(state.setup.authMode, 'token');
  assert.equal(state.setup.token, 'temporary sample');

  state = transition(state, { type: 'editInput', name: 'kubeconfig', value: '/Users/demo/.kube/config' });
  assert.equal(state.setup.authMode, 'kubeconfig');
  assert.equal(state.setup.token, '');
  assert.equal(state.setup.kubeconfig, '/Users/demo/.kube/config');

  state = transition(state, { type: 'editInput', name: 'token', value: 'replacement sample' });
  assert.equal(state.setup.authMode, 'token');
  assert.equal(state.setup.kubeconfig, '');
});

test('database and destination sections remain in the setup right pane', () => {
  let state = transition(createInitialState(), { type: 'openSetup' });
  state = transition(state, { type: 'selectSetupSection', section: 'Databases' });
  assert.equal(state.overlay.kind, 'setup');
  assert.equal(state.overlay.layout, 'split');
  assert.equal(state.overlay.section, 'Databases');
  state = transition(state, { type: 'selectSetupSection', section: 'Destinations' });
  assert.equal(state.overlay.section, 'Destinations');
});

test('unknown ownership shows diagnosis actions without enabling runtime mutation', () => {
  let state = createInitialState();
  state = transition(state, { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 2 });
  const actions = profileActions(state);
  for (const id of ['details', 'refresh', 'check', 'registrations']) {
    assert.equal(actions.find(action => action.id === id)?.disabled, false, id);
  }
  for (const id of ['start', 'stop']) {
    const action = actions.find(item => item.id === id);
    assert.equal(action.disabled, true, id);
    assert.match(action.reason, /owner evidence/i);
  }
});

test('a failed simulated save keeps edited answers and offers return to the form', () => {
  let state = transition(createInitialState(), { type: 'openSetup' });
  state = transition(state, { type: 'editInput', name: 'token', value: 'temporary sample' });
  state = transition(state, { type: 'editInput', name: 'listenPort', value: '9999' });
  state = transition(state, { type: 'saveSetup' });
  assert.equal(state.overlay.kind, 'result');
  assert.equal(state.overlay.status, 'error');
  assert.match(state.overlay.message, /address already in use/);
  assert.equal(state.setup.listenPort, '9999');
  state = transition(state, { type: 'editAnswers' });
  assert.equal(state.overlay.kind, 'setup');
  assert.equal(state.setup.listenPort, '9999');
});

test('setup validates credentials and named destinations within the same two-pane form', () => {
  let state = transition(createInitialState(), { type: 'openSetup' });
  state = transition(state, { type: 'saveSetup' });
  assert.equal(state.overlay.kind, 'setup');
  assert.equal(state.overlay.section, 'Authentication');
  assert.match(state.setup.error, /Token or Source kubeconfig/);

  state = transition(state, { type: 'editInput', name: 'token', value: 'temporary sample' });
  state = transition(state, { type: 'toggleDestination', id: 'codex' });
  state = transition(state, { type: 'saveSetup' });
  assert.equal(state.overlay.kind, 'setup');
  assert.equal(state.overlay.section, 'Destinations');
  assert.match(state.setup.error, /named agent/);
});

test('scroll cues disclose content below and disappear at the end', () => {
  assert.deepEqual(scrollCues(0, 200, 500), { above: false, below: true });
  assert.deepEqual(scrollCues(150, 200, 500), { above: true, below: true });
  assert.deepEqual(scrollCues(300, 200, 500), { above: true, below: false });
});

test('registration dialog is split and changes only the local agent selection', () => {
  let state = transition(createInitialState(), { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 2 });
  state = transition(state, { type: 'openRegistrations' });
  assert.equal(state.overlay.kind, 'registrations');
  assert.equal(state.overlay.layout, 'split');
  assert.equal(state.overlay.itemIndex, 0);
  state = transition(state, { type: 'checkRegistrationEndpoint' });
  assert.equal(state.overlay.kind, 'registrations');
  assert.match(state.overlay.endpointCheck, /connection refused/);
  state = transition(state, { type: 'toggleRegistration', id: 'opencode' });
  assert.deepEqual(state.overlay.draftRegistrations, ['codex', 'opencode']);
  assert.deepEqual(state.registrations, ['codex']);
  state = transition(state, { type: 'selectRegistrationItem', index: 2 });
  assert.equal(state.overlay.itemIndex, 2);
  state = transition(state, { type: 'closeOverlay' });
  assert.deepEqual(state.registrations, ['codex']);
});

test('closing a deeper detail restores the selected profile action layer', () => {
  let state = transition(createInitialState(), { type: 'focusPane', pane: 'profiles' });
  state = transition(state, { type: 'selectHomeDetail', index: 2 });
  state = transition(state, { type: 'enterHome' });
  assert.equal(state.overlay.kind, 'actions');
  state = { ...state, menuIndex: 4 };
  state = transition(state, { type: 'openRegistrations' });
  assert.equal(state.overlay.kind, 'registrations');
  state = transition(state, { type: 'closeOverlay' });
  assert.equal(state.overlay.kind, 'actions');
  assert.equal(state.menuIndex, 4);
  assert.equal(selectedProfile(state).name, 'home / pms15');

  state = transition(state, { type: 'openDetails' });
  assert.equal(state.overlay.kind, 'details');
  state = transition(state, { type: 'closeOverlay' });
  assert.equal(state.overlay.kind, 'actions');
});

test('registration changes apply only after confirmation, and removals start unselected', () => {
  let state = transition(createInitialState(), { type: 'openRegistrations' });
  state = transition(state, { type: 'toggleRegistration', id: 'opencode' });
  state = transition(state, { type: 'applyRegistrations' });
  assert.deepEqual(state.registrations, ['codex', 'opencode']);
  assert.equal(state.overlay.kind, 'result');
  state = transition(state, { type: 'openRegistrations', remove: true });
  assert.deepEqual(state.overlay.removeSelected, []);
  assert.deepEqual(state.registrations, ['codex', 'opencode']);
  state = transition(state, { type: 'toggleRegistration', id: 'codex' });
  assert.deepEqual(state.overlay.removeSelected, ['codex']);
  state = transition(state, { type: 'applyRegistrations' });
  assert.deepEqual(state.registrations, ['opencode']);
});

test('settings choices change without changing existing registrations', () => {
  let state = createInitialState();
  state = transition(state, { type: 'setSettingsBackend', value: 'Explicit context: desktop-linux' });
  state = transition(state, { type: 'toggleDefaultAgent', id: 'claude' });
  assert.equal(state.settings.backend, 'Explicit context: desktop-linux');
  assert.deepEqual(state.settings.defaultAgents, ['codex', 'opencode', 'claude']);
  assert.deepEqual(state.registrations, ['codex']);
});
