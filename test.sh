#!/usr/bin/env bash
set -eo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

env GOCACHE="${GOCACHE:-/tmp/etlegacy-go-build-cache}" go build -o ./demoparser .
./demoparser '/home/raf/.etlegacy/legacy/demos/2025-06/2025-06-26-174921-etl_braundorf.dm_84'
