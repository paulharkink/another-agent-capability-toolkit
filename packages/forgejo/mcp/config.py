from dataclasses import dataclass
from pathlib import Path
import re, tomllib
@dataclass(frozen=True)
class ForgejoTarget:
    base_url: str
    local_port: int
    token_file: Path
    config_path: Path
    state_directory: Path

def load_target(path: Path, environment: str, target: str, state_home: Path | None = None) -> ForgejoTarget:
    for value in (environment, target):
        if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", value): raise ValueError("Invalid target identifier.")
    data=tomllib.loads(Path(path).read_text()); port=data.get("mcp",{}).get("local_port"); url=data.get("forgejo",{}).get("base_url")
    if not isinstance(port,int) or not 1024 <= port <= 65535: raise ValueError("[mcp].local_port must be between 1024 and 65535.")
    if not isinstance(url,str) or not re.fullmatch(r"https?://[^\s]+",url): raise ValueError("[forgejo].base_url must be an HTTP(S) URL.")
    state=Path(state_home or Path.home()/".local"/"state")/"agent-skills"/"forgejo"/environment/target
    return ForgejoTarget(url.rstrip("/"),port,state/"access-token",Path(path).resolve(),state)
