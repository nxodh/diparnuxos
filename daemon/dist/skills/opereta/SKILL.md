---
name: opereta
description: Inspect and troubleshoot the Opereta model server on this computer. Use it when the user asks about the local model server, its speed, context size, slots, API keys, network exposure, memory or GPU use, or when a model request fails.
---

# Opereta on this computer

Opereta is the operating system this computer runs. It serves a local language model (llama.cpp) and
you are probably talking to that model right now. A daemon (`operetad`) manages the model server and
the `opereta` command talks to it. Everything below reads state; nothing changes the system unless the
user asks for it.

## Read state (safe)

Prefer the MCP tools named `opereta_*` when they are available. Otherwise run the command:

| Question | Command | MCP tool |
| --- | --- | --- |
| Is the server running, which model, GPU and memory | `opereta status` | `opereta_status` |
| How busy is it, tokens per second, queued requests | `opereta serve stats` | `opereta_server_stats` |
| Address, model name, context of one request, key needed | `opereta connection` | `opereta_connection` |
| What plan would be used for a model and why | `opereta serve plan [model]` | `opereta_plan` |
| CPU, memory, disk, temperature, GPU load | `opereta resources` | `opereta_resources` |
| What the computer offers to the network | `opereta exposure` | `opereta_exposure` |
| Does the model call tools correctly | `opereta serve toolcheck` | `opereta_toolcheck` |
| Why did the server stop or fail to start | `opereta serve logs -n 100` | `opereta_logs` |

Add `--json` before the command for machine-readable output.

## Facts that explain most problems

- The context of one request is "context per slot". With 2 slots the server splits its total context in
  two. If a long conversation fails with a context error, the plan gave too little to each slot.
- The `agent` profile asks for at least 16K tokens per request and two slots when a GPU is present.
  Show it with `opereta serve plan --profile agent`. It is applied with `opereta config set profile agent`
  followed by a server restart (the user does this, not you).
- A request that waits is queued behind busy slots. Many parallel tool calls need more slots, not a longer context.
- HTTP 401 means an API key is required for that address. Programs on this computer need no key
  (`local_no_key`); other computers do.
- HTTP 429 means all slots and the queue are busy. Retrying after the `Retry-After` seconds works.
- A model that answers in text when it should call a tool fails `opereta serve toolcheck`. Its chat
  template has no tool support; a different model fixes it.
- Running out of GPU memory shows in `opereta status` as a CPU fallback or in the plan as "CPU only".
  Smaller model, smaller context or fewer slots are the fixes.

## Rules

- Do not run `opereta serve start`, `serve stop`, `config set`, `apikey create`, `firewall`, `network set`,
  `update` or `user` commands unless the user asked for that change. Describe the command instead.
- Never print or store API keys. A key is shown once when it is created; ask the user to read it themselves.
- The firewall closes everything by default. Do not suggest opening a port without saying what it exposes.
