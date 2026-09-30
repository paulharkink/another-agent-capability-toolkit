import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from config import TektonProfile
from tekton_tools import register_tekton_tools


class ToolRegistry:
    def __init__(self):
        self.tools = {}

    def tool(self):
        def register(function):
            self.tools[function.__name__] = function
            return function
        return register


class TektonRegistrationTests(unittest.TestCase):
    def test_no_tekton_profile_registers_no_tools(self):
        mcp = ToolRegistry()
        register_tekton_tools(mcp, None, lambda args: "", lambda: None)
        self.assertEqual(mcp.tools, {})

    def test_configured_tekton_profile_registers_existing_tool_set(self):
        mcp = ToolRegistry()
        profile = TektonProfile(default_namespace="ci")
        register_tekton_tools(mcp, profile, lambda args: "", lambda: None)
        self.assertEqual(set(mcp.tools), {
            "branch_pipeline_runs", "main_pipeline_runs", "postbuild_pipeline_runs",
            "pipeline_taskruns", "taskrun_pod", "pod_status", "pod_logs",
            "search_named_resources",
        })


if __name__ == "__main__":
    unittest.main()
