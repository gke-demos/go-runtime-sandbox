---
title: go-runtime-sandbox
---

{{< blocks/cover title="go-runtime-sandbox" image_anchor="top" height="med" >}}

<p class="lead mt-5">
Ship arbitrary Go source code into a <a href="https://github.com/kubernetes-sigs/agent-sandbox">Kubernetes Agent Sandbox</a> from a host program, compile and execute it inside the sandboxed pod, get the result back. Built for the AI-agent use case: an LLM writes Go code, calls a tool, sees the output, iterates.
</p>

<a class="btn btn-lg btn-primary me-3 mb-4" href="docs/quickstart/">Get started <i class="fa-solid fa-arrow-right ms-2"></i></a>
<a class="btn btn-lg btn-secondary me-3 mb-4" href="https://github.com/gke-demos/go-runtime-sandbox">Source on GitHub <i class="fa-brands fa-github ms-2"></i></a>

{{< /blocks/cover >}}

{{% blocks/lead color="primary" %}}

Three pieces, working together: a **runtime image** that bundles a Go toolchain and a tiny HTTP server inside a gVisor-isolated pod, a **Go library** (`pkg/goruntime`) that ships source files and shell commands into it, and an **MCP server** (`cmd/mcp-server`) that exposes one `run_go_code` tool to any MCP client — Claude Code, Gemini CLI, Cursor.

{{% /blocks/lead %}}

{{% blocks/section color="dark" type="row" %}}

{{% blocks/feature icon="fa-solid fa-shield-halved" title="Real isolation" url="docs/install/" %}}
Runs Go programs inside a Kubernetes Agent Sandbox — gVisor by default on GKE. The agent writes the code; gVisor's user-space kernel keeps it from touching the host or the cluster.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-solid fa-arrows-rotate" title="State persists across calls" url="docs/how-it-works/" %}}
The MCP server holds one sandbox per session. Files written in call N are still there in call N+1; the Go build cache stays warm; binaries built earlier remain executable. Iterating agents pay sub-second per-call latency.
{{% /blocks/feature %}}

{{% blocks/feature icon="fa-solid fa-plug" title="One tool, two clients" url="docs/mcp/" %}}
Single `run_go_code` MCP tool. Ready-made config samples for Claude Code (<code>.mcp.json</code>) and Gemini CLI (<code>.gemini/settings.json</code>) ship in the repo — copy, edit the path, restart the client.
{{% /blocks/feature %}}

{{% /blocks/section %}}

{{% blocks/section color="white" %}}

<div class="td-content col-12 col-lg-8 mx-auto">

## Status

Proof of concept, validated end-to-end on:

- **kind 0.31 + Kubernetes 1.34** — no sandbox runtime, useful for local development.
- **GKE Autopilot 1.36.0-gke.1759000** — agent-sandbox addon enabled, gVisor confirmed active via `runtimeClassName=gvisor`.

The MCP server has been driven by Claude Code against real GKE: it iterates, builds, runs, and surfaces compile errors back to the model. See the [GKE install guide]({{< relref "install.md" >}}) for the deployment recipe.

</div>

{{% /blocks/section %}}
