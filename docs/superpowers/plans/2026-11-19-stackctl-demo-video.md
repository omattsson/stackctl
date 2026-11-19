# stackctl Demo Video Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A reproducible 2 to 3 minute webm demo of stackctl against the k8s-stack-manager demo kind cluster, in the style of `ksm-demo.webm`.

**Architecture:** A VHS tape types the commands in a bash shell that sources `lib.sh` (the caption helper). `setup.sh` prepares the cluster state (users, contexts, namespace) so the recording itself stays short. Sources live in `docs/demo/`; the webm is rendered, not committed.

**Tech Stack:** VHS (+ ttyd), ffmpeg, bash, kind/kubectl/helm, stackctl 0.10.1.

**Spec:** `docs/superpowers/specs/2026-11-19-stackctl-demo-video-design.md`

## Global Constraints

- Output `docs/demo/stackctl-demo.webm`, 1280x800, 2 to 3 minutes, not committed (`.gitignore`: `docs/demo/*.webm`).
- Numbered captions on the last terminal row, drawn by `say "<n>. text"`; closing caption "Try it yourself: make demo".
- Cast: admin, alice (plain user), devon (devops); stackctl contexts `admin`, `alice`, `devon`.
- Demo cluster: `scripts/demo.sh up` in `~/git/k8s-stack-manager` (kind cluster `ksm-demo`, API on `localhost:8080`, admin password in `~/.ksm-demo/secrets.yaml`).
- A throwaway `STACKCTL_CONFIG_DIR`; the user's own stackctl contexts stay untouched.
- No password, token or API key on screen or in the recording.
- No stackctl code, docs or `agent_guide.md` change. Nothing is committed or pushed until the user says so.

## Review Focus

- `setup.sh` run twice (users, contexts, stack `my-first-stack` already exist): must succeed and leave a clean start state.
- Demo cluster not running or `localhost:8080` not answering: `setup.sh` exits non-zero with a message naming `scripts/demo.sh up`.
- Deploy slower than the tape expects: the `Wait` times out and the render fails loudly, not a recording of a half-finished deploy.
- Terminal narrower or caption longer than the width: caption is truncated, not wrapped over the prompt.
- A secret on screen: no frame, and no line of the tape, shows a password.
- User's real `~/.stackmanager` config: unchanged after `setup.sh` and a render.

---

### Task 1: Tools, cluster and open questions

**Files:**
- Create: `docs/demo/FINDINGS.md` (scratch, deleted in Task 4; records the answers below)

**Interfaces:**
- Produces: facts Tasks 2 and 3 use: the create-user API call, which persona may `port-forward`/`exec`, namespace name of alice's stack.

- [ ] **Step 1: Install and check tools.** `brew install vhs`; verify `vhs --version`, `ttyd --version`, `ffmpeg -version`, `helm version`, `docker info`.
- [ ] **Step 2: Bring up the demo cluster.** Run `scripts/demo.sh up` in `~/git/k8s-stack-manager` (about 5 minutes). Expected: ends with "K8s Stack Manager runs in the kind cluster ksm-demo."; `curl -s localhost:8080/health/live` answers.
- [ ] **Step 3: Find the create-user call.** Read the backend routes and `docs/api.md` for how an admin creates a local user; confirm with `curl` as admin (a user `probe`, then delete it with `stackctl user delete`). Record the call and body in `FINDINGS.md`.
- [ ] **Step 4: Probe permissions.** As alice (plain user), create a stack from "Hello World", deploy it, then try `stack pods`, `port-forward`, `exec -- hostname`, `logs`, `clean`. Record which fail and the error text. Expected for the design: if `port-forward` or `exec` is refused for a plain user, scenes 6 and 7 run as devon.
- [ ] **Step 5: Record the namespace** of alice's stack (`stackctl stack get <name> -o json`), needed for the service-account patch in `setup.sh`.

### Task 2: `setup.sh` and `lib.sh`

**Files:**
- Create: `docs/demo/setup.sh`, `docs/demo/lib.sh`, `docs/demo/check.sh`

**Interfaces:**
- Produces: `setup.sh` (no args; reads `~/.ksm-demo/secrets.yaml`; exports nothing; writes `~/.ksm-demo/stackctl-video/` as config dir and prints its path). `lib.sh` defines `say() { ... }` (arg: caption text; truncates to `$COLUMNS`) and sets `PS1`. `check.sh` exits 0 when the start state is right.

