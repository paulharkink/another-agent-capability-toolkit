// Illustrative data for the public interaction mock. Nothing here reads local state.
export const capabilities = [
  { id: 'cluster-inspector', name: 'Cluster Inspector', kind: 'skill + MCP', mcp: true },
  { id: 'grafana-inspector', name: 'Grafana Inspector', kind: 'skill + MCP', mcp: true },
  { id: 'azure-inspector', name: 'Azure Inspector', kind: 'skill + MCP', mcp: true },
  { id: 'forgejo', name: 'Forgejo', kind: 'skill + MCP', mcp: true },
  { id: 'find-session', name: 'Find Session', kind: 'skill', mcp: false },
  { id: 'non-interactive-ready-planning', name: 'Non Interactive Ready Planning', kind: 'skill', mcp: false },
  { id: 'git-repo-map', name: 'Local Git Repository Map', kind: 'skill', mcp: false },
  { id: 'agent-skills-management', name: 'agent-skills-management', kind: 'skill', mcp: false },
  { id: 'clean-up', name: 'clean-up', kind: 'skill', mcp: false },
  { id: 'kinfer-platform', name: 'kinfer-platform', kind: 'skill', mcp: false },
  { id: 'kubedock-docker', name: 'kubedock-docker', kind: 'skill', mcp: false },
  { id: 'gitops-triage', name: 'pms15-gitops-triage', kind: 'skill', mcp: false },
  { id: 'live-cluster-diagnostics', name: 'pms15-live-cluster-diagnostics', kind: 'skill', mcp: false },
  { id: 'cluster-inspector-skill', name: 'cluster-inspector', kind: 'skill', mcp: false },
  { id: 'grafana-inspector-skill', name: 'grafana-inspector', kind: 'skill', mcp: false },
];

export const profiles = [
  {
    id: 'cluster-home-pms15', capabilityId: 'cluster-inspector', name: 'home / pms15',
    environment: 'home', target: 'pms15', runtime: 'Not observed', owner: 'Unknown',
    connection: 'Unreachable', endpoint: 'http://127.0.0.1:18766/mcp',
    observation: 'Sample: no matching container observed',
    connectionError: 'Sample: connect: connection refused',
    ownerEvidence: 'No local start record or matching ownership label in this sample',
  },
  {
    id: 'grafana-home', capabilityId: 'grafana-inspector', name: 'home / default',
    environment: 'home', target: 'default', runtime: 'Stopped', owner: 'This AACT',
    connection: 'Not checked', endpoint: 'http://127.0.0.1:18767/mcp',
    observation: 'Sample: stopped', connectionError: '', ownerEvidence: 'Local start record',
  },
];

export const agents = [
  { id: 'codex', name: 'Codex', status: 'Detected', config: '~/.codex/config.toml', effect: 'Install skill and add MCP registration' },
  { id: 'opencode', name: 'OpenCode', status: 'Detected', config: '~/.config/opencode/opencode.json', effect: 'Install skill and add MCP registration' },
  { id: 'claude', name: 'Claude Code', status: 'Detected', config: '~/.claude.json', effect: 'Install skill and add MCP registration' },
];

export const databases = [
  'homeassistant_postgres/homeassistant',
  'honcho_postgres/honcho',
  'shared_postgres/openwebui',
  'shared_postgres/plane',
];

export const setupSections = ['Connection', 'Authentication', 'Databases', 'Destinations'];
export function setupSectionsFor(state) {
  return selectedCapability(state).id === 'cluster-inspector' ? setupSections : ['Inputs', 'Destinations'];
}

export function nextOverlayControlIndex(groups, current, step) {
  const group = groups[current];
  if (!group) return current;
  const candidates = groups.flatMap((item, index) => item === group ? [index] : []);
  return candidates[Math.max(0, Math.min(candidates.length - 1, candidates.indexOf(current) + step))];
}
export const settingsSections = ['Default agents', 'Docker backend', 'Catalog and state'];
export const helpSections = ['Navigation', 'Status labels', 'Forms and values'];

export function createInitialState() {
  return {
    scope: { checkout: 'agent-skills', platform: 'macOS / arm64', source: 'pms15-agent-skills' },
    view: 'home', focus: 'capabilities', capabilityIndex: 0, profileIndex: 0,
    agentIndex: 0, environmentIndex: 0, targetIndex: 0, settingsIndex: 0, helpIndex: 0,
    menuIndex: 0, overlay: null, toast: 'Interactive design sample — no files or containers are changed.',
    marked: [],
    setup: {
      section: 'Authentication', authMode: 'token', token: '', kubeconfig: '',
      listenAddress: '127.0.0.1', listenPort: '18766',
      databases: [], destinations: ['codex'], dirty: false, kind: 'existing', error: '',
    },
    registrations: ['codex'],
    settings: { section: 'Default agents', defaultAgents: ['codex', 'opencode'], backend: 'Follow Docker CLI selection' },
  };
}

export function selectedCapability(state) {
  return capabilities[state.capabilityIndex] ?? capabilities[0];
}

