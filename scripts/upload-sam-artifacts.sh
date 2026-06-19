#!/usr/bin/env sh
set -eu

artifact_store="${ARTIFACT_STORE_URL:-http://127.0.0.1:8080}"
dataset="${DATASET:-demo/serverless/sam/dataset.json}"
bucket="${BUCKET:-most-dog}"
output_dir="${OUTPUT_DIR:-/tmp/most-dog-workflow-requests}"

usage() {
  cat <<EOF
Usage: $0 [--artifact-store URL] [--dataset PATH] [--bucket NAME] [--output-dir DIR]

Uploads SAM demo images to the artifact store and writes workflow request JSON.
Requires: curl, jq
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --artifact-store)
      artifact_store="$2"
      shift 2
      ;;
    --dataset)
      dataset="$2"
      shift 2
      ;;
    --bucket)
      bucket="$2"
      shift 2
      ;;
    --output-dir)
      output_dir="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

need curl
need jq

if [ ! -f "$dataset" ]; then
  echo "dataset not found: $dataset" >&2
  exit 1
fi

dataset_dir=$(CDPATH= cd -- "$(dirname -- "$dataset")" && pwd)
dataset_file="$dataset_dir/$(basename -- "$dataset")"
mkdir -p "$output_dir"

artifact_ref() {
  printf 'artifact://%s' "$1"
}

put_object() {
  key="$1"
  file="$2"
  content_type="$3"
  curl --noproxy '*' -fsS \
    -X PUT \
    -H "Content-Type: $content_type" \
    --data-binary "@$file" \
    "$artifact_store/objects/$key" >/dev/null
}

tmp_dataset=$(mktemp)
trap 'rm -f "$tmp_dataset" "$output_dir/.request.tmp"' EXIT

dataset_key="$bucket/dataset.json"
cp "$dataset_file" "$tmp_dataset"

case_ids=$(jq -r '.cases[].id' "$dataset_file")
for case_id in $case_ids; do
  image_rel=$(jq -r --arg id "$case_id" '.cases[] | select(.id == $id) | .image.path' "$dataset_file")
  image_id=$(jq -r --arg id "$case_id" '.cases[] | select(.id == $id) | .image.id // .id' "$dataset_file")
  image_file="$dataset_dir/$image_rel"
  image_name=$(basename -- "$image_file")
  image_key="$bucket/images/$image_name"
  mask_key="$bucket/masks/$case_id.json"
  score_key="$bucket/scores/$case_id.json"

  if [ ! -f "$image_file" ]; then
    echo "image not found for case $case_id: $image_file" >&2
    exit 1
  fi

  content_type=$(case "$image_name" in
    *.jpg|*.jpeg|*.JPG|*.JPEG) printf 'image/jpeg' ;;
    *.png|*.PNG) printf 'image/png' ;;
    *) printf 'application/octet-stream' ;;
  esac)

  put_object "$image_key" "$image_file" "$content_type"

  image_ref=$(artifact_ref "$image_key")
  mask_ref=$(artifact_ref "$mask_key")
  score_ref=$(artifact_ref "$score_key")

  tmp_next=$(mktemp)
  jq \
    --arg id "$case_id" \
    --arg imageRef "$image_ref" \
    --arg maskRef "$mask_ref" \
    --arg scoreRef "$score_ref" \
    '(.cases[] | select(.id == $id) | .imageRef) = $imageRef |
     (.cases[] | select(.id == $id) | .maskRef) = $maskRef |
     (.cases[] | select(.id == $id) | .scoreRef) = $scoreRef' \
    "$tmp_dataset" > "$tmp_next"
  mv "$tmp_next" "$tmp_dataset"

  jq -c \
    --arg id "$case_id" \
    --arg imageId "$image_id" \
    --arg imageRef "$image_ref" \
    --arg maskRef "$mask_ref" \
    --arg scoreRef "$score_ref" \
    '.cases[] | select(.id == $id) |
     {
       caseId: .id,
       imageId: $imageId,
       imageRef: $imageRef,
       image: {id: $imageId, type: "artifact", artifactRef: $imageRef},
       prompt: .prompt,
       target: .target,
       maskRef: $maskRef,
       scoreRef: $scoreRef
     }' \
    "$dataset_file" > "$output_dir/$case_id.json"
done

put_object "$dataset_key" "$tmp_dataset" "application/json"

jq -nc \
  --arg datasetRef "$(artifact_ref "$dataset_key")" \
  --arg rankingRef "$(artifact_ref "$bucket/outputs/most-dog-ranking.json")" \
  '{datasetRef: $datasetRef, rankingRef: $rankingRef}' \
  > "$output_dir/make-ranking.json"

jq -nc \
  --arg status ok \
  --arg datasetRef "$(artifact_ref "$dataset_key")" \
  --arg outputDir "$output_dir" \
  --argjson requests "$(printf '%s\n' "$case_ids" | sed '/^$/d' | wc -l)" \
  '{status: $status, datasetRef: $datasetRef, requests: $requests, outputDir: $outputDir}'
