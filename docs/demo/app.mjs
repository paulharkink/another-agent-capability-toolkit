import {
  agents, capabilities, createInitialState, databases, destinationsFor, helpSections,
  nextOverlayControlIndex, profileActions, scrollCues, selectedCapability, selectedProfile,
  settingsSections, setupSectionsFor, transition, visibleProfiles,
} from './model.mjs';

const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, character => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[character]));

function button(label, action, primary = false, extra = '') {
  return `<button type="button" class="action-button${primary ? ' primary' : ''}" data-action="${action}" ${extra}>${label}</button>`;
}

function scrollFrame(content, name) {
  return `<div class="scroll-frame"><div class="scroll-region" data-scroll-region="${name}">${content}</div><div class="scroll-cue up" data-scroll-cue="${name}-up" hidden>↑ More above</div><div class="scroll-cue down" data-scroll-cue="${name}-down" hidden>↓ More below</div></div>`;
}

function pane(title, content, position, name, focused) {
  return `<section class="pane${focused ? ' focused' : ''}" data-pane="${name}" tabindex="-1"><div class="pane-title"><strong>${escapeHTML(title)}</strong><span>${focused ? '● focused' : ''}</span></div>${scrollFrame(content, name)}<div class="pane-bottom">${escapeHTML(position)}</div></section>`;
}

function listRow(label, meta, kind, index, selected, detail = '') {
  return `<button type="button" class="list-row${selected ? ' selected' : ''}" data-select="${kind}" data-index="${index}" title="${escapeHTML(detail || label)}"><span>${escapeHTML(label)}</span><small>${escapeHTML(meta)}</small></button>`;
}

function renderHome(state) {
  const capability = selectedCapability(state);
  const related = visibleProfiles(state);
  const profile = selectedProfile(state);
  const left = capabilities.map((item, index) => {
    const mark = state.marked.includes(item.id) ? '[x]' : '[ ]';
    return listRow(`${mark} ${item.name}`, item.kind, 'capability', index, index === state.capabilityIndex, item.name);
  }).join('');
  const right = related.length
    ? related.map((item, index) => listRow(item.name, `${item.runtime} · ${item.owner}`, 'profile', index, index === state.profileIndex, item.endpoint)).join('')
    : `<p class="empty-list">${capability.mcp ? 'No MCP profiles yet — configure/install to create one.' : 'No MCP profiles — skill-only capability.'}</p>`;
  const status = state.focus === 'profiles' && profile
    ? `<strong>${escapeHTML(profile.name)}</strong> · ${escapeHTML(profile.endpoint)}<br>Runtime: ${escapeHTML(profile.runtime)} · Owner: ${escapeHTML(profile.owner)} · Connection: ${escapeHTML(profile.connection)}<br><span class="subtle">${escapeHTML(profile.observation)} · ${escapeHTML(profile.connectionError || profile.ownerEvidence)}</span>`
    : `<strong>${escapeHTML(capability.name)}</strong> · ${escapeHTML(capability.kind)} · ${related.length} related MCP profile${related.length === 1 ? '' : 's'}<br><span class="subtle">${capability.mcp ? 'Select an MCP profile on the right, or configure this capability.' : 'Skill-only: install to All — ~/.agents/skills or a named agent.'}</span>`;
  return `<div class="panes base-panes">${pane('Capabilities', left, `${state.capabilityIndex + 1} / ${capabilities.length}`, 'capabilities', state.focus === 'capabilities')}${pane(`MCP profiles · ${capability.name}`, right, related.length ? `${state.profileIndex + 1} / ${related.length}` : '0 / 0', 'profiles', state.focus === 'profiles')}</div><div class="status-strip">${status}</div>`;
}

function renderAgents(state) {
  const agent = agents[state.agentIndex];
  const left = agents.map((item, index) => listRow(item.name, item.status, 'agent', index, index === state.agentIndex)).join('');
  const right = `<div class="detail-copy"><h3>${escapeHTML(agent.name)}</h3><dl><dt>Detection</dt><dd>${escapeHTML(agent.status)} · illustrative</dd><dt>Configuration</dt><dd>${escapeHTML(agent.config)}</dd><dt>AACT registrations</dt><dd>${state.registrations.includes(agent.id) ? 'Cluster Inspector' : 'None in this sample'}</dd></dl><p class="hint">The real TUI obtains detection and config locations from its compiled adapter.</p>${button('View configuration files', 'viewConfig')}${button('Configure location…', 'location')}</div>`;
  return `<div class="panes base-panes">${pane('Detected agents', left, `${state.agentIndex + 1} / ${agents.length}`, 'agents', true)}${pane(agent.name, right, 'Details', 'agent-details', false)}</div><div class="status-strip">Select an agent on the left. Its detection evidence and configuration appear on the right.</div>`;
}