export function visibleProfiles(state) {
  return profiles.filter(profile => profile.capabilityId === selectedCapability(state).id);
}

export function selectedProfile(state) {
  return visibleProfiles(state)[state.profileIndex] ?? null;
}

export function destinationsFor(state) {
  const skillOnly = !selectedCapability(state).mcp;
  const named = agents.map(agent => ({ ...agent, selected: state.setup.destinations.includes(agent.id) }));
  return skillOnly
    ? [{ id: 'all', name: 'All — ~/.agents/skills', status: 'Available', config: '~/.agents/skills', effect: 'Install this skill for all agents', selected: state.setup.destinations.includes('all') }, ...named]
    : named;
}

export function profileActions(state) {
  const profile = selectedProfile(state);
  if (!profile) return [];
  const unknown = profile.owner === 'Unknown';
  const owned = profile.owner === 'This AACT';
  const ownerReason = 'Runtime owner evidence is missing. View Details or Refresh observation.';
  return [
    { id: 'start', label: profile.runtime === 'Running' ? 'Restart…' : 'Start…', disabled: !owned, reason: unknown ? ownerReason : 'Runtime is controlled by its owner.' },
    { id: 'stop', label: 'Stop…', disabled: !owned || profile.runtime !== 'Running', reason: unknown ? ownerReason : 'Runtime is not running here.' },
    { id: 'auth', label: 'Authenticate…', disabled: !owned, reason: unknown ? ownerReason : 'Credentials belong to the owner.' },
    { id: 'parameters', label: 'Edit parameters…', disabled: !owned, reason: unknown ? ownerReason : 'Server parameters belong to the owner.' },
    { id: 'registrations', label: 'Configure local agent registrations…', disabled: !profile.endpoint, reason: 'No MCP endpoint is known.' },
    { id: 'removeRegistrations', label: 'Remove local registrations…', disabled: false, reason: '' },
    { id: 'check', label: 'Check connection', disabled: !profile.endpoint, reason: 'No MCP endpoint is known.' },
    { id: 'refresh', label: 'Refresh observation', disabled: false, reason: '' },
    { id: 'logs', label: 'View logs', disabled: !owned, reason: unknown ? ownerReason : 'Logs belong to the owner.' },
    { id: 'details', label: 'View details', disabled: false, reason: '' },
    { id: 'back', label: 'Back', disabled: false, reason: '' },
  ];
}

export function scrollCues(scrollTop, clientHeight, scrollHeight) {
  return { above: scrollTop > 1, below: scrollTop + clientHeight < scrollHeight - 1 };
}

function withSetup(state, changes) {
  return { ...state, setup: { ...state.setup, ...changes, dirty: true, error: '' } };
}

