# Domain glossary

## Actor Rank

The hierarchical rank of an agent instance operating in the fleet:
- `general`: Fleet Orchestrator (highest authority).
- `captain`: Persistent Domain Supervisor.
- `soldier`: Short-lived Task Worker.

Specified via `MUNSU_ROLE=general|captain|soldier`.

## Task Kind

The functional classification of a task assigned to a soldier or captain:
- `ship`: Code delivery task (implements features/fixes, subject to no-mistakes delivery pipeline).
- `scout`: Investigation or audit task (produces research report).
- `captain-supervisor`: Persistent supervisor lane for domain management.

## Branch Prefix

The git branch naming prefix for soldier worktrees. Default is `mu/<task-id>` (configured via `config/branch-prefix`). Legacy `fm/` prefix is deprecated and unsupported (Clean Break Policy).

## Uplink Report

A material report sent from a lower rank to exactly one parent rank.

## Delivery Notification

An immediate signal that a durable Uplink Report is waiting for the receiving rank.

## Processing Ack

Confirmation that the receiving agent has accepted an Uplink Report into its context. It does not mean that any follow-up action has been completed.
