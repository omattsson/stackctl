# stackctl demo video

The sources of the demo video in the README: a terminal recording made with
[VHS](https://github.com/charmbracelet/vhs), in the style of the
k8s-stack-manager demo (numbered captions on the last row, one story through
the roles admin and alice). About 3 minutes, 1280x800, webm.

| File | Role |
|---|---|
| `demo.tape` | the recording: commands, pauses, captions |
| `setup.sh` | users, stackctl contexts and namespace on the demo cluster |
| `check.sh` | exit 0 when the cluster is in the start state of the tape |
| `lib.sh` | the caption helper `say` and the shared paths |

## Render

You need `vhs` (with `ttyd`), `ffmpeg`, `jq`, `curl`, `kubectl` and `stackctl`
(`brew install vhs jq`). The recording runs against the demo cluster of
[k8s-stack-manager](https://github.com/omattsson/k8s-stack-manager):

```bash
(cd ../k8s-stack-manager && scripts/demo.sh up)   # kind cluster ksm-demo, API on localhost:8080
docs/demo/setup.sh                                 # run before every render
rm -f docs/demo/stackctl-demo.webm
vhs docs/demo/demo.tape                            # writes docs/demo/stackctl-demo.webm; takes about 4 minutes
```

If `vhs` exits non-zero (for example a `Wait` timed out because a deploy hung),
the webm is incomplete: do not use it, fix the cause and render again.

`setup.sh` is safe to run again; the tape ends with `stack clean`, so run it
before each render. It uses its own stackctl configuration directory
(`~/.ksm-demo/stackctl-video`), so your stackctl contexts stay as they are.
Passwords of alice and devon are random and stay in `passwords.env` there
(mode 0600); the tape reads them from the environment of hidden steps and
they never appear on screen.

Remove everything with `scripts/demo.sh down` in k8s-stack-manager.

## Publish

The webm is not committed (about 1.5 MB, `.gitignore`). Upload it to GitHub (for
example by dragging it into an issue or a README edit in the browser) and put
the link under a `## Demo` heading in the README, like k8s-stack-manager does.

## Change a scene

Each scene in `demo.tape` starts with a hidden `clear; say '<n>. text'`. A
scene that wraps or shows an error is easiest to find by pulling a frame every
few seconds: `ffmpeg -i stackctl-demo.webm -vf fps=1/6 f%03d.png`.
