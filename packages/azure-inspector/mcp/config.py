from dataclasses import dataclass
from pathlib import Path
import re, tomllib
@dataclass(frozen=True)
class AzureTarget:
    tenant_id: str
    subscription_id: str
    local_port: int
    resource_hints: dict[str,str]
    cli_state: Path
    config_path: Path

def load_target(path: Path, environment: str, target: str, state_home: Path | None = None) -> AzureTarget:
    for value in (environment, target):
        if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", value): raise ValueError("Invalid target identifier.")
    data=tomllib.loads(Path(path).read_text()); port=data.get("mcp",{}).get("local_port"); cfg=data.get("azure",{})
    if not isinstance(port,int) or not 1024 <= port <= 65535: raise ValueError("[mcp].local_port must be between 1024 and 65535.")
    tenant, sub=cfg.get("tenant_id"),cfg.get("subscription_id")
    if not isinstance(tenant,str) or not tenant.strip() or not isinstance(sub,str) or not sub.strip(): raise ValueError("Azure tenant_id and subscription_id are required.")
    hints=cfg.get("resource_hints",{})
    if not isinstance(hints,dict) or any(not isinstance(k,str) or not isinstance(v,str) for k,v in hints.items()): raise ValueError("[azure.resource_hints] must contain string values.")
    if hints: raise ValueError("This Azure MCP image does not expose a supported resource-hints interface; remove [azure.resource_hints].")
    state=Path(state_home or Path.home()/".local"/"state")/"agent-skills"/"azure-inspector"/environment/target
    return AzureTarget(tenant,sub,port,hints,state,Path(path).resolve())