function renderEnvironments(state) {
  const environments = ['home', 'No environment file'];
  const selected = environments[state.environmentIndex];
  const targets = selected === 'home'
    ? ['Cluster Inspector · pms15', 'Grafana Inspector · default']
    : ['Public package defaults · all inputs visible'];
  const left = environments.map((environment, index) => listRow(environment, index === 0 ? 'Configured targets' : 'Package defaults', 'environment', index, index === state.environmentIndex)).join('');
  const right = targets.map((target, index) => listRow(target, index === 0 && selected === 'home' ? 'Target TOML · fixed and default inputs' : 'Target', 'target', index, index === state.targetIndex)).join('') + `<div class="inline-actions">${button('Use for new setups', 'useEnvironment', true)}${button('View target', 'viewTarget')}${button('Environment root…', 'environmentRoot')}</div>`;
  return `<div class="panes base-panes">${pane('Environments', left, `${state.environmentIndex + 1} / ${environments.length}`, 'environments', true)}${pane(`Configured targets · ${selected}`, right, `${state.targetIndex + 1} / ${targets.length}`, 'targets', false)}</div><div class="status-strip">Root: agent-skills/environments · sample checkout data. Select a target to inspect its source.</div>`;
}

function renderSettings(state) {
  const left = settingsSections.map((section, index) => listRow(section, '', 'settings', index, index === state.settingsIndex)).join('');
  let right = '';
  if (state.settingsIndex === 0) {
    right = `<h3>Default named agents</h3><p class="hint">Used for future MCP installations. Existing registrations do not change.</p>${agents.map(agent => `<label class="check-row"><input type="checkbox" data-toggle-default="${agent.id}" ${state.settings.defaultAgents.includes(agent.id) ? 'checked' : ''}><span>${escapeHTML(agent.name)}</span></label>`).join('')}<p class="hint">Skill-only installations offer All — ~/.agents/skills separately.</p>`;
  } else if (state.settingsIndex === 1) {
    right = `<h3>Docker backend</h3><label class="field-label" for="backend-choice">Selection</label><select id="backend-choice" data-setting="backend"><option ${state.settings.backend === 'Follow Docker CLI selection' ? 'selected' : ''}>Follow Docker CLI selection</option><option ${state.settings.backend === 'Explicit context: desktop-linux' ? 'selected' : ''}>Explicit context: desktop-linux</option></select><p>Resolved backend: <strong>desktop-linux</strong> <span class="hint">(sample)</span></p>${button('Check backend', 'checkBackend')}`;
  } else {
    right = `<h3>Catalog and state</h3><dl><dt>Checkout</dt><dd>agent-skills</dd><dt>Source</dt><dd>pms15-agent-skills</dd><dt>State</dt><dd>~/.local/state/aact</dd><dt>Platform</dt><dd>macOS / arm64</dd><dt>Version</dt><dd>v0.1.2 sample</dd></dl>${button('View known checkouts…', 'knownCheckouts')}`;
  }
  return `<div class="panes base-panes">${pane('Settings', left, `${state.settingsIndex + 1} / ${settingsSections.length}`, 'settings', true)}${pane(settingsSections[state.settingsIndex], `<div class="detail-copy">${right}</div>`, 'Details', 'settings-details', false)}</div><div class="status-strip">Settings here are simulated. The real TUI must show its resolved backend and state.</div>`;
}

function renderHelp(state) {
  const left = helpSections.map((section, index) => listRow(section, '', 'help', index, index === state.helpIndex)).join('');
  const copy = [
    '<h3>Navigation</h3><p>Layer 1: overview on the left. Layer 2: details on the right. Open an item for a centered layer-3 dialog; when it needs its own details, layers 3 and 4 appear side by side above the base screen.</p><p>Tab or ←/→ switches panes. ↑/↓ selects within the focused pane. Enter or F2 opens Actions. Esc closes only the top layer.</p>',
    '<h3>Status labels</h3><p>Runtime observation, connection result, and owner evidence are separate facts. Unknown owner does not mean that a server is stopped. Refresh, Check connection, and Details help diagnose it.</p>',
    '<h3>Forms and values</h3><p>Environment fixed values are omitted from setup. Editable defaults show their origin. Token and Source kubeconfig are both focusable; entering one makes it active and clears the other. Save applies immediately.</p>',
  ][state.helpIndex];
  return `<div class="panes base-panes">${pane('Help topics', left, `${state.helpIndex + 1} / ${helpSections.length}`, 'help', true)}${pane(helpSections[state.helpIndex], `<div class="detail-copy">${copy}</div>`, 'Details', 'help-details', false)}</div><div class="status-strip">${button('Back to Home', 'back')}</div>`;
}

