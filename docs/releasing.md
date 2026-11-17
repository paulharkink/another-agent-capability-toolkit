# Releasing AACT

Use the repository's automated release workflow. Do not create or push version
tags manually, dispatch the build-and-publish workflow manually, or open a
handwritten release PR.

1. Finish and review the feature PRs that belong in the release. Merge them to
   `main` only with the maintainer's explicit approval for those merges.
2. After those feature PRs are merged, go to GitHub Actions and run **Request
   release** on `main`. Choose the requested patch, minor, or major version
   bump. The workflow updates the
   README install versions in both the Unix shell and Windows PowerShell
   snippets and opens a `release/vX.Y.Z` PR. The workflow checks that both
   commands contain the selected version before it opens the PR.
3. Review the release PR and its checks. Merge it only with the maintainer's
   explicit approval. The merge triggers the tag workflow, which verifies that
   both README install commands name the same version, creates the `vX.Y.Z` tag
   on the merge commit, and starts the release workflow. The release workflow
   checks both commands again before publishing.
4. Monitor **Build and publish release artifacts** to completion. It runs the
   Go tests and GoReleaser validation, builds and publishes the multi-platform
   MCP images, assembles their manifests, and publishes the GitHub release
   assets. Confirm the workflow succeeded and verify the release assets and
   image tags before reporting the release complete.

The release tag must point to `main` after the feature PRs have been merged.
Never create a release tag from a feature branch or a release PR that contains
unmerged product changes. If publication fails, rerun **Build and publish
release artifacts** from `main` with `release_tag` set to the existing tag
(for example, `v0.7.3`). Leave `publish_assets_only` off to retry image and
release publication. If the required public multi-platform images already
exist and only GitHub release assets need publishing, set `publish_assets_only`
to `true`; that path verifies the existing images before publishing the assets.
Do not create a replacement tag.
