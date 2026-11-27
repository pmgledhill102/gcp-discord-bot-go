#!/bin/sh
# Build benchmark images with Cloud Build and push them to Artifact Registry.
#
#   PROJECT_ID=my-project bench/build.sh [name...]
#
# With no names, builds every service in bench/services.txt. Builds are
# submitted asynchronously and polled with one list call: a dozen gcloud
# processes each polling their own build hit the API's rate limit. Run it from
# the repository root.
set -eu

: "${PROJECT_ID:?set PROJECT_ID}"
REGION=${REGION:-europe-west2}
REPO=${REPO:-bench}
# gcloud's default staging bucket is in the US multi-region, which an org
# resource-location policy can refuse. Stage in the build region instead.
STAGING=${STAGING:-gs://$PROJECT_ID-bench-build/source}
# A dedicated build identity holding only read on the staging bucket, write on
# the image repository and log writing. Without one, Cloud Build runs as the
# default compute service account.
BUILDER=${BUILDER:-projects/$PROJECT_ID/serviceAccounts/bench-builder@$PROJECT_ID.iam.gserviceaccount.com}

registry="$REGION-docker.pkg.dev/$PROJECT_ID/$REPO"
config="$(pwd)/bench/cloudbuild.yaml"
ids=$(mktemp)
trap 'rm -f "$ids"' EXIT

grep -v '^#' bench/services.txt | while read -r name context dockerfile; do
	[ -n "$name" ] || continue
	if [ $# -gt 0 ]; then
		case " $* " in *" $name "*) ;; *) continue ;; esac
	fi
	id=$(gcloud builds submit "$context" --project "$PROJECT_ID" --region "$REGION" \
		--config "$config" --gcs-source-staging-dir "$STAGING" --service-account "$BUILDER" \
		--substitutions "_DOCKERFILE=$dockerfile,_IMAGE=$registry/$name:latest" \
		--async --quiet --format 'value(id)' 2>/dev/null)
	echo "submitted $name $id"
	echo "$id $name" >>"$ids"
done

[ -s "$ids" ] || { echo "nothing to build" >&2; exit 2; }

while :; do
	sleep 20
	pending=0 failed=0
	statuses=$(gcloud builds list --project "$PROJECT_ID" --region "$REGION" \
		--limit 100 --format 'value(id,status)')
	while read -r id name; do
		status=$(echo "$statuses" | awk -v id="$id" '$1 == id { print $2 }')
		case $status in
		SUCCESS) ;;
		QUEUED | WORKING | PENDING | "") pending=$((pending + 1)) ;;
		*) failed=$((failed + 1)) ;;
		esac
	done <"$ids"
	[ "$pending" -eq 0 ] && break
done

while read -r id name; do
	status=$(echo "$statuses" | awk -v id="$id" '$1 == id { print $2 }')
	echo "$status $name ($id)"
done <"$ids"

[ "$failed" -eq 0 ]