function renderBody(state) {
  switch (state.view) {
    case 'agents': return renderAgents(state);
    case 'environments': return renderEnvironments(state);
    case 'settings': return renderSettings(state);
    case 'help': return renderHelp(state);
    default: return renderHome(state);
  }
}

function menuItems(state) {
  if (state.overlay?.kind === 'main') return [
    { id: 'agents', label: 'Agents' }, { id: 'environments', label: 'Environments' },
    { id: 'settings', label: 'Settings' }, { id: 'help', label: 'Help' }, { id: 'closeOverlay', label: 'Back' },
  ];
  if (state.view === 'home' && state.focus === 'profiles') return profileActions(state);
  if (state.view === 'home') return [
    { id: 'parameters', label: `Configure / install ${selectedCapability(state).name}…` },
    { id: 'details', label: 'View capability details' },
    { id: 'mark', label: state.marked.includes(selectedCapability(state).id) ? 'Unmark for batch' : 'Mark for batch' },
    { id: 'batch', label: `Apply marked (${state.marked.length})…`, disabled: state.marked.length === 0, reason: 'Mark at least one capability first.' },
    { id: 'closeOverlay', label: 'Back' },
  ];
  if (state.view === 'agents') return [
    { id: 'viewConfig', label: 'View configuration files' }, { id: 'location', label: 'Configure location…' },
    { id: 'refresh', label: 'Refresh detection' }, { id: 'closeOverlay', label: 'Back' },
  ];
  if (state.view === 'environments') return [
    { id: 'useEnvironment', label: 'Use for new setups' }, { id: 'viewTarget', label: 'View target' },
    { id: 'environmentRoot', label: 'Environment root…' }, { id: 'closeOverlay', label: 'Back' },
  ];
  return [{ id: 'closeOverlay', label: 'Back' }];
}

function dialog(content, title, actions, layout = 'single') {
  const split = layout === 'split';
  return `<div class="overlay" role="presentation"><section class="dialog ${split ? 'split-dialog' : 'single-dialog'}" role="dialog" aria-modal="true" aria-label="${escapeHTML(title)}" data-layer="${split ? '3-4' : '3'}"><header class="dialog-title"><strong>${escapeHTML(title)}</strong><span class="sample-tag">SIMULATION</span></header>${content}<footer class="dialog-actions">${actions}</footer></section></div>`;
}

function renderMenu(state) {
  const items = menuItems(state);
  const title = state.overlay.kind === 'main' ? 'Main menu' : state.view === 'home'
    ? (state.focus === 'profiles' ? `${selectedCapability(state).name} · ${selectedProfile(state)?.name ?? ''}` : `${selectedCapability(state).name} · Actions`)
    : `${state.view[0].toUpperCase()}${state.view.slice(1)} · Actions`;
  const content = `<div class="dialog-content menu-content">${items.map((item, index) => `<button type="button" class="menu-item${index === state.menuIndex ? ' active' : ''}" data-action="${item.id}" data-menu-index="${index}" ${item.disabled ? 'disabled' : ''}><span>${escapeHTML(item.label)}</span>${item.reason ? `<small>${escapeHTML(item.reason)}</small>` : ''}</button>`).join('')}</div><div class="dialog-nav">↑↓ Choose · Enter Open · Esc Back</div>`;
  return dialog(content, title, button('Back', 'closeOverlay'));
}

function setupSectionButton(section, current) {
  return `<button type="button" class="section-row${current === section ? ' selected' : ''}" data-section="${section}"><span>${section}</span><small>${section === 'Databases' ? 'Optional read-only access' : section === 'Destinations' ? 'Agent installation and MCP config' : 'Inputs'}</small></button>`;
}

