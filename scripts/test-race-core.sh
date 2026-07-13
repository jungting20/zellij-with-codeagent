#!/usr/bin/env bash
set -euo pipefail

go test -race -count=1 ./internal/eventbus ./internal/runtime ./internal/transport