- [ ] **Step 1: Write `check.sh`** (the failing test): exits 1 unless the API answers on `localhost:8080`, contexts `admin`, `alice`, `devon` exist in the video config dir, `stackctl --context`-free login works for each, and stack `my-first-stack` does not exist. Run it; expected FAIL (no config dir yet).
- [ ] **Step 2: Write `setup.sh`**: `set -euo pipefail`; refuse with a message naming `scripts/demo.sh up` when the API does not answer; create alice and devon with the call from Task 1 (passwords generated once, stored mode 0600 in the video config dir, passed through `STACKCTL_PASSWORD`, never printed); create the three contexts with `stackctl config`; log each in; delete a leftover `my-first-stack`; create alice's namespace and patch the default ServiceAccount (`automountServiceAccountToken: false`) the way `demo.sh` does. Idempotent: existing users are accepted.
- [ ] **Step 3: Write `lib.sh`**: `say` saves the cursor, moves to the last row, prints the text centered in a dark pill (ANSI 48;5;236 background), restores the cursor; truncates to `COLUMNS - 4`.
- [ ] **Step 4: Run `setup.sh` twice, then `check.sh`.** Expected: both runs exit 0, `check.sh` exits 0. Also run `bash -n` on all three, and check `~/.stackmanager` is unchanged (`ls -la` before and after).
- [ ] **Step 5: Test the caption in a real terminal:** `source docs/demo/lib.sh; say "1. Sign in as alice"` in an 80-column and a 40-column terminal. Expected: pill on the last row, truncated at 40.
- [ ] **Step 6: Commit** (only when the user says so): `docs/demo/setup.sh docs/demo/lib.sh docs/demo/check.sh`.

### Task 3: `demo.tape` and the render

**Files:**
- Create: `docs/demo/demo.tape`
- Output: `docs/demo/stackctl-demo.webm` (ignored)

**Interfaces:**
- Consumes: `setup.sh`, `lib.sh`, the Task 1 findings.

- [ ] **Step 1: Write the tape header.** `Output docs/demo/stackctl-demo.webm`, `Set Width 1280`, `Set Height 800`, `Set Shell bash`, `Set TypingSpeed 40ms`, a dark theme, a font size readable at 1280 wide; `Require stackctl`. A `Hide` block exports `STACKCTL_CONFIG_DIR` and sources `lib.sh`, then `Show`.
- [ ] **Step 2: Write scenes 1 to 11 from the design**, each starting with `say`, each ending with a pause long enough to read. Use `Wait+Screen@120s /<expected text>/` after `stack deploy --follow`. Passwords come from the hidden block's environment, never typed.
- [ ] **Step 3: Render.** `cd ~/git/stackctl && vhs docs/demo/demo.tape`. Expected: exit 0, the webm exists, duration 120 to 180 s (`ffprobe`), 1280x800.
- [ ] **Step 4: Review frames.** `ffmpeg -i stackctl-demo.webm -vf "fps=1/5,scale=640:-1,tile=3x3"` into the scratchpad, read each sheet, fix scenes that look wrong (cut-off captions, errors, unreadable text), re-render.
- [ ] **Step 5: Secret check.** `grep -n -i 'password\|token\|sk_' docs/demo/demo.tape` shows no value; the contact sheets show none.

### Task 4: README, ignore rule, cleanup

**Files:**
- Create: `docs/demo/README.md`
- Modify: `.gitignore` (add `docs/demo/*.webm`)
- Delete: `docs/demo/FINDINGS.md`

- [ ] **Step 1: Write `docs/demo/README.md`**: prerequisites (vhs, ffmpeg, the demo cluster), the three commands (`scripts/demo.sh up`, `docs/demo/setup.sh`, `vhs docs/demo/demo.tape`), and that the maintainer uploads the webm to GitHub and edits the README.
- [ ] **Step 2: Add the ignore rule;** verify with `git check-ignore -v docs/demo/stackctl-demo.webm`.
- [ ] **Step 3: Delete `FINDINGS.md`;** move anything a future re-render needs into the README.
- [ ] **Step 4: Final check:** `git status` shows only `docs/demo/*`, the spec, this plan and `.gitignore`; re-run `setup.sh` then the render once from a clean shell. Report the webm path to the user.
- [ ] **Step 5: Commit** only when the user says so.

---

## Self-review

- Spec coverage: goals (scenes 1 to 11, captions, cast) in Task 3; setup, contexts, API users and SA patch in Task 2; non-goals respected (no code change, webm ignored, no upload); open items (plain-user permissions, create-user call, jq) in Task 1. `jq` is decided in Task 3 Step 2 by what Task 1 shows installed.
- Names: `say`, `setup.sh`, `check.sh`, `lib.sh`, `FINDINGS.md`, `stackctl-demo.webm` used consistently.
- Scene 2 of the design ("admin adds users") is shown as `stackctl user list` after setup, as the spec says.
