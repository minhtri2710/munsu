---
name: ask-user-authority
description: Boundary rules for autonomous execution versus asking human user authority. Use when deciding whether a task/action requires human approval.
---

# Ask User Authority (Human Gate Boundaries)

This skill defines the strict boundaries between actions an agent may perform autonomously and actions that REQUIRE explicit human user authorization.

## Autonomous Actions (No Human Permission Required)

Agents are fully authorized to perform the following actions within their assigned worktree:

1. **Reading & Inspection:** Inspecting code, git status, logs, documentation, and system state.
2. **Code Editing & Refactoring:** Modifying code, writing unit tests, formatting, and fixing bugs within the task worktree.
3. **Local Testing & Building:** Running unit tests (`go test`), builds (`go build`), linters (`go vet`), and local validation tools.
4. **Draft PR Creation:** Pushing feature branches (`mu/<task-id>`) and opening draft PRs via `no-mistakes` or `gh-axi`.
5. **Status Reporting:** Appending status lines (`munsu report working|needs-decision|blocked|done|failed`).

## Mandatory Human Gates (Requires Explicit Human Authorization)

An agent MUST STOP and issue a `needs-decision` report or ask the user directly before executing any of the following:

1. **Destructive Operations:**
   - Deleting database tables, production data, or remote repositories.
   - Force-pushing to shared branches (`git push --force`).
   - Removing files outside the task worktree.
2. **Production Deployment & Release:**
   - Merging PRs into default production branches (unless automated via `PR.CanMerge()` pipeline).
   - Cutting releases, publishing packages, or deploying to production environments.
3. **Credentials & External API Mutating Operations:**
   - Modifying Actions secrets, API keys, or security permissions.
   - Invoking paid third-party APIs with high-cost implications.
4. **Architecture & Scope Expansion:**
   - Altering core system contracts or database schemas not requested in the task brief.
   - Modifying global configuration files (`~/.munsu/config/*`).

## Protocol

When encountering a Mandatory Human Gate:
1. Formulate a concise summary of the proposed action, trade-offs, and risk.
2. Call `munsu report needs-decision "{summary}"` and stop execution.
3. Wait for human review and explicit authorization before proceeding.