function setupRight(state) {
  const section = state.setup.section;
  if (section === 'Inputs') return `<h3>Inputs</h3><p class="hint">${selectedCapability(state).id === 'git-repo-map' ? 'This packaged skill can ask for one or more local repository directories. The Mac TUI adds them one at a time with a directory picker.' : selectedCapability(state).mcp ? 'This capability’s package-specific inputs are not represented in this interaction sample.' : 'This packaged skill has no required inputs in this sample.'}</p>`;
  if (section === 'Connection') return `<h3>Connection</h3><p class="hint">Environment: home / pms15. Fixed Kubernetes URL and CA values are supplied by its target TOML and omitted here.</p><label class="field-block"><span>Listen address <small>Environment default · editable</small></span><input data-field="listenAddress" value="${escapeHTML(state.setup.listenAddress)}"></label><label class="field-block"><span>Listen port <small>Saved override</small></span><input data-field="listenPort" inputmode="numeric" value="${escapeHTML(state.setup.listenPort)}"></label><p class="hint">The MCP endpoint shown to local agents uses this address and port.</p>`;
  if (section === 'Authentication') return `<h3>Authentication</h3><p class="hint">Both inputs stay available. Entering one activates it and clears the other. Source kubeconfig is imported into managed credentials.</p><label class="credential ${state.setup.authMode === 'token' ? 'active-credential' : 'inactive-credential'}"><span>Token <small>${state.setup.authMode === 'token' ? 'Active method' : 'Type here to switch to Token'}</small></span><input type="text" autocomplete="off" data-field="token" value="${escapeHTML(state.setup.token)}" placeholder="Enter token"></label><label class="credential ${state.setup.authMode === 'kubeconfig' ? 'active-credential' : 'inactive-credential'}"><span>Source kubeconfig <small>${state.setup.authMode === 'kubeconfig' ? 'Active method · imported, not a live path' : 'Type a path to switch to kubeconfig'}</small></span><input type="text" autocomplete="off" data-field="kubeconfig" value="${escapeHTML(state.setup.kubeconfig)}" placeholder="Choose or enter a file path"></label><p class="hint">The real Mac TUI also offers a file picker. This browser sample accepts a path for interaction testing.</p>`;
  if (section === 'Databases') return `<h3>Read-only database queries</h3><p class="hint">Optional. The choices below are shown directly in this detail pane; there is no database submenu.</p>${databases.map(database => `<label class="check-row"><input type="checkbox" data-toggle-database="${escapeHTML(database)}" ${state.setup.databases.includes(database) ? 'checked' : ''}><span>${escapeHTML(database)}</span></label>`).join('')}<p class="hint">Each selected database grants only the packaged read-only queries.</p>`;
  return `<h3>Destinations</h3><p class="hint">${selectedCapability(state).mcp ? 'An MCP capability requires at least one named agent. There is no All or generic destination.' : 'All installs this skill into ~/.agents/skills; named agents can be selected too.'}</p>${destinationsFor(state).map(destination => `<label class="destination-row"><input type="checkbox" data-toggle-destination="${destination.id}" ${destination.selected ? 'checked' : ''}><span><strong>${escapeHTML(destination.name)}</strong><small>${escapeHTML(destination.status)} · ${escapeHTML(destination.config)}</small><small>${escapeHTML(destination.effect)}</small></span></label>`).join('')}`;
}

function renderSetup(state) {
  const capability = selectedCapability(state);
  const sectionsForCapability = setupSectionsFor(state);
  const sections = sectionsForCapability.map(section => setupSectionButton(section, state.setup.section)).join('');
  const left = pane('Setup sections', sections, `${sectionsForCapability.indexOf(state.setup.section) + 1} / ${sectionsForCapability.length}`, 'setup-sections', true);
  const right = pane(`${capability.name} · ${state.setup.section}`, `<div class="detail-copy">${setupRight(state)}</div>`, 'Details', 'setup-detail', false);
  const scope = capability.id === 'cluster-inspector' ? 'Environment: home · Target: pms15' : capability.mcp ? 'Environment: home · Target: default' : 'Source: pms15-agent-skills · Package defaults';
  const content = `<div class="dialog-scope">${scope} · ${state.setup.kind === 'new' ? 'New setup' : 'Existing profile'} · Sample data</div>${state.setup.error ? `<div class="form-error" role="alert">${escapeHTML(state.setup.error)}</div>` : ''}<div class="panes overlay-panes">${left}${right}</div><div class="dialog-nav">Tab/←→ Panes · ↑↓ Controls · Space Toggle · Enter Activate · Esc Back</div>`;
  return dialog(content, `${state.setup.kind === 'new' ? 'Install' : 'Parameters'} · ${capability.name}`, button('Cancel', 'closeOverlay') + button(state.setup.kind === 'new' ? 'Install' : 'Save', 'saveSetup', true), 'split');
}

