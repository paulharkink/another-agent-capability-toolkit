from pathlib import Path
import tomllib


ROOT = Path(__file__).resolve().parents[2]


def test_skill_uses_shared_auth_and_per_mutation_approval():
    manifest = tomllib.loads((ROOT / "package.toml").read_text())
    templates = manifest.get("templates", [])
    assert {template["destination"] for template in templates} >= {"SKILL.md"}

    template = ROOT / "SKILL.md.mustache"
    assert template.is_file(), "argocd package must provide its skill template"
    body = template.read_text()
    for required in (
        "authenticate",
        "login_url",
        "health_path",
        "authenticated",
        "read_only",
        "hard refresh",
        "approval",
        "aact mcp authenticate <capability> --profile <name>",
        "aact mcp status --json",
        "`instances` array",
        "`key.source`",
        "`key.package`",
        "`key.profile`",
        "`key.mcp`",
        "`authenticated: false`",
        "argocd_app_get",
        "positionals",
        '"refresh": true',
        '"hard_refresh": true',
        'arguments: {"positionals": ["my-app"], "refresh": true}',
        'arguments: {"positionals": ["my-app"], "hard_refresh": true}',
        "approval for this specific hard refresh",
    ):
        assert required in body, f"skill is missing the {required!r} workflow guidance"
    assert "Do not invent separate authentication status or cancellation actions" in body
    assert "it is a read and remains allowed in read-only mode" in body
    assert "is a mutation; the server hides or rejects it" in body
