#!/bin/sh
# Run one measurement round against every service in bench/services.txt.
#
#   PROJECT_NUMBER=123 KEY=path/to/bench.key bench/round.sh round-1 [results.jsonl]
#
# A round is one cold request per service, all fired together, then five warm
# ones each. It is only a cold round if every service has been idle long enough
# to scale to zero: leave at least 20 minutes between rounds and send nothing
# else to the services in between. `client report` checks this afterwards
# against the request logs rather than trusting it.
set -eu

label=${1:?usage: round.sh LABEL [results.jsonl]}
out=${2:-bench/results/results.jsonl}
: "${PROJECT_NUMBER:?set PROJECT_NUMBER}"
: "${KEY:?set KEY to the seed file written by client keygen}"
REGION=${REGION:-europe-west2}

urls=$(grep -v '^#' bench/services.txt | awk -v n="$PROJECT_NUMBER" -v r="$REGION" \
	'NF { printf "https://bench-%s-%s.%s.run.app/ ", $1, n, r }')

mkdir -p "$(dirname "$out")"
# shellcheck disable=SC2086 # one argument per URL
go run ./bench/client run -key "$KEY" -label "$label" -out "$out" -warm 5 $urls