function renderRegistrations(state) {
  const index = state.overlay.agentIndex ?? 0;
  const agent = agents[index];
  const removing = state.overlay.remove;
  const marked = removing ? state.overlay.removeSelected : state.overlay.draftRegistrations;
  const left = pane(removing ? 'Registrations to remove' : 'Named agents', agents.map((item, i) => listRow(`${marked.includes(item.id) ? '[x]' : '[ ]'} ${item.name}`, item.status, 'registrationAgent', i, i === index)).join(''), `${index + 1} / ${agents.length}`, 'registration-agents', true);
  const rowLabel = removing ? `Remove this registration from ${agent.name}` : `Register this endpoint for ${agent.name}`;
  const notRegistered = removing && !state.registrations.includes(agent.id);
  const right = pane(agent.name, `<div class="detail-copy"><h3>${escapeHTML(agent.name)}</h3><p>Endpoint: <code>${escapeHTML(selectedProfile(state)?.endpoint ?? 'http://127.0.0.1:18766/mcp')}</code></p>${button('Check connection', 'check')}<dl><dt>Agent detection</dt><dd>${escapeHTML(agent.status)} · sample</dd><dt>Config file</dt><dd>${escapeHTML(agent.config)}</dd><dt>Planned effect</dt><dd>${escapeHTML(agent.effect)}</dd></dl><label class="check-row"><input type="checkbox" data-toggle-registration="${agent.id}" ${marked.includes(agent.id) ? 'checked' : ''} ${notRegistered ? 'disabled' : ''}><span>${escapeHTML(rowLabel)}${notRegistered ? ' · no registration exists' : ''}</span></label></div>`, 'Details', 'registration-detail', false);
  const scope = removing ? 'Select existing registrations to remove' : 'Desired final local registrations';
  return dialog(`<div class="dialog-scope">${scope} · Endpoint: ${escapeHTML(selectedProfile(state)?.endpoint ?? 'http://127.0.0.1:18766/mcp')}</div><div class="panes overlay-panes">${left}${right}</div><div class="dialog-nav">Tab/←→ Panes · ↑↓ Agents · Space Toggle · Esc Back</div>`, removing ? 'Remove local registrations' : 'Configure agent registrations', button('Cancel', 'closeOverlay') + button('Apply changes', 'applyRegistrations', true), 'split');
}

function renderDetails(state) {
  const profile = state.view === 'home' && state.focus === 'profiles' ? selectedProfile(state) : null;
  let content;
  if (profile) content = `<dl><dt>Source / package</dt><dd>pms15-agent-skills / ${escapeHTML(selectedCapability(state).name)}</dd><dt>Environment / target</dt><dd>${escapeHTML(profile.environment)} / ${escapeHTML(profile.target)}</dd><dt>Endpoint</dt><dd>${escapeHTML(profile.endpoint)}</dd><dt>Runtime observation</dt><dd>${escapeHTML(profile.runtime)} · ${escapeHTML(profile.observation)}</dd><dt>Owner evidence</dt><dd>${escapeHTML(profile.owner)} · ${escapeHTML(profile.ownerEvidence)}</dd><dt>Connection</dt><dd>${escapeHTML(profile.connection)} · ${escapeHTML(profile.connectionError)}</dd><dt>Local registrations</dt><dd>${escapeHTML(state.registrations.join(', ') || 'None')}</dd></dl><p class="hint">Unknown owner does not mean stopped. Refresh or check the endpoint, then register it with a local agent if appropriate.</p>`;
  else if (state.view === 'home') content = `<p><strong>${escapeHTML(selectedCapability(state).name)}</strong> · ${escapeHTML(selectedCapability(state).kind)}</p><p>Source: pms15-agent-skills · checkout: agent-skills</p><p>Inputs and destinations appear together in its Install / Parameters screen.</p>`;
  else content = `<p>Selected ${escapeHTML(state.view)} item. This viewer is read-only in the sample.</p>`;
  return dialog(scrollFrame(`<div class="detail-copy">${content}</div>`, 'details'), profile ? `Details · ${profile.name}` : 'Capability details', button('Back', 'closeOverlay'));
}

function renderResult(state) {
  const error = state.overlay.status === 'error';
  const content = `<div class="dialog-content"><p class="result-${error ? 'error' : 'success'}">${escapeHTML(state.overlay.message)}</p><p class="hint">Simulation only. No local file, agent configuration, credential, or container was changed.</p></div>`;
  return dialog(content, 'Operation result', (error ? button('Edit answers', 'editAnswers') : '') + button('Back', 'closeOverlay', !error));
}

function renderOverlay(state) {
  if (!state.overlay) return '';
  switch (state.overlay.kind) {
    case 'main': case 'actions': return renderMenu(state);
    case 'setup': return renderSetup(state);
    case 'registrations': return renderRegistrations(state);
    case 'details': return renderDetails(state);
    case 'result': return renderResult(state);
    case 'discard': return dialog('<div class="dialog-content"><p>Discard unsaved answers in this setup form?</p></div>', 'Discard changes?', button('Keep editing', 'keepEditing') + button('Discard', 'discardChanges', true));
    default: return '';
  }
}

