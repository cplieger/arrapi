#!/usr/bin/env bash
# .github/scripts/annotate.sh writes exactly one escaped workflow command line,
# and refuses an unknown command or a wrong argument count. ANNOTATE_SH points
# the suite at another copy for a red-check.
# SC2015: lib.sh verdict helpers return 0.
# shellcheck disable=SC2015
set -u

# shellcheck source-path=SCRIPTDIR
. "$(dirname -- "$0")/lib.sh"
new_workdir >/dev/null
ANNOTATE="${ANNOTATE_SH:-$REPO_ROOT/.github/scripts/annotate.sh}"

# annotate <args>...: run the helper, leaving its stdout in $WORK/out, its
# stderr in $_err and its status in $_rc.
annotate() {
  _rc=0
  bash "$ANNOTATE" "$@" >"$WORK/out" 2>"$WORK/err" || _rc=$?
  _err=$(cat "$WORK/err")
}

# expect <what> <exact stdout>: the helper exited 0, wrote nothing to stderr
# and wrote exactly the given bytes.
expect() {
  printf '%s' "$2" >"$WORK/want"
  [ "$_rc" -eq 0 ] && [ -z "$_err" ] && cmp -s "$WORK/want" "$WORK/out" \
    && ok "$1" \
    || no "$1" "rc=$_rc err=$_err out=$(od -c "$WORK/out")"
}

if [ ! -f "$ANNOTATE" ]; then
  no "precondition: the helper exists" "$ANNOTATE"
  report
  exit 1
fi
ok "precondition: the helper exists at $ANNOTATE"

hostile=$'50% a:b,c\r\nd::error::injected'

annotate error 'a title' "$hostile"
expect "the message escapes %, CR and LF and keeps : and ," \
  $'::error title=a title::50%25 a:b,c%0D%0Ad::error::injected\n'

annotate warning "$hostile" 'plain'
expect "the title escapes %, CR, LF, : and ," \
  $'::warning title=50%25 a%3Ab%2Cc%0D%0Ad%3A%3Aerror%3A%3Ainjected::plain\n'

annotate notice '%0A' '%0D'
expect "% is escaped before the escapes it introduces" \
  $'::notice title=%250A::%250D\n'

annotate error '' 'no title'
expect "an empty title writes no title property" $'::error::no title\n'

for bad in group debug '' 'error title=x'; do
  annotate "$bad" 't' 'm'
  [ "$_rc" -eq 2 ] && [ ! -s "$WORK/out" ] && [ -n "$_err" ] \
    && ok "command '$bad' is refused with status 2 and no output" \
    || no "command '$bad'" "rc=$_rc err=$_err out=$(cat "$WORK/out")"
done

annotate error 'only a message'
[ "$_rc" -eq 2 ] && [ ! -s "$WORK/out" ] && [ -n "$_err" ] \
  && ok "2 arguments are refused with status 2 and no output" \
  || no "2 arguments" "rc=$_rc err=$_err out=$(cat "$WORK/out")"
annotate error t m extra
[ "$_rc" -eq 2 ] && [ ! -s "$WORK/out" ] && [ -n "$_err" ] \
  && ok "4 arguments are refused with status 2 and no output" \
  || no "4 arguments" "rc=$_rc err=$_err out=$(cat "$WORK/out")"

report
