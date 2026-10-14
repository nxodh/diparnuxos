---
name: diparnux
description: Inspect and troubleshoot the Diparnux model server on this computer. Use it when the user asks about the local model server, its speed, context size, slots, API keys, network exposure, memory or GPU use, or when a model request fails.
---

# Diparnux on this computer

Diparnux is the operating system this computer runs. It serves a local language model (llama.cpp) and
you are probably talking to that model right now. A daemon (`diparnuxd`) manages the model server and
the `diparnux` command talks to it. Everything below reads state; nothing changes the system unless the
user asks for it.

## Read state (safe)

Prefer the MCP tools named `diparnux_*` when they are available. Otherwise run the command:

| Question | Command | MCP tool |
| --- | --- | --- |
| Is the server running, which model, GPU and memory | `diparnux status` | `diparnux_status` |
| How busy is it, tokens per second, queued requests | `diparnux serve stats` | `diparnux_server_stats` |
| Address, model name, context of one request, key needed | `diparnux connection` | `diparnux_connection` |
| What plan would be used for a model and why | `diparnux serve plan [model]` | `diparnux_plan` |
| CPU, memory, disk, temperature, GPU load | `diparnux resources` | `diparnux_resources` |
| What the computer offers to the network | `diparnux exposure` | `diparnux_exposure` |
| Does the model call tools correctly | `diparnux serve toolcheck` | `diparnux_toolcheck` |
| Why did the server stop or fail to start | `diparnux serve logs -n 100` | `diparnux_logs` |

Add `--json` before the command for machine-readable output.

## Facts that explain most problems

- The context of one request is "context per slot". With 2 slots the server splits its total context in
  two. If a long conversation fails with a context error, the plan gave too little to each slot.
- The `agent` profile asks for at least 16K tokens per request and two slots when a GPU is present.
  Show it with `diparnux serve plan --profile agent`. It is applied with `diparnux config set profile agent`
  followed by a server restart (the user does this, not you).
- A request that waits is queued behind busy slots. Many parallel tool calls need more slots, not a longer context.
- HTTP 401 means an API key is required for that address. Programs on this computer need no key
  (`local_no_key`); other computers do.
- HTTP 429 means all slots and the queue are busy. Retrying after the `Retry-After` seconds works.
- A model that answers in text when it should call a tool fails `diparnux serve toolcheck`. Its chat
  template has no tool support; a different model fixes it.
- Running out of GPU memory shows in `diparnux status` as a CPU fallback or in the plan as "CPU only".
  Smaller model, smaller context or fewer slots are the fixes.

## Rules

- Do not run `diparnux serve start`, `serve stop`, `config set`, `apikey create`, `firewall`, `network set`,
  `update` or `user` commands unless the user asked for that change. Describe the command instead.
- Never print or store API keys. A key is shown once when it is created; ask the user to read it themselves.
- The firewall closes everything by default. Do not suggest opening a port without saying what it exposes.
