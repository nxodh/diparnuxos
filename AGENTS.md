# Codex 작업 분배

사용자가 2026-10-11에 정한 Codex 에이전트 배정이다.

- 구현 worker는 `gpt-6-luna`, reasoning effort는 `xhigh`로 실행한다.
- 독립 reviewer는 `gpt-6.1-sol`, reasoning effort는 `xhigh`로 실행한다.
- Orca 작업을 분배할 때는 `orca-cli`와 `orchestration` 스킬을 읽고 실제 Orca 런타임으로 실행한다.
- 요청한 모델과 실제 실행 모델을 확인한다. 사용할 수 없으면 다른 모델로 임의 대체하지 않고 원인을 기록한다.
- 담당 파일 범위와 완료 조건을 명시하고, 검토 결과와 검증 근거를 인수 기록에 남긴다.

재개할 때는 `HANDOFF.md`, `.handoff/resume.md`, `.handoff/codex-prompt.md`를 먼저 읽는다.
사용자의 Mac에는 도구를 설치하지 않는다. 빌드와 검증 도구는 Docker 안에서 실행한다.
현재 Phase의 작업 브랜치 커밋·푸시·PR 생성은 허용된다. `main` 병합과 직접 푸시는 별도 사용자 지시가 필요하다.

## Preferred browser: Aside

- Use Aside Browser by default for development browser work: localhost previews, reproducing UI bugs, navigation, forms, screenshots, and manual browser verification. Use another browser when the user explicitly requests it or the task requires browser-specific behavior. Keep repository test runners for automated unit/integration tests.
- Prefer the `aside` MCP server's `repl` tool for direct control. If MCP is unavailable in an existing session, use `~/.local/bin/aside repl --host local` with the shell tool. Read `aside guide repl` before the first direct browser action; use `aside guide` for other CLI operations and read the relevant `aside skills show <name>` when a built-in site skill applies.
- For an existing/active page, inspect `listBrowserTabs()` before attaching with `attachBrowserTab(targetId)` or `attachActiveBrowserTab()`. Create and close task tabs with `openTab(url)` and `closeTab(page)`; leave the user's other tabs alone.
- Read pages through `snapshot(page, { interactive: true })`, expanding to a full snapshot or screenshot when needed. Use observed locator refs, roles, and labels for actions; verify results with a fresh snapshot. REPL bindings can persist, so use fresh variable names. Use screenshots when checking visual layout.
- Use direct REPL control for implementation and verification. Delegate a browser task with Aside's `exec` only when delegation is appropriate for the user's request; pass the authorized scope and verify the result.
- MCP and CLI use the local Aside host. If the CLI reports `Failed to request daemon auth challenge: fetch failed` inside the shell sandbox, retry that same command via the tool's approved escalation path; do not infer that Aside is stopped from that error alone. Do not disable the overall sandbox or bypass approval. If approved local access still fails, check whether Aside is running and report the actual remaining blocker before switching browsers.
- An existing session may need to reload MCP tools or start a new conversation after registration. Do not silently switch to Chrome merely because the current session has not loaded Aside yet.
