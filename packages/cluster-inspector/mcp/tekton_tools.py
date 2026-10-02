"""Read-only Tekton tools registered on a configured Cluster Inspector."""
from __future__ import annotations

import json
import os
import re
import subprocess
from typing import Any, Callable, Literal

from config import TektonProfile


def register_tekton_tools(mcp, profile: TektonProfile | None, kubectl: Callable[[list[str]], str], require_session: Callable[[], dict[str, str] | None]) -> None:
    """Register Tekton diagnostics only for profiles that opt into the capability."""
    if profile is None:
        return

    def error(message: str) -> dict[str, str]:
        return {"error": message}

    def namespace(value: str | None) -> str:
        selected = value or profile.default_namespace
        if not re.fullmatch(r"[a-z0-9]([-a-z0-9]*[a-z0-9])?", selected) or len(selected) > 63:
            raise ValueError("Namespace must be a DNS-compatible Kubernetes name.")
        return selected

    def resource_name(value: str, description: str) -> str:
        if not re.fullmatch(r"[a-z0-9]([-a-z0-9.]*[a-z0-9])?", value) or len(value) > 253:
            raise ValueError(f"{description} must be a DNS-compatible Kubernetes resource name.")
        return value

    def project_fragment(value: str) -> str:
        if not re.fullmatch(r"[A-Za-z0-9.-]+", value):
            raise ValueError("Project fragment must contain only letters, digits, dots, or hyphens.")
        return value

    def normalized_branch(value: str) -> str:
        normalized = re.sub(r"[^A-Za-z0-9]+", "-", value).strip("-").lower()
        if not normalized:
            raise ValueError("Branch must contain at least one letter or digit.")
        return normalized

    def json_resource(resource: str, name: str | None, selected_namespace: str) -> dict[str, Any]:
        arguments = ["get", resource]
        if name:
            arguments.append(name)
        arguments.extend(["--namespace", selected_namespace, "--output", "json"])
        return json.loads(kubectl(arguments))

    def repository_overrides(repository_path: str | None) -> dict[str, str]:
        if not repository_path or not profile.repository_context_hook:
            return {}
        hook = profile.repository_context_hook
        try:
            result = subprocess.run([hook, repository_path], check=True, capture_output=True, text=True, timeout=10)
        except (OSError, subprocess.SubprocessError) as exc:
            raise ValueError("Configured repository context hook failed.") from exc
        values: dict[str, str] = {}
        for line in result.stdout.splitlines():
            key, separator, value = line.partition("=")
            if separator and key in {"repository", "branch", "project_fragment", "namespace"}:
                values[key] = value
        return values

    def condition(resource: dict[str, Any], condition_type: str) -> dict[str, Any]:
        return next((item for item in resource.get("status", {}).get("conditions", []) if item.get("type") == condition_type), {})

    def pipeline_summary(run: dict[str, Any]) -> dict[str, Any]:
        succeeded = condition(run, "Succeeded")
        return {"name": run.get("metadata", {}).get("name"), "succeeded": succeeded.get("status"), "reason": succeeded.get("reason"), "startTime": run.get("status", {}).get("startTime"), "completionTime": run.get("status", {}).get("completionTime")}

    def taskrun_summary(taskrun: dict[str, Any]) -> dict[str, Any]:
        succeeded = condition(taskrun, "Succeeded")
        return {"name": taskrun.get("metadata", {}).get("name"), "succeeded": succeeded.get("status"), "reason": succeeded.get("reason"), "startTime": taskrun.get("status", {}).get("startTime"), "completionTime": taskrun.get("status", {}).get("completionTime"), "podName": taskrun.get("status", {}).get("podName")}

    def pipeline_runs_for_selector(selector: str, selected_namespace: str, fragment: str) -> list[dict[str, Any]]:
        runs = json.loads(kubectl(["get", "pipelineruns.tekton.dev", "--namespace", selected_namespace, "--selector", selector, "--output", "json"])).get("items", [])
        return [pipeline_summary(run) for run in runs if fragment in run.get("metadata", {}).get("name", "") or (profile.repository_selector_key and fragment == run.get("metadata", {}).get("labels", {}).get(profile.repository_selector_key, ""))]

    def branch_selector(branch: str) -> str:
        return f"{profile.branch_selector_key or 'tekton.dev/branch'}={normalized_branch(branch)}"

    @mcp.tool()
    def branch_pipeline_runs(branch: str, project_fragment: str, namespace: str | None = None, repository_path: str | None = None) -> dict[str, Any]:
        """List PipelineRun candidates for a caller-supplied branch and project fragment."""
        if err := require_session(): return err
        try:
            context = repository_overrides(repository_path)
            selected_branch = context.get("branch", branch)
            normalized = normalized_branch(selected_branch)
            fragment = project_fragment_fn(context.get("project_fragment", project_fragment))
            selected_namespace = namespace_fn(context.get("namespace", namespace))
            return {"branch": selected_branch, "normalizedBranch": normalized, "projectFragment": fragment, "pipelineRuns": pipeline_runs_for_selector(branch_selector(selected_branch), selected_namespace, fragment)}
        except (ValueError, json.JSONDecodeError) as exc:
            return error(str(exc))

    # Avoid shadowing the validator with the public tool parameter names.
    namespace_fn = namespace

    @mcp.tool()
    def main_pipeline_runs(project_fragment: str, namespace: str | None = None, repository_path: str | None = None) -> dict[str, Any]:
        """List main-branch PipelineRun candidates for a caller-supplied project fragment."""
        if err := require_session(): return err
        try:
            context = repository_overrides(repository_path)
            fragment = project_fragment_fn(context.get("project_fragment", project_fragment))
            return {"projectFragment": fragment, "pipelineRuns": pipeline_runs_for_selector(branch_selector("main"), namespace_fn(context.get("namespace", namespace)), fragment)}
        except (ValueError, json.JSONDecodeError) as exc:
            return error(str(exc))

    project_fragment_fn = project_fragment

    @mcp.tool()
    def postbuild_pipeline_runs(project_fragment: str, namespace: str | None = None, repository_path: str | None = None) -> dict[str, Any]:
        """List post-build PipelineRuns for a project fragment, without branch filtering."""
        if err := require_session(): return err
        try:
            context = repository_overrides(repository_path)
            fragment = project_fragment_fn(context.get("project_fragment", project_fragment))
            runs = json_resource("pipelineruns.tekton.dev", None, namespace_fn(context.get("namespace", namespace))).get("items", [])
            return {"projectFragment": fragment, "pipelineRuns": [pipeline_summary(run) for run in runs if "post-build" in run.get("metadata", {}).get("name", "").lower() and fragment in run.get("metadata", {}).get("name", "")]}
        except (ValueError, json.JSONDecodeError) as exc:
            return error(str(exc))

    @mcp.tool()
    def pipeline_taskruns(pipeline_run: str, namespace: str | None = None) -> dict[str, Any]:
        """List TaskRuns linked to one exact PipelineRun using Tekton's pipelineRun label."""
        if err := require_session(): return err
        try:
            selected = resource_name(pipeline_run, "PipelineRun")
            selected_namespace = namespace_fn(namespace)
            runs = json.loads(kubectl(["get", "taskruns.tekton.dev", "--namespace", selected_namespace, "--selector", f"tekton.dev/pipelineRun={selected}", "--output", "json"])).get("items", [])
            return {"pipelineRun": selected, "taskRuns": [taskrun_summary(run) for run in runs]}
        except (ValueError, json.JSONDecodeError) as exc:
            return error(str(exc))

    @mcp.tool()
    def taskrun_pod(task_run: str, namespace: str | None = None) -> dict[str, Any]:
        """Resolve a pod only from the exact TaskRun's authoritative status.podName."""
        if err := require_session(): return err
        try:
            selected = resource_name(task_run, "TaskRun")
            taskrun = json_resource("taskruns.tekton.dev", selected, namespace_fn(namespace))
            pod_name = taskrun.get("status", {}).get("podName")
            if not pod_name: return error(f"TaskRun {selected} has no status.podName yet.")
            return {"taskRun": selected, "podName": pod_name}
        except (ValueError, json.JSONDecodeError) as exc:
            return error(str(exc))

    @mcp.tool()
    def pod_status(pod: str, namespace: str | None = None) -> dict[str, Any]:
        """Return compact status for one exact pod, including its containers."""
        if err := require_session(): return err
        try:
            resource = json_resource("pods", resource_name(pod, "Pod"), namespace_fn(namespace))
            statuses = resource.get("status", {}).get("containerStatuses", [])
            return {"name": resource.get("metadata", {}).get("name"), "phase": resource.get("status", {}).get("phase"), "reason": resource.get("status", {}).get("reason"), "containers": [{"name": item.get("name"), "ready": item.get("ready"), "restartCount": item.get("restartCount"), "state": item.get("state")} for item in statuses]}
        except (ValueError, json.JSONDecodeError) as exc:
            return error(str(exc))

    @mcp.tool()
    def pod_logs(pod: str, container: str, tail_lines: int = 200, namespace: str | None = None) -> dict[str, Any]:
        """Return a bounded log tail for one exact pod container."""
        if err := require_session(): return err
        try:
            selected = resource_name(pod, "Pod")
            if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", container): raise ValueError("Container name contains unsupported characters.")
            if not 1 <= tail_lines <= 2_000: raise ValueError("tail_lines must be between 1 and 2000.")
            logs = kubectl(["logs", "--namespace", namespace_fn(namespace), selected, "--container", container, "--tail", str(tail_lines)])
            return {"pod": selected, "container": container, "tailLines": tail_lines, "logs": logs}
        except (ValueError, json.JSONDecodeError) as exc:
            return error(str(exc))

    @mcp.tool()
    def search_named_resources(resource: Literal["pipelinerun", "taskrun", "pod"], terms: list[str], namespace: str | None = None) -> dict[str, Any]:
        """Find resources with all literal name fragments; use only for unusual pipeline conventions."""
        if err := require_session(): return err
        try:
            if not terms or len(terms) > 5 or any(not re.fullmatch(r"[A-Za-z0-9.-]+", term) for term in terms):
                raise ValueError("terms must contain one to five literal name fragments using letters, digits, dots, or hyphens.")
            plural = {"pipelinerun": "pipelineruns.tekton.dev", "taskrun": "taskruns.tekton.dev", "pod": "pods"}[resource]
            items = json_resource(plural, None, namespace_fn(namespace)).get("items", [])
            matches = [item for item in items if all(term.lower() in item.get("metadata", {}).get("name", "").lower() for term in terms)]
            if resource == "pipelinerun": results = [pipeline_summary(item) for item in matches]
            elif resource == "taskrun": results = [taskrun_summary(item) for item in matches]
            else: results = [{"name": item.get("metadata", {}).get("name"), "phase": item.get("status", {}).get("phase")} for item in matches]
            return {"resource": resource, "terms": terms, "matches": results}
        except (ValueError, json.JSONDecodeError, KeyError) as exc:
            return error(str(exc))
