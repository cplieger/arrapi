#!/usr/bin/env bash
# annotate.sh <error|warning|notice> <title> <message>
#
# Writes one workflow command line. The message is escaped as the Actions
# toolkit's escapeData does and the title as its escapeProperty does, so no
# value can end the line or start a second workflow command:
# https://github.com/actions/toolkit/blob/main/packages/core/src/command.ts
# An empty title writes no title property, as the toolkit omits it.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  printf 'usage: %s <error|warning|notice> <title> <message>\n' "$0" >&2
  exit 2
fi
case $1 in
  error | warning | notice) ;;
  *)
    printf '%s: unknown workflow command %s\n' "$0" "$1" >&2
    exit 2
    ;;
esac

# % first, or the escapes added after it would be escaped again.
data=${3//'%'/%25}
data=${data//$'\r'/%0D}
data=${data//$'\n'/%0A}

props=''
if [ -n "$2" ]; then
  title=${2//'%'/%25}
  title=${title//$'\r'/%0D}
  title=${title//$'\n'/%0A}
  title=${title//:/%3A}
  title=${title//,/%2C}
  props=" title=$title"
fi

printf '::%s%s::%s\n' "$1" "$props" "$data"
