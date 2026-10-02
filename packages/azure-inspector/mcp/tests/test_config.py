import tempfile
import unittest
import sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from config import load_target

class AzureTargetTests(unittest.TestCase):
    def test_two_targets_resolve_distinct_subscription_port_and_cli_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); targets = []
            for name, port, tenant, sub in (("one", 8123, "tenant-one", "sub-one"), ("two", 8124, "tenant-two", "sub-two")):
                path = root / f"{name}.toml"; path.write_text(f'[mcp]\nlocal_port={port}\n[azure]\ntenant_id="{tenant}"\nsubscription_id="{sub}"\n')
                targets.append(load_target(path, "lab", name, root / "state"))
            self.assertEqual([t.tenant_id for t in targets], ["tenant-one", "tenant-two"])
            self.assertEqual([t.subscription_id for t in targets], ["sub-one", "sub-two"])
            self.assertEqual([t.local_port for t in targets], [8123, 8124])
            self.assertNotEqual(targets[0].cli_state, targets[1].cli_state)
            (targets[0].cli_state / "token-cache").parent.mkdir(parents=True, exist_ok=True)
            (targets[0].cli_state / "token-cache").write_text("secret-one")
            self.assertFalse((targets[1].cli_state / "token-cache").exists())

    def test_unsupported_resource_hints_rejected_before_runtime(self):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/"target.toml"
            path.write_text('[mcp]\nlocal_port=8084\n[azure]\ntenant_id="tenant-example"\nsubscription_id="subscription-example"\n[azure.resource_hints]\nresource="unsupported"\n')
            with self.assertRaisesRegex(ValueError, "resource-hints"):
                load_target(path,"lab","sample",Path(directory)/"state")
