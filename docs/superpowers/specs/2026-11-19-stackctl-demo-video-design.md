# stackctl demo video: design

## Intent

The k8s-stack-manager README has a 3:17 demo video (`ksm-demo.webm`, 1280x800,
numbered caption bar at the bottom of each scene, one story told through three
roles). stackctl has no video. Make one in the same style, from a terminal,
against the demo kind cluster that `make demo` of k8s-stack-manager creates.
The video goes in the stackctl README (and the ksm README can link to it).

## Goals

- A 2 to 3 minute webm, 1280x800, with numbered captions like the ksm video
  ("1. Sign in as alice").
- The developer loop first: login, definitions, create and deploy a stack with
  `--follow`, get/list as table, JSON and `-q`, `stack pods`, `port-forward`,
  `exec`, `logs`, `clean` with its confirmation prompt. One short scripting
  scene at the end (`-q`, `-o json`, exit code).
- Same cast as the ksm video: admin, alice (plain user), devon (devops).
- Reproducible: anyone with the demo cluster can re-render the video.

## Non-goals and anti-goals

- No change to stackctl code, docs or `agent_guide.md`.
- No GIF, no upload to GitHub (user attachments cannot be automated; the
  maintainer uploads the file and edits the README).
- The video file is not committed (about 15 MB). Only the sources are.
- Anti-goal: a password, token or API key on screen or in the recording.

## Constraints

- Demo cluster: `scripts/demo.sh up` in k8s-stack-manager (kind cluster
  `ksm-demo`, UI and API on `localhost:8080`, admin password in
  `~/.ksm-demo/secrets.yaml`). stackctl keeps its own config directory there.
- stackctl 0.10.1 has no `user create`; alice and devon must be created
  through the API (or the UI) in the setup step.
- Server refuses `exec` into a pod with a service-account token; `demo.sh`
  patches the default ServiceAccount of the `hello` stack namespace. Alice's
  stack has another namespace and needs the same patch before deploy.
- Needs `vhs` (brew; pulls in ttyd) and ffmpeg (installed).

## Approaches

- **A. VHS, scripted (picked).** A `.tape` types the commands and renders webm.
  Reproducible; `Wait` handles the variable deploy time. Cost: a terminal
  render, not a real screen capture.
- B. asciinema + agg + ffmpeg: real keystrokes, but manual takes and captions
  synced by hand.
- C. Real screen recording: not reproducible, cannot be driven from here.

## Design

Files in `docs/demo/`:

- `setup.sh`: checks the demo cluster is up; creates alice and devon through
  the API with passwords from the environment; creates stackctl contexts
  `admin`, `alice`, `devon` (same API URL, own token files) in a throwaway
  `STACKCTL_CONFIG_DIR`; pre-creates alice's stack namespace and turns off the
  token mount of its default ServiceAccount; removes leftovers from an earlier
  run (stack `my-first-stack`).
- `lib.sh`: sourced inside the recording shell. `say "<n>. text"` draws the
  caption pill on the last terminal row (save cursor, move to the last row,
  dark rounded bar, restore cursor), so captions need no timing against the
  video. Also a short prompt.
- `demo.tape`: theme, font size and 1280x800 size; scenes in order, each
  starting with `say`. Typing speed about 40 ms, pauses long enough to read.
- `README.md`: how to bring up the cluster, run `setup.sh`, render
  (`vhs docs/demo/demo.tape`), and what the maintainer does with the file.
- `.gitignore` entry for `docs/demo/*.webm`.

Scenes (about 12): 1 admin signs in, definitions; 2 admin adds alice and devon
(this is shown as one `stackctl user list` after setup, since there is no
create command); 3 switch to alice (`config use-context`); 4 create and
deploy with `--follow`; 5 table / `-o json` / `-q`; 6 `stack pods`,
`port-forward` with a `curl`; 7 `exec -- hostname`; 8 `logs`; 9 `clean` with
the prompt; 10 scripting: `stack list -q | xargs ...`, exit code of a failing
command; 11 closing caption "Try it yourself: make demo".

Verification: render, pull a frame every 5 seconds with ffmpeg, read the
contact sheets scene by scene, and check that no secret appears.

## Left to the builder

- Exact commands for scenes 6 to 10, as far as they work on the demo cluster
  (for example, whether a plain user may `port-forward` and `exec`; if not,
  run those scenes as devon).
- The API call that creates a user, theme, font size, pause lengths.
- Whether `jq` is used in the scripting scene (only if installed on the
  machine that renders).

## My calls (not yours yet)

- VHS with in-terminal captions, not an ffmpeg overlay.
- Contexts `admin`/`alice`/`devon` to switch personas on screen.
- Users created in `setup.sh`, shown with `user list`, not typed in the video.
- Sources in `docs/demo/`; the webm not committed; file name `stackctl-demo.webm`.
- Nothing is committed or pushed until you say so.
