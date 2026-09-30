import tempfile
import unittest
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from config import load_target

class ForgejoTargetTests(unittest.TestCase):
    def test_two_targets_resolve_distinct_endpoint_and_token_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); targets = []
            for name, port, url in (("one", 8123, "https://one.example.test"), ("two", 8124, "https://two.example.test")):
                path = root / f"{name}.toml"; path.write_text(f'[mcp]\nlocal_port={port}\n[forgejo]\nbase_url="{url}"\n')
                targets.append(load_target(path, "lab", name, root / "state"))
            self.assertEqual([t.base_url for t in targets], ["https://one.example.test", "https://two.example.test"])
            self.assertEqual([t.local_port for t in targets], [8123, 8124])
            self.assertNotEqual(targets[0].token_file, targets[1].token_file)
            targets[0].token_file.parent.mkdir(parents=True, exist_ok=True); targets[0].token_file.write_text("secret-one")
            self.assertFalse(targets[1].token_file.exists())
