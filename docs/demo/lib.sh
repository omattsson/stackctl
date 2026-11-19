# Sourced by setup.sh, check.sh and the recording shell (demo.tape).
#
# DEMO_CONFIG_DIR is the throwaway stackctl config directory of the video;
# the user's own ~/.stackmanager is never touched.
#
# stackctl lets these variables win over the config file, so a value from your
# own shell would send the recording to another server.
unset STACKCTL_API_URL STACKCTL_API_KEY STACKCTL_DEBUG STACKCTL_USERNAME STACKCTL_PASSWORD
DEMO_DIR="${KSM_DEMO_DIR:-${HOME}/.ksm-demo}"
DEMO_CONFIG_DIR="${DEMO_DIR}/stackctl-video"
DEMO_API="http://localhost:${KSM_DEMO_PORT:-8080}"
DEMO_STACK="my-first-stack"

# say "<n>. text": a caption pill on the last terminal row, in the style of
# the ksm demo video. say_init reserves that row (scroll region), so the
# caption stays while the output above scrolls.
say_init() {
  local rows
  rows="$(tput lines)"
  printf '\e[1;%dr\e[H\e[2J' "$((rows - 1))"
}

say() {
  local text="$*" cols rows left
  cols="$(tput cols)"
  rows="$(tput lines)"
  if ((${#text} > cols - 8)); then
    text="${text:0:cols-11}..."
  fi
  left=$(((cols - ${#text} - 4) / 2))
  printf '\e7\e[%d;1H\e[2K\e[%d;%dH\e[48;5;236m\e[38;5;255m\e[1m  %s  \e[0m\e8' \
    "$rows" "$rows" "$((left + 1))" "${text}"
}

PS1='\[\e[1;32m\]$\[\e[0m\] '
