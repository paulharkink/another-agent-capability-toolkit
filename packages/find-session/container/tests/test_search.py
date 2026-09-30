"""Exercise the shipped third-party search using synthetic history only."""
import json
from pathlib import Path
import subprocess
from coding_agent_tools.find_session import search_all_agents

def test_synthetic_codex_history_is_found_across_projects(tmp_path):
    codex=tmp_path/"codex"; history=codex/"sessions/2026/09/30";history.mkdir(parents=True)
    path=history/"rollout-2026-09-30T12-00-00-synthetic-session.jsonl"
    entries=[{"type":"session_meta","payload":{"id":"synthetic-session","cwd":"/fixture/project","git":{"branch":"feature/example"},"timestamp":"2026-09-30T12:00:00Z"}}, {"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"We discussed synthetic postgres migration evidence"}]}}]
    path.write_text("\n".join(json.dumps(item) for item in entries)+"\n")
    before=path.read_bytes()
    matches=search_all_agents(["postgres"],global_search=True,codex_home=str(codex),claude_home=str(tmp_path/"missing-claude"),opencode_home=str(tmp_path/"missing-opencode"))
    assert len(matches)==1
    assert matches[0]["session_id"]=="synthetic-session"
    assert matches[0]["cwd"]=="/fixture/project"
    assert path.read_bytes()==before

def test_missing_history_cli_exits_without_interactive_prompt(tmp_path):
    result=subprocess.run(["find-session","-g","absent-keyword","--claude-home",str(tmp_path/"missing-claude"),"--codex-home",str(tmp_path/"missing-codex"),"--opencode-home",str(tmp_path/"missing-opencode")],stdin=subprocess.DEVNULL,text=True,capture_output=True,timeout=10)
    assert result.returncode==0, result.stderr
    assert "No sessions found" in result.stdout+result.stderr
