# Plan: battlefield hierarchy + remaining hard gaps

## Rank map (authoritative)
- **Marshal** = munsu fleet orchestrator / primary home / fleet CLI surface
- **Second** = persistent domain supervisor (today: secondmate)
- **Crew** = task worker (today: crewmate)

Hierarchy: `Marshal → Second → Crew` (same depth as firstmate primary → secondmate → crewmate).

## Delivery order
1. `munsu-rank-rename` — full breaking rename across CLI/API/meta/env/labels/docs/skills/tests + migration notes
2. `munsu-watcher-guard` — watcher liveness beacon + guard truthfulness on real/temp homes
3. `munsu-teardown-husk` — second/retire + topology husk cleanup (no leftover herdr workspaces/tabs)
4. `munsu-second-bootstrap` — registry/converge/liveness/handoff depth + second launch extensions/pre-sync
5. `munsu-live-harnesses` — live validation for remaining installed harnesses; document deferrals

## Non-goals
- Upstream firstmate contributions
- Changing delivery modes / backlog domain separation
- Implementing uninstalled harnesses as "live verified" without installs

## Success criteria
- Rank vocabulary is consistent in code, CLI, runtime identity, labels, docs, skills
- Watch ensure → guard healthy without false NEVER STARTED on a live daemon
- Retire/teardown leaves no husk workspaces for munsu-owned labels
- Second bootstrap matches firstmate depth for registry, converge, liveness, handoff, launch extensions
- Remaining harness adapters either live-verified or explicitly deferred with evidence