export function transition(state, action) {
  switch (action.type) {
    case 'selectCapability': {
      const capabilityIndex = Math.max(0, Math.min(capabilities.length - 1, action.index));
      const skillOnly = !capabilities[capabilityIndex].mcp;
      return {
        ...state, capabilityIndex, profileIndex: 0, focus: 'capabilities',
        setup: { ...state.setup, destinations: skillOnly ? ['all'] : ['codex'], dirty: false },
      };
    }
    case 'selectProfile':
      return { ...state, profileIndex: Math.max(0, Math.min(visibleProfiles(state).length - 1, action.index)), focus: 'profiles' };
    case 'focusPane':
      return { ...state, focus: action.pane === 'profiles' && visibleProfiles(state).length === 0 ? 'capabilities' : action.pane };
    case 'setView':
      return { ...state, view: action.view, overlay: null, focus: action.view === 'home' ? 'capabilities' : state.focus };
    case 'selectAgent':
      return { ...state, agentIndex: Math.max(0, Math.min(agents.length - 1, action.index)) };
    case 'selectEnvironment':
      return { ...state, environmentIndex: Math.max(0, Math.min(2, action.index)), targetIndex: 0 };
    case 'selectTarget':
      return { ...state, targetIndex: Math.max(0, action.index) };
    case 'selectSettingsSection':
      return { ...state, settingsIndex: Math.max(0, Math.min(settingsSections.length - 1, action.index)) };
    case 'selectHelpSection':
      return { ...state, helpIndex: Math.max(0, Math.min(helpSections.length - 1, action.index)) };
    case 'openMainMenu':
      return { ...state, overlay: { kind: 'main', layout: 'single' }, menuIndex: 0 };
    case 'openActions':
      return { ...state, overlay: { kind: 'actions', layout: 'single' }, menuIndex: 0 };
    case 'openRegistrations':
      return { ...state, overlay: { kind: 'registrations', layout: 'split', agentIndex: 0, remove: !!action.remove, draftRegistrations: [...state.registrations], removeSelected: [] } };
    case 'selectRegistrationAgent':
      return { ...state, overlay: { ...state.overlay, agentIndex: Math.max(0, Math.min(agents.length - 1, action.index)) } };
    case 'toggleRegistration': {
      const field = state.overlay.remove ? 'removeSelected' : 'draftRegistrations';
      if (state.overlay.remove && !state.registrations.includes(action.id)) return state;
      const registrations = new Set(state.overlay[field]);
      if (registrations.has(action.id)) registrations.delete(action.id);
      else registrations.add(action.id);
      return { ...state, overlay: { ...state.overlay, [field]: [...registrations] } };
    }
    case 'applyRegistrations': {
      const registrations = state.overlay.remove
        ? state.registrations.filter(id => !state.overlay.removeSelected.includes(id))
        : [...state.overlay.draftRegistrations];
      return {
        ...state,
        registrations,
        overlay: {
          kind: 'result', layout: 'single', status: 'success',
          message: state.overlay.remove
            ? `Local agent registrations selected for removal: ${state.overlay.removeSelected.join(', ') || 'none'}. No config file was changed (simulation).`
            : `Local agent registrations selected: ${registrations.join(', ') || 'none'}. No config file was changed (simulation).`,
        },
      };
    }
    case 'openSetup':
      {
      const section = selectedCapability(state).id === 'cluster-inspector' ? 'Authentication' : 'Inputs';
      return {
        ...state,
        setup: { ...state.setup, section, kind: action.kind ?? (selectedProfile(state) ? 'existing' : 'new'), dirty: false, error: '' },
        overlay: { kind: 'setup', layout: 'split', section },
      };
      }
    case 'selectSetupSection':
      return { ...state, setup: { ...state.setup, section: action.section }, overlay: { ...state.overlay, section: action.section } };
    case 'focusInput':
      return state;
    case 'editInput': {
      if (action.name === 'token' && action.value !== '') {
        return withSetup(state, { token: action.value, kubeconfig: '', authMode: 'token' });
      }
      if (action.name === 'kubeconfig' && action.value !== '') {
        return withSetup(state, { kubeconfig: action.value, token: '', authMode: 'kubeconfig' });
      }
      return withSetup(state, { [action.name]: action.value });
    }
    case 'toggleDatabase': {
      const current = new Set(state.setup.databases);
      if (current.has(action.id)) current.delete(action.id);
      else current.add(action.id);
      return withSetup(state, { databases: [...current] });
    }
    case 'toggleDestination': {
      if (action.id === 'all' && selectedCapability(state).mcp) return state;
      const current = new Set(state.setup.destinations);
      if (current.has(action.id)) current.delete(action.id);
      else current.add(action.id);
      return withSetup(state, { destinations: [...current] });
    }
    case 'saveSetup': {
      if (selectedCapability(state).id === 'cluster-inspector' && !state.setup.token.trim() && !state.setup.kubeconfig.trim()) {
        return {
          ...state,
          setup: { ...state.setup, section: 'Authentication', error: 'Enter a Token or Source kubeconfig.' },
          overlay: { kind: 'setup', layout: 'split', section: 'Authentication' },
        };
      }
      if (state.setup.destinations.length === 0) {
        return {
          ...state,
          setup: { ...state.setup, section: 'Destinations', error: selectedCapability(state).mcp ? 'Select at least one named agent destination.' : 'Select All or at least one named agent destination.' },
          overlay: { kind: 'setup', layout: 'split', section: 'Destinations' },
        };
      }
      const error = state.setup.listenPort === '9999';
      return {
        ...state,
        overlay: {
          kind: 'result', layout: 'single', status: error ? 'error' : 'success',
          message: error
            ? 'bind 127.0.0.1:9999: address already in use. Your edited answers remain in this mock. Edit the port and Save to retry.'
            : 'Answers saved and applied (simulation). No file, agent config, or container was changed.',
        },
        setup: { ...state.setup, dirty: false, error: '' },
      };
    }
    case 'editAnswers':
      return { ...state, overlay: { kind: 'setup', layout: 'split', section: state.setup.section } };
    case 'closeOverlay':
      return state.overlay?.kind === 'setup' && state.setup.dirty
        ? { ...state, overlay: { kind: 'discard', layout: 'single' } }
        : { ...state, overlay: null };
    case 'keepEditing':
      return { ...state, overlay: { kind: 'setup', layout: 'split', section: state.setup.section } };
    case 'discardChanges':
      return { ...state, overlay: null, setup: { ...createInitialState().setup, destinations: state.setup.destinations } };
    case 'showResult':
      return { ...state, overlay: { kind: 'result', layout: 'single', status: action.status ?? 'info', message: action.message } };
    case 'setToast':
      return { ...state, toast: action.message };
    case 'setSettingsBackend':
      return { ...state, settings: { ...state.settings, backend: action.value } };
    case 'toggleDefaultAgent': {
      const defaultAgents = new Set(state.settings.defaultAgents);
      if (defaultAgents.has(action.id)) defaultAgents.delete(action.id);
      else defaultAgents.add(action.id);
      return { ...state, settings: { ...state.settings, defaultAgents: [...defaultAgents] } };
    }
    case 'toggleMark': {
      const marks = new Set(state.marked);
      const id = selectedCapability(state).id;
      if (marks.has(id)) marks.delete(id);
      else marks.add(id);
      return { ...state, marked: [...marks] };
    }
    default:
      return state;
  }
}