export function render(state) {
  const footer = `<footer class="terminal-footer"><button data-action="help"><b>F1</b> Help</button><button data-action="actions"><b>F2</b> Actions</button><button data-action="details"><b>F3</b> Details</button><button data-action="parameters"><b>F4</b> Parameters</button><button data-action="refresh"><b>F5</b> Refresh</button><button data-action="main"><b>F9</b> Main menu</button><button data-action="back"><b>${state.view === 'home' ? 'F10' : 'Esc'}</b> ${state.view === 'home' ? 'Quit' : 'Back'}</button></footer>`;
  return `<div class="terminal"><div class="shell" ${state.overlay ? 'inert' : ''}><header class="terminal-title"><strong>AACT · Another Agent Capability Toolkit</strong><span>INTERACTIVE DESIGN SAMPLE</span></header><div class="menubar"><button data-action="main"><b>F9</b> Main menu: Agents | Environments | Settings | Help</button><button data-action="actions"><b>F2</b> Actions for selected item</button></div><div class="scope">Checkout: ${escapeHTML(state.scope.checkout)} · Managing: ${escapeHTML(state.scope.platform)} · Source: ${escapeHTML(state.scope.source)} · Env: home</div><div class="terminal-body">${renderBody(state)}</div><div class="toast" role="status">${escapeHTML(state.toast)}</div>${footer}</div>${renderOverlay(state)}</div>`;
}

function updateScrollCues(root) {
  root.querySelectorAll('[data-scroll-region]').forEach(region => {
    const name = region.dataset.scrollRegion;
    const cues = scrollCues(region.scrollTop, region.clientHeight, region.scrollHeight);
    const up = root.querySelector(`[data-scroll-cue="${name}-up"]`);
    const down = root.querySelector(`[data-scroll-cue="${name}-down"]`);
    if (up) up.hidden = !cues.above;
    if (down) down.hidden = !cues.below;
  });
}

