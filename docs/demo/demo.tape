# stackctl demo video. Render from the repository root, after docs/demo/setup.sh:
#
#   docs/demo/setup.sh && vhs docs/demo/demo.tape
#
# Each scene starts with a hidden "clear; say '<n>. ...'" that draws the
# caption (docs/demo/lib.sh). Passwords come from the environment of the
# hidden blocks and never appear on screen.
Output docs/demo/stackctl-demo.webm

Require stackctl
Require jq
Require curl

Set Shell bash
Set Width 1280
Set Height 800
Set FontSize 16
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 30
Set WaitTimeout 120s

# Setup (hidden).
Hide
Type@1ms "export STACKCTL_CONFIG_DIR=$HOME/.ksm-demo/stackctl-video; source docs/demo/lib.sh; source $STACKCTL_CONFIG_DIR/passwords.env; say_init" Enter
Sleep 500ms
Show

# 1. Sign in as the admin
Hide
Type@1ms "clear; say '1. Sign in as the admin; a definition says which Helm charts make a stack'; export STACKCTL_PASSWORD=$ADMIN_PASSWORD" Enter
Show
Sleep 1s
Type "stackctl login --username admin"
Enter
Sleep 3s
Type 'stackctl definition get "Hello World"'
Enter
Sleep 6s

# 2. Users
Hide
Type@1ms "clear; say '2. The admin has added two users: alice (user) and devon (devops)'" Enter
Show
Sleep 1s
Type "stackctl user list"
Enter
Sleep 6s

# 3. Alice
Hide
Type@1ms "clear; say '3. Switch to alice, a plain user: other people'\''s stacks are visible'; export STACKCTL_PASSWORD=$ALICE_PASSWORD" Enter
Show
Sleep 1s
Type "stackctl config use-context alice"
Enter
Sleep 1s
Type "stackctl login --username alice"
Enter
Sleep 3s
Type "stackctl stack list"
Enter
Sleep 6s

# 4. Create
Hide
Type@1ms "clear; say '4. Create your own stack from a definition'" Enter
Show
Sleep 1s
Type 'stackctl stack create --definition "Hello World" --name my-first-stack --ttl 480'
Enter
Sleep 6s

# 5. Deploy
Hide
Type@1ms "clear; say '5. Deploy: the charts are installed with Helm, and --follow shows the log'" Enter
Show
Sleep 1s
Type "stackctl stack deploy my-first-stack --follow"
Enter
Wait+Screen@120s /All pods healthy/
Sleep 6s

# 6. One answer, three formats
Hide
Type@1ms "clear; say '6. The same data as a table, as JSON, or as IDs only'" Enter
Show
Sleep 1s
Type "stackctl stack get my-first-stack"
Enter
Sleep 6s
Type "stackctl stack get my-first-stack -o json | jq '{name, status, namespace}'"
Enter
Sleep 4s
Type "stackctl stack list -q"
Enter
Sleep 4s

# 7. Pods
Hide
Type@1ms "clear; say '7. Live pod status, and the container you may exec into (*)'" Enter
Show
Sleep 1s
Type "stackctl stack pods my-first-stack"
Enter
Sleep 6s

# 8. Port-forward
Hide
Type@1ms "clear; say '8. Reach the app on localhost with port-forward'" Enter
Show
Sleep 1s
Type "stackctl stack port-forward my-first-stack podinfo 9898 >/dev/null 2>&1 &"
Enter
Sleep 4s
Type "curl -s localhost:9898 | jq '{hostname, version, message}'"
Enter
Sleep 4s
Type "kill %1; wait %1 2>/dev/null"
Enter
Sleep 3s

# 9. Exec
Hide
Type@1ms "clear; say '9. Run a command in the pod'" Enter
Show
Sleep 1s
Type "stackctl stack exec my-first-stack podinfo -- hostname"
Enter
Sleep 6s

# 10. Logs
Hide
Type@1ms "clear; say '10. Every deploy is kept: history, and the log of each'" Enter
Show
Sleep 1s
Type "stackctl stack history my-first-stack"
Enter
Sleep 6s

# 11. Scripting
Hide
Type@1ms "clear; say '11. Made for scripts: IDs only with -q, exit codes that mean something'" Enter
Show
Sleep 1s
Type "stackctl stack list -o json | jq -r '.data[].name'"
Enter
Sleep 3s
Type "stackctl stack get no-such-stack; echo exit code $?"
Enter
Sleep 6s

# 12. Clean up
Hide
Type@1ms "clear; say '12. Destructive commands ask first; --yes skips the prompt'" Enter
Show
Sleep 1s
Type "stackctl stack clean my-first-stack"
Enter
Sleep 3s
Type "n"
Enter
Sleep 1s
Type "echo exit code $?"
Enter
Sleep 4s
Type "stackctl stack clean my-first-stack --yes"
Enter
Wait+Screen@120s /Cleaning stack/
Sleep 4s

# Closing
Hide
Type@1ms "clear; say 'Try it yourself: make demo'" Enter
Show
Sleep 1s
Type "# brew install --cask omattsson/tap/stackctl"
Enter
Type "# git clone https://github.com/omattsson/k8s-stack-manager && cd k8s-stack-manager && make demo"
Enter
Sleep 6s
