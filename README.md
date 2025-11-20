# LM Studio NATS Service

A Go (1.25+) service that controls [LM Studio](https://lmstudio.ai) **entirely via NATS** (no HTTP REST API exposed), designed for environments where:

- You want **pure NATS** (pub/sub, request-reply, JetStream) as the integration bus.
- LM Studio runs **directly on the host** (no Docker), with full access to **NVIDIA / AMD GPU drivers**.
- Optionally, **JetStream Object Store (file bucket)** acts as a **central model repository**: nodes download models and materialize them locally for LM Studio.

---

## Features

- ✅ **NATS-only API** – no HTTP REST gateway required
- ✅ Integrates with LM Studio via:
    - `lms server start` (local HTTP API, default `http://127.0.0.1:1234`)
    - `lms get`, `lms unload` (CLI for managing models)
- ✅ NATS **request–reply** subjects:
    - `lmstudio.list_models` – list models known by LM Studio
    - `lmstudio.pull_model` – pull/download a model via `lms get`
    - `lmstudio.delete_model` – unload and delete a model from disk
    - `lmstudio.chat_model` – chat/completion requests to LM Studio
- ✅ Host-only setup (no Docker) for:
    - LM Studio
    - NATS Server (with JetStream enabled)
- ♻️ **JetStream Object Store (file bucket) – optional**:
    - Use NATS JetStream as a *central repository* of `.gguf` model files
    - Nodes can download models from the Object Store, write them to disk in LM Studio’s models directory, and optionally run `lms import`

---

## High-Level Architecture

```
+------------------+         +-------------------+         +------------------------+
|  Your Services   | <-----> |    NATS Server    | <-----> |  LM Studio NATS Worker |
|  (Go / TS / etc) |   NATS  |  (JetStream opt.) |   NATS  |  (this project, Go)   |
+------------------+         +-------------------+         +-----------+------------+
                                                                         |
                                                                         | HTTP + CLI
                                                                         v
                                                                 +----------------+
                                                                 |   LM Studio    |
                                                                 | (local server) |
                                                                 +----------------+
                                                                       |
                                                                       | local files
                                                                       v
                                                          ~/.lmstudio/models / custom dir
```

> **Note**: LM Studio always needs the model as a local file on disk (e.g. in `~/.lmstudio/models/...`).  
> JetStream Object Store can be used as a central store, but each node must still download the model to disk before using it.

---

## NATS Subjects (API)

### 1. `lmstudio.list_models`

- **Description**: List models known by the LM Studio local server.
- **Request subject**: `lmstudio.list_models`
- **Payload**: empty JSON object `{}` (or `{}` as a placeholder)
- **Response**:

```json
{
  "ok": true,
  "data": {
    "http_status": 200,
    "models": {
      "data": [
        {
          "id": "granite-3.0-2b-instruct",
          "publisher": "ibm-granite",
          "...": "..."
        }
      ]
    }
  }
}
```

The `models` field is the raw JSON returned by LM Studio’s `/api/v0/models` endpoint.

**Example (CLI):**
```bash
nats req lmstudio.list_models '{}'
```

---

### 2. `lmstudio.pull_model`

- **Description**: Download a model via `lms get <identifier>`.
- **Request subject**: `lmstudio.pull_model`
- **Request payload**:

```json
{
  "identifier": "meta-llama/Meta-Llama-3-8B-Instruct"
}
```

- **Response** (success):

```json
{
  "ok": true,
  "data": {
    "model": "meta-llama/Meta-Llama-3-8B-Instruct",
    "output": "stdout/stderr from `lms get` ..."
  }
}
```

- **Response** (error):

```json
{
  "ok": false,
  "error": "erro ao executar 'lms get ...': ...",
  "data": {
    "model": "meta-llama/Meta-Llama-3-8B-Instruct",
    "output": "stdout/stderr from lms ..."
  }
}
```

**Example (CLI):**
```bash
nats req lmstudio.pull_model '{
  "identifier": "meta-llama/Meta-Llama-3-8B-Instruct"
}'
```

---

### 3. `lmstudio.delete_model`

- **Description**: Unload a model and delete it from the LM Studio model directory.
- **Request subject**: `lmstudio.delete_model`
- **Request payload**:

```json
{
  "model_id": "granite-3.0-2b-instruct"
}
```

- **Behavior**:
  1. Calls `lms unload <model_id>` (best-effort)
  2. Calls LM Studio’s `/api/v0/models/{model_id}` to get model info (publisher, ID)
  3. Computes the model folder, e.g. `~/.lmstudio/models/<publisher>/<id>`
  4. Removes that directory with `os.RemoveAll`

- **Response** (success):

```json
{
  "ok": true,
  "data": {
    "model_id": "granite-3.0-2b-instruct",
    "deleted_dir": "/home/user/.lmstudio/models/ibm-granite/granite-3.0-2b-instruct"
  }
}
```

- **Response** (error example):

```json
{
  "ok": false,
  "error": "modelo ... não encontrado no LM Studio",
  "data": {
    "model_id": "granite-3.0-2b-instruct",
    "dir": "/home/user/.lmstudio/models/ibm-granite/granite-3.0-2b-instruct"
  }
}
```

**Example (CLI):**
```bash
nats req lmstudio.delete_model '{
  "model_id": "granite-3.0-2b-instruct"
}'
```

---

### 4. `lmstudio.chat_model`

- **Description**: Sends a chat/completion request to LM Studio (proxy to `/api/v0/chat/completions`)
- **Request subject**: `lmstudio.chat_model`
- **Request payload**: Any valid LM Studio/OpenAI-style chat payload, but must include `model` field:

```json
{
  "model": "granite-3.0-2b-instruct",
  "messages": [
    { "role": "system", "content": "You are a helpful assistant." },
    { "role": "user", "content": "Explain briefly what LM Studio is." }
  ],
  "temperature": 0.7
}
```

If `model` is missing or the JSON is invalid, an error is returned.

- **Response** (success):

```json
{
  "ok": true,
  "data": {
    "http_status": 200,
    "response": {
      "id": "chatcmpl-...",
      "choices": [
        {
          "message": {
            "role": "assistant",
            "content": "..."
          }
        }
      ],
      "usage": {
        "prompt_tokens": 15,
        "completion_tokens": 42,
        "total_tokens": 57
      }
    }
  }
}
```

**Example (CLI):**
```bash
nats req lmstudio.chat_model '{
  "model": "granite-3.0-2b-instruct",
  "messages": [
    { "role": "system", "content": "Você é um assistente útil." },
    { "role": "user", "content": "Explique rapidamente o que é o LM Studio." }
  ],
  "temperature": 0.7
}'
```

---

## JetStream Object Store (File Bucket) for Models (Optional Design)

If you want a central model repository using NATS:

- Use NATS JetStream Object Store as a storage backend for large `.gguf` model files.

For each node:
1. Download the model from the Object Store.
2. Write it to the LM Studio models directory.
3. Optionally, import via `lms import`.

This lets you:
- Avoid storing models manually on each host.
- Avoid Docker or shared NFS/SMB to synchronize models.

**Example: Creating a Model Bucket**

Once JetStream is enabled on your NATS server:

```bash
# Create an Object Store bucket for models
nats obj add llm-models

# Upload a model to the bucket
nats obj put llm-models ./meta-llama-3-8b-instruct.gguf \
  --name meta-llama/Meta-Llama-3-8B-Instruct/model.gguf
```

Recommended naming pattern:
```
<publisher>/<model>/<file>.gguf
# e.g. meta-llama/Meta-Llama-3-8B-Instruct/model.gguf
```

You can extend this service with an extra subject, e.g.:

#### `lmstudio.sync_model_from_bucket`

**Example request (conceptual):**
```json
{
  "bucket": "llm-models",
  "object_name": "meta-llama/Meta-Llama-3-8B-Instruct/model.gguf",
  "publisher": "meta-llama",
  "model_dir": "Meta-Llama-3-8B-Instruct"
}
```
The worker would then:

1. Open Object Store `llm-models`
2. Download `object_name`
3. Write to:
   ```
   $LMSTUDIO_MODELS_DIR/<publisher>/<model_dir>/model.gguf
   ```
4. Optionally:
   ```bash
   lms import /path/to/model.gguf
   ```

5. Respond with:
```json
{
  "ok": true,
  "data": {
    "local_path": "/home/user/.lmstudio/models/meta-llama/Meta-Llama-3-8B-Instruct/model.gguf"
  }
}
```

---

## Project Structure (Suggested)
```
.
├── main.go
├── go.mod
├── go.sum
├── .env                # generated by setup scripts (not committed)
├── scripts
│   ├── setup_unix.sh      # macOS / Linux host setup
│   └── setup_windows.ps1  # Windows host setup (winget + NATS)
└── README.md           # this file
```

---

## Requirements

- Go: 1.25+
- LM Studio (host installation, no Docker)
    - Linux/macOS: manual download from https://lmstudio.ai
    - Windows: via winget (see below)
- LM Studio CLI: `lms` available in PATH
- NATS Server (with JetStream enabled)
- (Optional, but recommended) NATS CLI (`nats`) for debugging & manual requests.

---

## Host Setup

> This repository assumes you do **NOT** run LM Studio in Docker, to ensure full access to GPU drivers (NVIDIA/AMD) on the host.

### macOS / Linux

Use the setup script:

```bash
chmod +x scripts/setup_host_unix.sh
./scripts/setup_host_unix.sh
```

What it does:
- Installs LM Studio (macOS via Homebrew, Linux: manual download)
- Ensures `lms` is bootstrapped (typically from `~/.lmstudio/bin/lms bootstrap`)
- Starts LM Studio local server:

    ```bash
    lms server start --port 1234 --background
    ```

- Installs NATS Server natively (no Docker), and generates a simple `nats-server.conf` with JetStream enabled:

    ```hcl
    port: 4222

    jetstream {
      store_dir: "./nats_data"
      max_memory_store: 1073741824   # 1 GiB
      max_file_store:   107374182400 # 100 GiB
    }
    ```

- Starts NATS server in background and generates a `.env` file:

    ```env
    NATS_URL=nats://127.0.0.1:4222
    LMSTUDIO_BASE_URL=http://127.0.0.1:1234
    # LMSTUDIO_MODELS_DIR=$HOME/.lmstudio/models
    ```

> Adjust ports / paths as needed for your environment.

---

### Windows (PowerShell)

Use the setup script:

```powershell
# Run from a PowerShell prompt
Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
.\scripts\setup_host_windows.ps1
```

What it does:
- Installs LM Studio via winget:

    ```powershell
    winget install -e --id ElementLabs.LMStudio `
      --accept-source-agreements --accept-package-agreements
    ```

- Bootstraps `lms`:

    ```powershell
    $env:USERPROFILE\.lmstudio\bin\lms.exe bootstrap
    ```

- Starts LM Studio server:

    ```powershell
    lms server start --port 1234 --background
    ```

- Installs NATS Server (e.g. via Chocolatey):

    ```powershell
    choco install nats-server -y
    ```

- Generates `nats-server.conf` with JetStream and starts NATS server minimized:

    ```hcl
    port: 4222

    jetstream {
      store_dir: "./nats_data"
      max_memory_store: 1073741824
      max_file_store:   107374182400
    }
    ```

- Writes a `.env` file:

    ```env
    NATS_URL=nats://127.0.0.1:4222
    LMSTUDIO_BASE_URL=http://127.0.0.1:1234
    # LMSTUDIO_MODELS_DIR=%USERPROFILE%\.lmstudio\models
    ```

---

## Running the Service

After host setup (`.env` created, LM Studio and NATS running):

```bash
# From project root
go mod tidy

# Run directly
set -a; [ -f .env ] && . .env; set +a; \
go run .
```

Or build a binary:

```bash
go build -o bin/lmstudio-nats ./...
NATS_URL=nats://127.0.0.1:4222 \
LMSTUDIO_BASE_URL=http://127.0.0.1:1234 \
./bin/lmstudio-nats
```

The service will:
- Connect to `NATS_URL`
- Use `LMSTUDIO_BASE_URL` to talk to the LM Studio local HTTP API
- Subscribe (with a queue group) to:
    - `lmstudio.list_models`
    - `lmstudio.pull_model`
    - `lmstudio.delete_model`
    - `lmstudio.chat_model`

You can scale out by running multiple instances of the service with the same queue group—NATS will load-balance requests.

---

## Environment Variables

The service recognizes:
- `NATS_URL` (default: `nats://127.0.0.1:4222`)
- `LMSTUDIO_BASE_URL` (default: `http://127.0.0.1:1234`)
- `LMSTUDIO_MODELS_DIR` (optional; default: `~/.lmstudio/models`)
- `NATS_QUEUE_GROUP` (optional; default: `lmstudio-workers`)

**Example `.env`:**
```env
NATS_URL=nats://127.0.0.1:4222
LMSTUDIO_BASE_URL=http://127.0.0.1:1234
LMSTUDIO_MODELS_DIR=$HOME/.lmstudio/models
NATS_QUEUE_GROUP=lmstudio-workers
```

---

## Example: Calling from Go

A simple example of calling `lmstudio.chat_model` from another Go service:

```go
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

type NATSResponse struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func main() {
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Drain()

	reqPayload := map[string]interface{}{
		"model": "granite-3.0-2b-instruct",
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Say hello from NATS client in Go."},
		},
		"temperature": 0.7,
	}

	b, _ := json.Marshal(reqPayload)

	msg, err := nc.Request("lmstudio.chat_model", b, 2*time.Minute)
	if err != nil {
		log.Fatal("request error:", err)
	}

	var resp NATSResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		log.Fatal("unmarshal error:", err)
	}

	if !resp.OK {
		log.Fatalf("LLM error: %s", resp.Error)
	}

	fmt.Println("Raw data:", string(resp.Data))
}
```

---

## Notes

- This project is designed to be GPU-friendly: LM Studio runs directly on the *host* (not in Docker), so you can configure CUDA/ROCm and drivers exactly as needed.
- NATS + JetStream provide a lightweight, high-performance control and distribution plane for LLM workloads.
- The JetStream Object Store pattern is especially useful for multiple nodes and a single source of truth for your `.gguf` models.

---

## License

TBD – choose the license that fits your project (MIT / Apache-2.0 / etc.).
