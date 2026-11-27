#!/bin/sh
# Build benchmark images with Cloud Build and push them to Artifact Registry.
#
#   PROJECT_ID=my-project bench/build.sh [name...]
#
# With no names, builds every service in bench/services.txt, in parallel. Run it
# from the repository root.
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
LOGS=${LOGS:-${TMPDIR:-/tmp}/bench-build}
mkdir -p "$LOGS"
rm -f "$LOGS"/*.failed

registry="$REGION-docker.pkg.dev/$PROJECT_ID/$REPO"
config="$(pwd)/bench/cloudbuild.yaml"

build() {
	name=$1 context=$2 dockerfile=$3
	echo "building $name ($context/$dockerfile)"
	if gcloud builds submit "$context" --project "$PROJECT_ID" --region "$REGION" \
		--config "$config" --gcs-source-staging-dir "$STAGING" --service-account "$BUILDER" --quiet \
		--substitutions "_DOCKERFILE=$dockerfile,_IMAGE=$registry/$name:latest" \
		>"$LOGS/$name.log" 2>&1; then
		echo "built    $name"
	else
		echo "FAILED   $name (see $LOGS/$name.log)"
		touch "$LOGS/$name.failed"
	fi
}

grep -v '^#' bench/services.txt | {
while read -r name context dockerfile; do
	[ -n "$name" ] || continue
	if [ $# -gt 0 ]; then
		case " $* " in *" $name "*) ;; *) continue ;; esac
	fi
	build "$name" "$context" "$dockerfile" &
done
# Inside the braces: the jobs belong to this pipeline's subshell.
wait
}

# The loop runs in a pipeline subshell, so failures are reported through files.
if ls "$LOGS"/*.failed >/dev/null 2>&1; then
	exit 1
fi
