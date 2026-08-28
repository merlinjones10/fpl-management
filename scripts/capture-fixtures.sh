#!/usr/bin/env bash
# Refresh testdata from live API responses.
#
# The FPL API is undocumented and unversioned: it gains fields between seasons
# and nulls things out pre-season. Re-run this when a season rolls over, then
# run the tests — a decode or Validate failure is the early warning.
set -euo pipefail

LEAGUE_ID="${1:-1058423}"
UA='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0 Safari/537.36'
OUT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/testdata"
BASE='https://fantasy.premierleague.com/api'

mkdir -p "$OUT"

echo "league $LEAGUE_ID standings -> testdata/standings-live.json"
curl -sS --fail -H "User-Agent: $UA" \
  "$BASE/leagues-classic/$LEAGUE_ID/standings/?page_standings=1&page_new_entries=1" \
  | jq . > "$OUT/standings-live.json"

# bootstrap-static is ~1.6MB and almost all of it is player data we never read.
echo "gameweek calendar -> testdata/bootstrap-events.json"
curl -sS --fail -H "User-Agent: $UA" "$BASE/bootstrap-static/" \
  | jq '{events: [.events[] | {id,name,deadline_time,deadline_time_epoch,finished,data_checked,is_previous,is_current,is_next,average_entry_score,highest_score,released}]}' \
  > "$OUT/bootstrap-events.json"

echo
echo "captured:"
ls -lh "$OUT"
echo
echo "next: go test ./..."
