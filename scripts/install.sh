#!/usr/bin/env bash
# AIvalon local AIO installer (Linux x64): llama.cpp CPU build + Qwen2.5-3B model + launcher.
# Needs: bash, curl, tar. Optional: jq (used for the GitHub API if present).
set -euo pipefail

# Not /releases/latest: that is the stable vX.Y.Z tag, which ships no binaries. The b<N> builds
# are prereleases; the list is newest-first, so the first matching asset is the newest build.
RELEASE_API="https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=30"
MODEL_URL="https://huggingface.co/bartowski/Qwen2.5-3B-Instruct-GGUF/resolve/main/Qwen2.5-3B-Instruct-Q4_K_M.gguf"
MODEL_FILE="Qwen2.5-3B-Instruct-Q4_K_M.gguf"
ASSET_RE='llama-b[0-9]+-bin-ubuntu-x64\.tar\.gz'
# The AIvalon archive itself isn't a GitHub Release asset — it's committed
# straight into the repo's /dist/ and served via raw.githubusercontent.com,
# pinned to this tag so it matches whatever install.sh version is running.
AIVALON_TAG="v0.2-beta-1"
AIVALON_URL="https://raw.githubusercontent.com/Lord-ZuzurNC/AIvalon/$AIVALON_TAG/dist/aivalon-linux-x64.tar.gz"

step() { printf '\n==> %s\n' "$*"; }
die()  { printf '\nERROR: %s\nInstall NOT complete.\n' "$*" >&2; exit 1; }

for cmd in curl tar find; do
  command -v "$cmd" >/dev/null 2>&1 || die "'$cmd' is required but not installed."
done

# 1. Install directory
default_dir="$HOME/.local/share/aivalon"
read -r -p "Install directory [$default_dir]: " INSTALL_DIR || INSTALL_DIR=""
INSTALL_DIR="${INSTALL_DIR:-$default_dir}"
INSTALL_DIR="${INSTALL_DIR/#\~/$HOME}"
mkdir -p "$INSTALL_DIR" 2>/dev/null || die "Cannot create '$INSTALL_DIR' (permission denied?)."
[[ -w "$INSTALL_DIR" ]] || die "No write permission to '$INSTALL_DIR'."
INSTALL_DIR="$(cd "$INSTALL_DIR" && pwd)"
echo "Installing into: $INSTALL_DIR"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# 2. Resolve latest llama.cpp asset
step "Querying recent llama.cpp releases"
json="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$RELEASE_API")" \
  || die "Could not reach the GitHub releases API ($RELEASE_API)."
if command -v jq >/dev/null 2>&1; then
  ASSET_URL="$(jq -r --arg re "^${ASSET_RE}\$" \
    '.[].assets[] | select(.name | test($re)) | .browser_download_url' <<<"$json" | head -n1)"
else
  ASSET_URL="$(grep -oE "\"browser_download_url\"[[:space:]]*:[[:space:]]*\"[^\"]*/${ASSET_RE}\"" <<<"$json" \
    | head -n1 | sed -E 's/.*"(https[^"]+)"$/\1/')"
fi
[[ -n "${ASSET_URL:-}" ]] || die "No asset matching $ASSET_RE in the 30 most recent llama.cpp releases."
ASSET_NAME="${ASSET_URL##*/}"
echo "Found: $ASSET_NAME"

# 3. Download + extract llama.cpp (extract to temp first, so a failure leaves any old install intact)
step "Downloading $ASSET_NAME"
curl -fL --progress-bar -o "$TMP_DIR/$ASSET_NAME" "$ASSET_URL" || die "Download of $ASSET_URL failed."

step "Extracting into $INSTALL_DIR/llama.cpp"
mkdir -p "$TMP_DIR/extract"
tar -xzf "$TMP_DIR/$ASSET_NAME" -C "$TMP_DIR/extract" || die "Extraction of $ASSET_NAME failed."
rm -rf "$INSTALL_DIR/llama.cpp"
mv "$TMP_DIR/extract" "$INSTALL_DIR/llama.cpp" || die "Could not move llama.cpp into $INSTALL_DIR."

