---
title: Documentation
linkTitle: Documentation
weight: 1
menu:
  main:
    weight: 10
---

`go-runtime-sandbox` reference docs. The site root has the marketing pitch; this section is the practical guide.

## Start here

**New to the project?** → [Quick start]({{< relref "quickstart.md" >}}) gets you from zero to a running `cmd/demo` against a local kind cluster in under five minutes.

**Want to deploy on GKE?** → [Install]({{< relref "install.md" >}}) walks through enabling the agent-sandbox addon, pushing images, and applying the right kustomize overlay for the Validating Admission Policy.

**Wiring up an LLM?** → [MCP server]({{< relref "mcp.md" >}}) covers registration in Claude Code, Gemini CLI, and any other stdio-MCP client.

**Trying to understand the design?** → [How it works]({{< relref "how-it-works.md" >}}) covers the three execution layers, the multi-file upload mechanism, lifecycle decisions, and the gotchas we hit during real-world testing.

## Reference index

- **[Quick start]({{< relref "quickstart.md" >}})** — the one-script kind path.
- **[Install]({{< relref "install.md" >}})** — kind walkthrough, full GKE recipe, runtime-class overlays, warm-pool tuning, troubleshooting.
- **[MCP server]({{< relref "mcp.md" >}})** — the `run_go_code` tool, registering it in Claude Code and Gemini CLI, sample prompts.
- **[How it works]({{< relref "how-it-works.md" >}})** — design and rationale (formerly `docs/design.md` in the repo).
