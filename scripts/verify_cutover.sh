#!/usr/bin/env bash
# ListenLedger — Production Cutover, Cold Reboot & Disaster Recovery Rehearsal
# References: README.md#testing and README.md#operations

set -euo pipefail

GO_BIN="${GO:-}"
if [[ -z "$GO_BIN" ]]; then
  if [[ -x "/home/recoding/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.12.linux-amd64/bin/go" ]]; then
    GO_BIN="/home/recoding/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.12.linux-amd64/bin/go"
  else
    GO_BIN="$(command -v go || echo "go")"
  fi
fi
export GO="$GO_BIN"

echo "=== ListenLedger Production Cutover & Disaster Recovery Rehearsal ==="

echo "1. Verifying SQLite backup, vacuum, and restore integrity..."
./scripts/verify_backup_restore.sh

echo "2. Verifying PostgreSQL migrations, dialect transfer, and clean restore..."
./scripts/verify_postgres_backup.sh

echo "3. Executing end-to-end cutover rehearsal & cold reboot verification suite..."
(cd backend && "$GO_BIN" test -v -race -run 'TestCutover_' ./storage)

echo "=========================================================="
echo "Production Cutover & Disaster Recovery Rehearsal: PASSED"
echo "Cold schema provisioning, multi-tenant state persistence,"
echo "abrupt server reboot, and clean disaster restore verified."
echo "=========================================================="
