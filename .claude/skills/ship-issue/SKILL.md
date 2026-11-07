---
name: ship-issue
description: Plan a GitHub issue or feature of the stackctl CLI and carry it out, in the main session or by starting the specialist agents (go-cli-developer, qa-engineer, devops-engineer, code-reviewer). Use for multi-step work across commands, client, tests, and build.
argument-hint: "<issue number or feature description>"
---

# Ship an issue

You are the tech lead in the main session. Plan the work, start the specialist agents directly, and track the result. This replaces the former `orchestrator` subagent: a planning subagent repeated the work of the main session, and the results of every agent that it started stayed in its context.

## Plan

1. Read the issue: `gh issue view <number>`. `CLAUDE.md` is loaded already. Read `docs/claude/api-reference.md` when the issue touches a server route, and check the server side in k8s-stack-manager when the route is new.
2. Choose a workflow sequence below. Leave out steps that the change does not need.
3. Small change (one command or one client method, a few files): do it in the main session, without agents. An agent costs a fresh context and a summary round trip.
4. Write a short numbered plan: one agent for each step, with acceptance criteria. Show it to the user before the first agent starts when the scope is not clear.

## Delegate

Give each agent a self-contained brief. The agent does not see this conversation.

- The goal, the acceptance criteria, and the issue number.
- The files, functions, and API routes to start from, and the facts that you already found (server version, response fields). The agent should not search for them again.
- The branch or worktree to work in.
- Ask for a short report: files changed, tests run with their results, and open questions. No full diffs and no full command output.

## Team

| Agent | Use for |
| --- | --- |
| go-cli-developer | Types, client methods, Cobra commands, output formatting, with tests |
| qa-engineer | Coverage gaps, integration, e2e, contract, and live tests |
| devops-engineer | Makefile, cross-compilation, GitHub Actions, goreleaser, Dockerfile |
| code-reviewer | Review of finished work: security, UX, pattern compliance |

## Implementation order

Types → client methods → command → output formatting → tests → `cmd/agent_guide.md` and `docs/` when users or agents need to know.

## Workflow sequences

### New command or command group

1. go-cli-developer: types, client methods, commands, output formatting, tests.
2. code-reviewer: review. Add qa-engineer only when the review finds coverage gaps, or for a new command group.

### Bug fix

1. Write a test that fails because of the bug (in the main session for a small fix, else qa-engineer).
2. go-cli-developer: fix the bug, unless the main session already can.
3. code-reviewer: review the fix.

### CI/CD or build change

1. devops-engineer: implement.
2. code-reviewer: review.

## Finish

1. Check the reports against the acceptance criteria. Run `cd cli && go test ./... && make lint` yourself before you report done.
2. Commit on the branch. Push and open a pull request only when the user asks.
3. When the context of this session is past about 40%, hand over before the next large step: run `/handoff` if you have that skill, or `/compact` with a focus on the remaining work.