export function mount(root) {
  let state = createInitialState();
  const defaultFocusSelector = () => {
    if (state.overlay?.kind === 'setup') return '.section-row.selected';
    if (state.overlay?.kind === 'registrations') return '.overlay-panes .list-row.selected';
    if (state.overlay?.kind === 'actions' || state.overlay?.kind === 'main') return '.menu-item:not(:disabled)';
    if (state.overlay) return '.dialog-actions button';
    if (state.view === 'home') return state.focus === 'profiles' ? '[data-select="profile"].selected' : '[data-select="capability"].selected';
    return `[data-select="${{ agents: 'agent', environments: 'environment', settings: 'settings', help: 'help' }[state.view] ?? 'capability'}"].selected`;
  };
  const paint = focusSelector => {
    const scrollPositions = new Map([...root.querySelectorAll('[data-scroll-region]')].map(region => [region.dataset.scrollRegion, region.scrollTop]));
    root.innerHTML = render(state);
    root.querySelectorAll('[data-scroll-region]').forEach(region => { region.scrollTop = scrollPositions.get(region.dataset.scrollRegion) ?? 0; });
    updateScrollCues(root);
    const focus = root.querySelector(focusSelector ?? defaultFocusSelector()) ?? root.querySelector(defaultFocusSelector());
    if (focus) focus.focus({ preventScroll: true });
  };
  const send = (action, focusSelector) => { state = transition(state, action); paint(focusSelector); };
  const show = (message, status = 'info') => send({ type: 'showResult', message, status }, '.dialog-actions button');
  const perform = action => {
    switch (action) {
      case 'main': send({ type: 'openMainMenu' }, '.menu-item:not(:disabled)'); break;
      case 'actions': send({ type: 'openActions' }, '.menu-item:not(:disabled)'); break;
      case 'parameters': send({ type: 'openSetup' }, '.section-row.selected'); break;
      case 'auth': send({ type: 'openSetup' }, '.section-row.selected'); break;
      case 'details': state = { ...state, overlay: { kind: 'details', layout: 'single' } }; paint('.dialog-actions button'); break;
      case 'registrations': send({ type: 'openRegistrations' }, '.list-row.selected'); break;
      case 'removeRegistrations': send({ type: 'openRegistrations', remove: true }, '.list-row.selected'); break;
      case 'saveSetup': send({ type: 'saveSetup' }, '.dialog-actions button'); break;
      case 'editAnswers': send({ type: 'editAnswers' }, '.section-row.selected'); break;
      case 'closeOverlay': send({ type: 'closeOverlay' }); break;
      case 'keepEditing': send({ type: 'keepEditing' }, '.section-row.selected'); break;
      case 'discardChanges': send({ type: 'discardChanges' }); break;
      case 'back':
        if (state.overlay) send({ type: 'closeOverlay' });
        else if (state.view !== 'home') send({ type: 'setView', view: 'home' });
        else send({ type: 'setToast', message: 'The real TUI exits with F10. Close this browser tab when finished.' });
        break;
      case 'agents': case 'environments': case 'settings': case 'help': send({ type: 'setView', view: action }); break;
      case 'mark': send({ type: 'toggleMark' }); break;
      case 'batch': show(`${state.marked.length} marked capabilities would be configured. This mock does not change the machine.`); break;
      case 'check': show(`${selectedProfile(state)?.endpoint ?? 'Sample endpoint'}: unreachable · connect: connection refused (sample observation)`, 'error'); break;
      case 'refresh': show('Observation refreshed in the simulation. Runtime: not observed; owner: unknown; endpoint connection: unreachable. Check Details for the evidence.'); break;
      case 'start': case 'stop': show(`${action === 'start' ? 'Start' : 'Stop'} simulated. No container was changed.`); break;
      case 'logs': show('No locally owned container logs are available in this sample.'); break;
      case 'applyRegistrations': send({ type: 'applyRegistrations' }, '.dialog-actions button'); break;
      case 'viewConfig': show(`${agents[state.agentIndex].config}: example viewer only; no local config file is read.`); break;
      case 'location': show('Configure location would open a form for this agent adapter. This sample does not change paths.'); break;
      case 'useEnvironment': show('Environment selected for new setups (simulation).'); break;
      case 'viewTarget': show('home / pms15 target TOML: sample fixed Kubernetes API and CA; editable listen address and port.'); break;
      case 'environmentRoot': show('Environment root: agent-skills/environments (sample).'); break;
      case 'checkBackend': show('Docker context desktop-linux is reachable (simulation).'); break;
      case 'knownCheckouts': show('Known checkout: agent-skills (sample).'); break;
      default: break;
    }
  };

  root.addEventListener('click', event => {
    const section = event.target.closest('[data-section]');
    if (section) { send({ type: 'selectSetupSection', section: section.dataset.section }, `[data-section="${section.dataset.section}"]`); return; }
    const row = event.target.closest('[data-select]');
    if (row) {
      const index = Number(row.dataset.index);
      const type = {
        capability: 'selectCapability', profile: 'selectProfile', agent: 'selectAgent',
        environment: 'selectEnvironment', target: 'selectTarget', settings: 'selectSettingsSection',
        help: 'selectHelpSection', registrationAgent: 'selectRegistrationAgent',
      }[row.dataset.select];
      if (type) send({ type, index }, `[data-select="${row.dataset.select}"][data-index="${index}"]`);
      return;
    }
    const action = event.target.closest('[data-action]');
    if (action && !action.disabled) perform(action.dataset.action);
  });

  root.addEventListener('input', event => {
    const field = event.target.dataset.field;
    if (!field) return;
    const start = event.target.selectionStart;
    send({ type: 'editInput', name: field, value: event.target.value }, `[data-field="${field}"]`);
    const input = root.querySelector(`[data-field="${field}"]`);
    if (input && start != null) input.setSelectionRange(start, start);
  });

  root.addEventListener('change', event => {
    const input = event.target;
    if (input.dataset.toggleDatabase) send({ type: 'toggleDatabase', id: input.dataset.toggleDatabase }, `[data-toggle-database="${CSS.escape(input.dataset.toggleDatabase)}"]`);
    else if (input.dataset.toggleDestination) send({ type: 'toggleDestination', id: input.dataset.toggleDestination }, `[data-toggle-destination="${input.dataset.toggleDestination}"]`);
    else if (input.dataset.toggleRegistration) send({ type: 'toggleRegistration', id: input.dataset.toggleRegistration }, `[data-toggle-registration="${input.dataset.toggleRegistration}"]`);
    else if (input.dataset.toggleDefault) send({ type: 'toggleDefaultAgent', id: input.dataset.toggleDefault }, `[data-toggle-default="${input.dataset.toggleDefault}"]`);
    else if (input.dataset.setting === 'backend') send({ type: 'setSettingsBackend', value: input.value }, '[data-setting="backend"]');
  });

  root.addEventListener('scroll', () => updateScrollCues(root), true);

  root.addEventListener('keydown', event => {
    const key = event.key;
    if (key === 'Escape') { event.preventDefault(); perform('back'); return; }
    const editing = event.target.matches('input[type="text"], input:not([type]), select, textarea');
    if (state.overlay) {
      const dialogElement = root.querySelector('.dialog');
      const controls = [...dialogElement.querySelectorAll('button:not(:disabled), input:not(:disabled), select:not(:disabled)')];
      const current = controls.indexOf(event.target);
      if (key === 'Tab' && controls.length) {
        event.preventDefault();
        controls[(current + (event.shiftKey ? -1 : 1) + controls.length) % controls.length].focus();
        return;
      }
      if ((key === 'ArrowDown' || key === 'ArrowUp') && controls.length) {
        event.preventDefault();
        const groups = controls.map(control => control.closest('.pane')?.dataset.pane ?? (control.closest('.menu-content') ? 'menu' : 'footer'));
        const index = nextOverlayControlIndex(groups, current, key === 'ArrowDown' ? 1 : -1);
        const target = controls[index];
        if (target?.dataset.section) send({ type: 'selectSetupSection', section: target.dataset.section }, `[data-section="${target.dataset.section}"]`);
        else if (target?.dataset.select === 'registrationAgent') send({ type: 'selectRegistrationAgent', index: Number(target.dataset.index) }, `[data-select="registrationAgent"][data-index="${target.dataset.index}"]`);
        else if (target) target.focus();
        return;
      }
      if (!editing && (key === 'ArrowLeft' || key === 'ArrowRight') && state.overlay.layout === 'split') {
        const paneSelector = key === 'ArrowRight' ? '.overlay-panes .pane:last-child' : '.overlay-panes .pane:first-child';
        const target = dialogElement.querySelector(`${paneSelector} button, ${paneSelector} input, ${paneSelector} select`) ?? dialogElement.querySelector(paneSelector);
        if (target) { event.preventDefault(); target.focus(); }
        return;
      }
      if ((state.overlay.kind === 'main' || state.overlay.kind === 'actions') && key === 'Enter' && event.target === dialogElement) {
        event.preventDefault(); controls.find(control => !control.disabled)?.click();
      }
      return;
    }
    if (editing) return;
    const commands = { F1: 'help', F2: 'actions', F3: 'details', F4: 'parameters', F5: 'refresh', F9: 'main', F10: 'back' };
    if (commands[key]) { event.preventDefault(); perform(commands[key]); return; }
    if (key === 'Enter' && event.target.matches('.list-row')) { event.preventDefault(); perform('actions'); return; }
    if (key === ' ' && state.view === 'home' && state.focus === 'capabilities') { event.preventDefault(); perform('mark'); return; }
    const basePane = event.target.closest('.base-panes .pane');
    if ((basePane || event.target === root) && (key === 'Tab' || key === 'ArrowLeft' || key === 'ArrowRight')) {
      event.preventDefault();
      const panes = [...root.querySelectorAll('.base-panes .pane')];
      const right = key === 'ArrowRight' || (key === 'Tab' && basePane === panes[0]);
      const targetPane = panes[right ? 1 : 0];
      if (state.view === 'home') {
        const pane = right ? 'profiles' : 'capabilities';
        send({ type: 'focusPane', pane }, pane === 'profiles' ? '[data-select="profile"].selected' : '[data-select="capability"].selected');
      } else {
        (targetPane.querySelector('button:not(:disabled), input:not(:disabled), select:not(:disabled)') ?? targetPane).focus();
      }
      return;
    }
    if (key === 'ArrowDown' || key === 'ArrowUp') {
      const step = key === 'ArrowDown' ? 1 : -1;
      event.preventDefault();
      if (basePane && basePane === root.querySelector('.base-panes .pane:last-child') && state.view !== 'home') {
        const controls = [...basePane.querySelectorAll('button:not(:disabled), input:not(:disabled), select:not(:disabled)')];
        const target = controls[Math.max(0, Math.min(controls.length - 1, controls.indexOf(event.target) + step))];
        if (target?.dataset.select === 'target') send({ type: 'selectTarget', index: Number(target.dataset.index) }, `[data-select="target"][data-index="${target.dataset.index}"]`);
        else target?.focus();
        return;
      }
      if (state.view === 'home' && state.focus === 'profiles') send({ type: 'selectProfile', index: state.profileIndex + step }, '[data-select="profile"].selected');
      else if (state.view === 'home') send({ type: 'selectCapability', index: state.capabilityIndex + step }, '[data-select="capability"].selected');
      else if (state.view === 'agents') send({ type: 'selectAgent', index: state.agentIndex + step }, '[data-select="agent"].selected');
      else if (state.view === 'environments') send({ type: 'selectEnvironment', index: state.environmentIndex + step }, '[data-select="environment"].selected');
      else if (state.view === 'settings') send({ type: 'selectSettingsSection', index: state.settingsIndex + step }, '[data-select="settings"].selected');
      else if (state.view === 'help') send({ type: 'selectHelpSection', index: state.helpIndex + step }, '[data-select="help"].selected');
    }
  });
  root.addEventListener('focusin', event => {
    const pane = event.target.closest('.pane');
    if (pane) {
      [...pane.parentElement.children].filter(item => item.classList.contains('pane')).forEach(item => {
        const focused = item === pane;
        item.classList.toggle('focused', focused);
        item.querySelector('.pane-title span').textContent = focused ? '● focused' : '';
      });
    }
    if (event.target.matches('.menu-item')) {
      state = { ...state, menuIndex: Number(event.target.dataset.menuIndex) };
      root.querySelectorAll('.menu-item').forEach(item => item.classList.toggle('active', item === event.target));
    }
  });
  paint();
}

if (typeof document !== 'undefined') {
  const root = document.getElementById('aact-demo');
  if (root) mount(root);
}
