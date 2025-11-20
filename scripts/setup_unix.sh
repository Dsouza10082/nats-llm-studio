#!/usr/bin/env bash
set -euo pipefail

echo "===> Detecting operating system..."
OS="$(uname -s || echo 'unknown')"
echo "OS detected: $OS"

LM_PORT="${LMSTUDIO_PORT:-1234}"
NATS_PORT="${NATS_PORT:-4222}"

echo
echo "===> Step 1: Installation of LM Studio (host)"

if [[ "$OS" == "Darwin" ]]; then
  # macOS: Homebrew Cask
  if command -v brew >/dev/null 2>&1; then
    echo "Installing LM Studio via Homebrew (brew install --cask lm-studio)..."
    brew install --cask lm-studio || true
  else
    echo "[WARNING] Homebrew not found. Install at https://brew.sh."
    echo "Depois baixe o LM Studio em: https://lmstudio.ai/"
  fi
elif [[ "$OS" == "Linux" ]]; then
  cat <<'EOF'
[INFO] In Linux, download LM Studio manually:
  https://lmstudio.ai/

Open the app at least once to create the directory ~/.lmstudio
EOF
else
  echo "[WARNING] OS not supported by this script. Continue manually."
fi

echo
echo "===> Step 2: Configuring CLI 'lms'"

if command -v lms >/dev/null 2>&1; then
  echo "'lms' is already in PATH."
else
  if [[ -x "$HOME/.lmstudio/bin/lms" ]]; then
    echo "Executing bootstrap of lms (~/.lmstudio/bin/lms bootstrap)..."
    "$HOME/.lmstudio/bin/lms" bootstrap || true
  else
    echo "[WARNING] ~/.lmstudio/bin/lms not found."
    echo "Open LM Studio at least once and try again, or consult:"
    echo "  https://lmstudio.ai/docs/cli"
  fi
fi

echo
echo "===> Step 3: Starting the LM Studio server (local API on port ${LM_PORT})"

if command -v lms >/dev/null 2>&1; then
  lms server start --port "${LM_PORT}" --background || true
  echo "LM Studio server requested at http://127.0.0.1:${LM_PORT}"
else
  echo "[WARNING] CLI 'lms' not available, start the server manually later:"
  echo "  lms server start --port ${LM_PORT}"
fi

echo
echo "===> Step 4: Installation of NATS server (without Docker)"

if [[ "$OS" == "Darwin" ]]; then
  if command -v brew >/dev/null 2>&1; then
    echo "Instalando nats-server via Homebrew..."
    brew install nats-server >/dev/null
  else
    echo "[AVISO] Instale o NATS server manualmente em: https://nats.io/download/"
  fi
elif [[ "$OS" == "Linux" ]]; then
  NATS_VERSION="${NATS_VERSION:-v2.10.12}"
  echo "Baixando NATS server ${NATS_VERSION}..."
  curl -L "https://github.com/nats-io/nats-server/releases/download/${NATS_VERSION}/nats-server-${NATS_VERSION}-linux-amd64.zip" -o /tmp/nats-server.zip
  sudo apt-get install -y unzip >/dev/null 2>&1 || true
  unzip -o /tmp/nats-server.zip -d /tmp/nats-server
  sudo cp /tmp/nats-server/nats-server-*/nats-server /usr/local/bin/nats-server
  sudo chmod +x /usr/local/bin/nats-server
else
  echo "[WARNING] For this OS, install NATS manually:"
  echo "  https://nats.io/download/"
fi

echo
echo "===> Step 5: Creating simple NATS config with JetStream"

cat > nats-server.conf <<EOF
port: ${NATS_PORT}

jetstream {
  store_dir: "./nats_data"
  max_memory_store: 1073741824 # 1 GiB
  max_file_store:   107374182400 # 100 GiB
}
EOF

echo "Config gerada em ./nats-server.conf"

echo
echo "Starting nats-server with JetStream in background..."
if command -v nats-server >/dev/null 2>&1; then
  nohup nats-server -js -c ./nats-server.conf > nats-server.log 2>&1 &
  echo "NATS server started at nats://127.0.0.1:${NATS_PORT}"
else
  echo "[WARNING] nats-server not found in PATH. Start manually later."
fi

echo
echo "===> Step 6: Generating .env for the Go service"

cat > .env <<EOF
NATS_URL=nats://127.0.0.1:${NATS_PORT}
LMSTUDIO_BASE_URL=http://127.0.0.1:${LM_PORT}
# LMSTUDIO_MODELS_DIR=\$HOME/.lmstudio/models
EOF

echo "Content of .env:"
cat .env

echo
echo "Unix setup completed."
