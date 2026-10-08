// Illustrative data for the public interaction mock. Nothing here reads local state.
export const capabilities = [
  { id: 'cluster-inspector', name: 'Cluster Inspector', kind: 'skill + MCP', mcp: true, installation: 'Partial', installationDetails: ['Skill: codex', 'MCP registration: no AACT record'] },
  { id: 'grafana-inspector', name: 'Grafana Inspector', kind: 'skill + MCP', mcp: true, installation: 'Installed', installationDetails: ['Skill: codex', 'MCP registration: codex'] },
  { id: 'azure-inspector', name: 'Azure Inspector', kind: 'skill + MCP', mcp: true, installation: 'Not installed' },
  { id: 'git-provider-inspector', name: 'Git Provider Inspector', kind: 'skill + MCP', mcp: true, installation: 'Not installed' },
  { id: 'find-session', name: 'Find Session', kind: 'skill', mcp: false, installation: 'Installed', installationDetails: ['Skill: all'] },
  { id: 'non-interactive-ready-planning', name: 'Non Interactive Ready Planning', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'git-repo-map', name: 'Local Git Repository Map', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'agent-skills-management', name: 'agent-skills-management', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'clean-up', name: 'clean-up', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'kinfer-platform', name: 'kinfer-platform', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'kubedock-docker', name: 'kubedock-docker', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'gitops-triage', name: 'target-a-gitops-triage', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'live-cluster-diagnostics', name: 'target-a-live-cluster-diagnostics', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'cluster-inspector-skill', name: 'cluster-inspector', kind: 'skill', mcp: false, installation: 'Not installed' },
  { id: 'grafana-inspector-skill', name: 'grafana-inspector', kind: 'skill', mcp: false, installation: 'Not installed' },
  {id:'guidance-bundle',name:'Guidance bundle',kind:'2 skills + optional plugin',mcp:false,installation:'Not installed'},
];

