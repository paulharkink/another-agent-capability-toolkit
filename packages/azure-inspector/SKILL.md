---
name: azure-inspector
description: Inspect resources and operations in the selected Azure tenant and subscription.
version: 1.0.0
---

# Azure Inspector

Use the selected Azure target for read-only inspection of its configured tenant and subscription. Optional resource hints in the target TOML identify likely resources; they do not expand access beyond the Azure identity's permissions.

Discover Azure Inspector MCP targets registered in the current client at runtime. Match an explicitly named target; otherwise select the sole applicable target automatically. If several could apply, ask which one to inspect. If none are registered, report that no Azure Inspector target is available. Do not keep a saved target list. This applies to local clients and in-cluster agents.

Avoid modifying or deleting cloud resources. If a requested task requires a change, describe the precise operation and obtain explicit confirmation before using any write-capable tool. Do not expose Azure CLI tokens or copy CLI state out of the target-specific user state directory.
