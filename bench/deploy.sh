#!/bin/sh
# Deploy benchmark services with the settings deploy/service.yaml recommends.
#
#   PROJECT_ID=my-project PUBLIC_SIG_KEY=<hex> bench/deploy.sh [name...]
#
# Every service gets identical settings, so the language and runtime are the
# only variables: gen1, startup CPU boost, request-based billing, 1 vCPU and
# 512 MiB, scale to zero, and no startup probe beyond Cloud Run's default.
set -eu

: "${PROJECT_ID:?set PROJECT_ID}"
: "${PUBLIC_SIG_KEY:?set PUBLIC_SIG_KEY (bench/client keygen prints one)}"
REGION=${REGION:-europe-west2}
REPO=${REPO:-bench}
TOPIC=${TOPIC:-bench-interactions}
SA=${SA:-bench-runtime@$PROJECT_ID.iam.gserviceaccount.com}

registry="$REGION-docker.pkg.dev/$PROJECT_ID/$REPO"

deploy() {
	name=$1
	# The functions framework serves "/" only when FUNCTION_TARGET is set; the
	# other services ignore it.
	if gcloud run deploy "bench-$name" --project "$PROJECT_ID" --region "$REGION" \
		--image "$registry/$name:latest" --quiet \
		--execution-environment gen1 --cpu-boost --cpu-throttling \
		--cpu 1 --memory 512Mi --concurrency 80 --timeout 30 \
		--min-instances 0 --max-instances 2 \
		--service-account "$SA" --allow-unauthenticated \
		--set-env-vars "PUBLIC_SIG_KEY=$PUBLIC_SIG_KEY,PUBSUB_PROJECT_ID=$PROJECT_ID,PUBSUB_TOPIC_NAME=$TOPIC,FUNCTION_TARGET=handleDiscordMessage" \
		>/dev/null 2>&1; then
		echo "deployed bench-$name"
	else
		echo "FAILED   bench-$name"
		return 1
	fi
}

grep -v '^#' bench/services.txt | {
while read -r name _context _dockerfile; do
	[ -n "$name" ] || continue
	if [ $# -gt 0 ]; then
		case " $* " in *" $name "*) ;; *) continue ;; esac
	fi
	deploy "$name" &
done
# Inside the braces: the jobs belong to this pipeline's subshell.
wait
}