export const profiles = [
  {
    id: 'cluster-home-target-a', capabilityId: 'cluster-inspector', name: 'ota', configStatus: 'Configured', skills: '1/1', registered: '0/1',
    environment: "sample-env", target: 'target-a', runtime: 'Not observed', owner: 'Unknown',
    connection: 'Unreachable', endpoint: 'http://127.0.0.1:18766/mcp',
    observation: 'Sample: no matching container observed',
    connectionError: 'Sample: connect: connection refused',
    ownerEvidence: 'No local start record or matching ownership label in this sample',
  },
  {
    id: 'grafana-home', capabilityId: 'grafana-inspector', name: 'default', configStatus: 'Configured', skills: '1/1', registered: '1/1',
    environment: "sample-env", target: 'default', runtime: 'Stopped', owner: 'This AACT',
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

export const setupSections = ['Overview', 'Connection', 'Authentication', 'Databases', 'Components', 'Agents', 'Runtime', 'Information'];
export function setupSectionsFor(state) {
  return selectedCapability(state).id === 'cluster-inspector' ? setupSections : selectedCapability(state).mcp ? ['Overview', 'Inputs', 'Components', 'Agents', 'Runtime', 'Information'] : ['Overview','Inputs','Components','Agents','Information'];
}

export function nextOverlayControlIndex(groups, current, step) {
  const group = groups[current];
  if (!group) return current;
  const candidates = groups.flatMap((item, index) => item === group ? [index] : []);
  return candidates[Math.max(0, Math.min(candidates.length - 1, candidates.indexOf(current) + step))];
}

export function nextOverlayArea(areas, current, step) {
  if (areas.length === 0) return null;
  const index = areas.indexOf(current);
  return areas[(index + step + areas.length) % areas.length] ?? areas[0];
}

export function overlayKeyCommand({ key, ctrlKey = false, metaKey = false, area, editing = false, atTextStart = false, split = false }) {
  if ((ctrlKey || metaKey) && key.toLowerCase() === 's') return 'save';
  if (key === 'Escape') {
    if (split && area === 'right') return 'left';
    if (split && area === 'actions') return 'right';
    return 'cancel';
  }
  if (key === 'ArrowLeft' && area === 'right' && (!editing || atTextStart)) return 'left';
  if (key === 'ArrowRight' && area === 'left') return 'right';
  return null;
}
export const settingsSections = ['Default agents', 'Docker backend', 'Catalog and state'];
export const helpSections = ['Navigation', 'Status labels', 'Forms and values'];

export function createInitialState() {
  return {
    scope: { pack: 'example-company', directory: '/packs/example-company', platform: 'macOS / arm64', profiles: 'environments' },
 localProfiles: [], draftProfileName: '',
    view: 'home', focus: 'capabilities', capabilityIndex: 0, homeDetailIndex: 0,
    agentIndex: 0, environmentIndex: 0, targetIndex: 0, settingsIndex: 0, helpIndex: 0,
    menuIndex: 0, overlay: null, toast: 'Interactive design sample — no files or containers are changed.',
    setup: {
      section: 'Authentication', authMode: 'token', token: '', kubeconfig: '',
      listenAddress: '127.0.0.1', listenPort: '18766',
      items: ['set:capability'], enabled: false, databases: [], destinations: ['codex'], dirty: false, kind: 'existing', error: '',
    },
    registrations: ['codex'],
    settings: { section: 'Default agents', defaultAgents: ['codex', 'opencode'], backend: 'Follow Docker CLI selection' },
  };
}

export function selectedCapability(state) {
  return capabilities[state.capabilityIndex] ?? capabilities[0];
}

export function visibleProfiles(state) {
  return [...profiles,...(state.localProfiles ?? [])].filter(profile => profile.capabilityId === selectedCapability(state).id);
}

export function selectedProfile(state) {
  return visibleProfiles(state)[state.homeDetailIndex] ?? null;
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
        ...state, capabilityIndex, homeDetailIndex: 0, focus: 'capabilities',
        setup: { ...state.setup, items:componentItemsFor({...state,capabilityIndex}).filter(item=>item.skills.length).map(item=>item.id),destinations:skillOnly?['all']:['codex'],dirty:false },
      };
    }
    case 'selectHomeDetail':
      return { ...state, homeDetailIndex: Math.max(0, Math.min(1 + visibleProfiles(state).length, action.index)), focus: 'profiles' };
    case 'focusPane':
      return { ...state, focus: action.pane };
    case 'backHome':
      return { ...state, focus: 'capabilities' };
    case 'enterHome':
      if (state.focus === 'capabilities') return { ...state, focus: 'profiles' };
      if (selectedProfile(state)) return transition(state, {type:'openSetup',kind:'existing'});
      if (state.homeDetailIndex===visibleProfiles(state).length) return {...state,draftProfileName:'',overlay:{kind:'profile-create',layout:'single'}};
      return transition(state,{type:'openDetails'});
    case 'editProfileName': return {...state,draftProfileName:action.name};
    case 'createProfile': {
      const name=state.draftProfileName;
      if(!/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(name)||visibleProfiles(state).some(p=>p.name===name)) return {...state,toast:'Choose a unique, valid profile name.'};
      const profile={id:`local-${selectedCapability(state).id}-${name}`,capabilityId:selectedCapability(state).id,name,configStatus:'Needs input',skills:'0/1',registered:'0/1',runtime:'Stopped',owner:'This AACT',endpoint:'',connection:'Not checked',observation:'Local profile (simulation)'};
      return transition({...state,localProfiles:[...state.localProfiles,profile],homeDetailIndex:visibleProfiles(state).length,overlay:null},{type:'openSetup',kind:'new'});
    }
    case 'toggleComponent': {const items=new Set(state.setup.items);if(items.has(action.id))items.delete(action.id);else if(!componentItemsFor(state).find(i=>i.id===action.id)?.disabled)items.add(action.id);return withSetup(state,{items:[...items]});}
    case 'selectAllSkills': {const items=new Set(state.setup.items);for(const item of componentItemsFor(state))if(item.skills.length)items.add(item.id);return withSetup(state,{items:[...items]});}
    case 'toggleEnabled':return withSetup(state,{enabled:!state.setup.enabled});
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
    case 'openDetails':
      return { ...state, overlay: { kind: 'details', layout: 'single', parent: state.overlay } };
    case 'openRegistrations':
      return { ...state, overlay: { kind: 'registrations', layout: 'split', parent: state.overlay, itemIndex: 0, endpointCheck: 'Not checked in this sample', remove: !!action.remove, draftRegistrations: [...state.registrations], removeSelected: [] } };
    case 'selectRegistrationItem':
      return { ...state, overlay: { ...state.overlay, itemIndex: Math.max(0, Math.min(agents.length, action.index)) } };
    case 'checkRegistrationEndpoint':
      return { ...state, overlay: { ...state.overlay, endpointCheck: 'Unreachable · connect: connection refused (sample observation)' } };
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
          kind: 'result', layout: 'single', parent: state.overlay.parent, status: 'success',
          message: state.overlay.remove
            ? `Local agent registrations selected for removal: ${state.overlay.removeSelected.join(', ') || 'none'}. No config file was changed (simulation).`
            : `Local agent registrations selected: ${registrations.join(', ') || 'none'}. No config file was changed (simulation).`,
        },
      };
    }
    case 'openSetup':
      {
      const section = 'Overview';
      return {
        ...state,
        setup: { ...state.setup, section, kind: action.kind ?? (selectedProfile(state) ? 'existing' : 'new'), dirty: false, error: '' },
        overlay: { kind: 'setup', layout: 'split', parent: state.overlay, section },
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
          overlay: { ...state.overlay, section: 'Authentication' },
        };
      }
      if (state.setup.destinations.length === 0) {
        return {
          ...state,
          setup: { ...state.setup, section: 'Agents', error: selectedCapability(state).mcp ? 'Select at least one named agent destination.' : 'Select All or at least one named agent destination.' },
          overlay: { ...state.overlay, section: 'Agents' },
        };
      }
      const error = state.setup.listenPort === '9999';
      return {
        ...state,
        overlay: {
          kind: 'result', layout: 'single', parent: state.overlay.parent, status: error ? 'error' : 'success',
          message: error
            ? 'bind 127.0.0.1:9999: address already in use. Your edited answers remain in this mock. Edit the port and Save to retry.'
            : 'Answers saved and applied (simulation). No file, agent config, or container was changed.',
        },
        setup: { ...state.setup, dirty: false, error: '' },
      };
    }
    case 'editAnswers':
      return { ...state, overlay: { kind: 'setup', layout: 'split', parent: state.overlay.parent, section: state.setup.section } };
    case 'closeOverlay':
      return state.overlay?.kind === 'setup' && state.setup.dirty
        ? { ...state, overlay: { kind: 'discard', layout: 'single', parent: state.overlay } }
        : { ...state, overlay: state.overlay?.parent ?? null };
    case 'keepEditing':
      return { ...state, overlay: state.overlay.parent };
    case 'discardChanges':
      return { ...state, overlay: state.overlay.parent?.parent ?? null, setup: { ...createInitialState().setup, destinations: state.setup.destinations } };
    case 'showResult':
      return { ...state, overlay: { kind: 'result', layout: 'single', parent: state.overlay, status: action.status ?? 'info', message: action.message } };
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
    default:
      return state;
  }
}

export function componentItemsFor(state){const capability=selectedCapability(state);if(capability.id==='guidance-bundle')return [{id:'skill:guide',label:'Guide',skills:['guide'],mcps:[]},{id:'skill:review',label:'Review',skills:['review'],mcps:[]},{id:'plugin:native',label:'Claude native plugin',skills:[],mcps:[],disabled:!state.setup.destinations.includes('claude')}];return [{id:capability.mcp?'set:capability':'skill:capability',label:capability.name,skills:[capability.id],mcps:capability.mcp?[capability.id]:[]}];}
