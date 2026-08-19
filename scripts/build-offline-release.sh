#!/usr/bin/env bash
# 生成 Linux amd64 的扁平 Cloud 离线包。目标服务器只需 docker load 后以 --no-build 启动。
set -euo pipefail

release_tag="${1:?usage: scripts/build-offline-release.sh <release-tag> [output-dir]}"
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${2:-$root_dir/release}"
if [[ "$output_dir" != /* ]]; then
  output_dir="$root_dir/$output_dir"
fi
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
stage_dir="$(mktemp -d)"
api_image="flyprint/cloud-api:${release_tag}"
session_file_image="flyprint/session-file-service:${release_tag}"
admin_image="flyprint/cloud-admin-builder:${release_tag}"
container_name="flyprint-admin-export-${release_tag//[^a-zA-Z0-9_.-]/-}-$$"

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  rm -rf "$stage_dir"
}
trap cleanup EXIT

require_command() { command -v "$1" >/dev/null || { echo "missing command: $1" >&2; exit 1; }; }
require_command docker
require_command sha256sum

archive_creator=""
python_command=()
if command -v zip >/dev/null; then
  archive_creator="zip"
elif command -v python3 >/dev/null && python3 --version >/dev/null 2>&1; then
  archive_creator="python"
  python_command=(python3)
elif command -v python >/dev/null && python --version >/dev/null 2>&1; then
  archive_creator="python"
  python_command=(python)
elif command -v py >/dev/null; then
  archive_creator="python"
  python_command=(py -3)
else
  echo "missing command: zip (or python3/python for the portable archive fallback)" >&2
  exit 1
fi

cd "$root_dir"

# 模板必须覆盖发布 Compose 的全部变量，防止打出无法启动或含旧配置的包。
mapfile -t compose_vars < <(grep -hEo '\$\{[A-Z0-9_]+' deploy/docker-compose.release*.yml | sed 's/^${//' | sort -u)
for name in "${compose_vars[@]}"; do
  if ! grep -qE "^${name}=" .env.release.example; then
    echo ".env.release.example is missing ${name}" >&2
    exit 1
  fi
done
if grep -qE '^(MINIO_PORT|MINIO_CONSOLE_PORT|REDIS_|SITE_PORTAL_API_TOKEN|INTEGRATION_DEMO|PROVIDER_)=' .env.release.example; then
  echo ".env.release.example contains removed release variables" >&2
  exit 1
fi

docker build --platform linux/amd64 -t "$api_image" ./api
docker build --platform linux/amd64 -t "$session_file_image" ./services/session-file-service
docker build --platform linux/amd64 -t "$admin_image" ./admin
admin_container_id="$(docker create --name "$container_name" "$admin_image")"
mkdir -p "$stage_dir/admin-built"
docker cp "$admin_container_id:/app/dist/." "$stage_dir/admin-built/"
docker rm "$admin_container_id" >/dev/null

runtime_images=(
  postgres:15.13
  quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z
  quay.io/minio/mc:RELEASE.2025-04-16T18-13-26Z
  nginx:1.27-alpine
  certbot/certbot:v3.1.0
)
pull_runtime_image() {
  local image="$1"
  local attempt
  for attempt in 1 2 3; do
    if docker pull --platform linux/amd64 "$image"; then
      return 0
    fi
    echo "retrying runtime image pull (${attempt}/3): ${image}" >&2
  done
  return 1
}
for image in "${runtime_images[@]}"; do
  pull_runtime_image "$image"
done

cp .env.release.example "$stage_dir/.env.release.example"
sed -i "s|^CLOUD_API_IMAGE_TAG=.*|CLOUD_API_IMAGE_TAG=${api_image}|" "$stage_dir/.env.release.example"
sed -i "s|^SESSION_FILE_SERVICE_IMAGE_TAG=.*|SESSION_FILE_SERVICE_IMAGE_TAG=${session_file_image}|" "$stage_dir/.env.release.example"
cp deploy/docker-compose.release.yml "$stage_dir/docker-compose.release.yml"
cp deploy/docker-compose.release.certbot.yml "$stage_dir/docker-compose.certbot.yml"
cp deploy/docker-compose.release.https.yml "$stage_dir/docker-compose.https.yml"
cp deploy/PUBLIC-DEPLOYMENT.md "$stage_dir/README.md"
cp deploy/session-file-minio-policy.json "$stage_dir/session-file-minio-policy.json"
cp -R nginx "$stage_dir/nginx"
docker image save -o "$stage_dir/docker-images-linux-amd64.tar" "$api_image" "$session_file_image" "${runtime_images[@]}"

(cd "$stage_dir" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
archive_path="$output_dir/flyprint-cloud-${release_tag}-offline-linux-amd64.zip"
if [[ "$archive_creator" == "zip" ]]; then
  (cd "$stage_dir" && zip -qr "$archive_path" .)
else
  python_stage_dir="$stage_dir"
  python_archive_path="$archive_path"
  case "$(uname -s)" in
    MINGW*|MSYS*)
      python_stage_dir="$(cygpath -w "$stage_dir")"
      python_archive_path="$(cygpath -w "$archive_path")"
      ;;
  esac
  "${python_command[@]}" - "$python_stage_dir" "$python_archive_path" <<'PY'
import pathlib
import sys
import zipfile

source = pathlib.Path(sys.argv[1])
archive = pathlib.Path(sys.argv[2])
with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
    for path in sorted(source.rglob("*")):
        if path.is_file():
            output.write(path, path.relative_to(source))
PY
fi
if [[ "$archive_creator" == "zip" ]]; then
  flat_layout="$(zipinfo -1 "$archive_path" | grep -Fx './docker-compose.release.yml' || true)"
else
  flat_layout="$("${python_command[@]}" - "$python_archive_path" <<'PY'
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1]) as archive:
    if "docker-compose.release.yml" in archive.namelist():
        print("present")
PY
)"
fi
if [[ -z "$flat_layout" ]]; then
  echo "offline package must use a flat archive layout" >&2
  exit 1
fi
echo "offline package created: $archive_path"