LLAMA_BIN="$(find "$INSTALL_DIR/llama.cpp" -type f -name llama-server | head -n1)"
[[ -n "$LLAMA_BIN" ]] || die "llama-server not found inside $ASSET_NAME (archive layout changed?)."
chmod +x "$LLAMA_BIN"
echo "llama-server: $LLAMA_BIN"

# 3b. AIvalon release (binary + web/), same temp-extract-then-move pattern as llama.cpp
step "Installing AIvalon $AIVALON_TAG"
if [[ -x "$INSTALL_DIR/aivalon" && -d "$INSTALL_DIR/web" ]]; then
  echo "Already present, skipping."
else
  curl -fL --progress-bar -o "$TMP_DIR/aivalon.tar.gz" "$AIVALON_URL" || die "Download of $AIVALON_URL failed."
  mkdir -p "$TMP_DIR/aivalon"
  tar -xzf "$TMP_DIR/aivalon.tar.gz" -C "$TMP_DIR/aivalon" || die "Extraction of ${AIVALON_URL##*/} failed."
  # Accept both a flat archive and one wrapped in a single top-level folder.
  app_src="$(find "$TMP_DIR/aivalon" -maxdepth 2 -type f -name aivalon | head -n1)"
  app_src="${app_src%/*}"
  [[ -n "$app_src" && -d "$app_src/web" ]] || die "aivalon binary or web/ not found inside ${AIVALON_URL##*/} (archive layout changed?)."
  rm -rf "$INSTALL_DIR/web"
  { mv "$app_src/web" "$INSTALL_DIR/web" && mv -f "$app_src/aivalon" "$INSTALL_DIR/aivalon"; } \
    || die "Could not move AIvalon into $INSTALL_DIR."
  chmod +x "$INSTALL_DIR/aivalon"
fi

# 4. Model (resumable: partial download kept as .part)
step "Downloading model $MODEL_FILE (~2 GB, resumable)"
mkdir -p "$INSTALL_DIR/models"
model_path="$INSTALL_DIR/models/$MODEL_FILE"
if [[ -f "$model_path" ]]; then
  echo "Already present, skipping."
else
  curl -fL -C - --progress-bar -o "$model_path.part" "$MODEL_URL" \
    || die "Model download failed. Re-run the installer to resume."
  mv "$model_path.part" "$model_path"
fi

# 5. Launcher
step "Writing launcher $INSTALL_DIR/run-aivalon.sh"
launcher="$INSTALL_DIR/run-aivalon.sh"
{
  echo '#!/usr/bin/env bash'
  echo 'set -euo pipefail'
  printf 'INSTALL_DIR=%q\n' "$INSTALL_DIR"
  printf 'export AIVALON_LLAMA_BIN=%q\n' "$LLAMA_BIN"
  printf 'export AIVALON_LLAMA_MODELS_DIR=%q\n' "$INSTALL_DIR/models"
  echo 'export AIVALON_ADDR=127.0.0.1:4140'
  printf 'export AIVALON_DB=%q\n' "$INSTALL_DIR/aivalon.db"
  cat <<'EOF'
if [[ ! -x "$INSTALL_DIR/aivalon" ]]; then
  msg="AIvalon binary missing at $INSTALL_DIR/aivalon: the install is incomplete or damaged. Re-run the AIvalon installer."
  echo "ERROR: $msg" >&2
  command -v notify-send >/dev/null 2>&1 && notify-send "AIvalon" "$msg"
  exit 1
fi
cd "$INSTALL_DIR"  # aivalon resolves web/static relative to the working directory
exec "$INSTALL_DIR/aivalon" "$@"
EOF
} >"$launcher" || die "Could not write $launcher."
chmod +x "$launcher"

# 6. Optional desktop entry
read -r -p "Create a desktop menu shortcut? [y/N]: " ans || ans=""
if [[ "$ans" =~ ^[Yy]$ ]]; then
  apps="$HOME/.local/share/applications"
  mkdir -p "$apps" || die "Cannot create $apps."
  cat >"$apps/aivalon.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=AIvalon
Exec="$launcher"
Path=$INSTALL_DIR
Terminal=false
EOF
  chmod +x "$apps/aivalon.desktop"
  echo "Shortcut: $apps/aivalon.desktop"
fi

step "Done"
echo "Run: $launcher  then open http://127.0.0.1:4140"
