#!/usr/bin/env bash
# Points this clone's git hooks at scripts/githooks (pre-commit: gofmt and vet
# on staged files; pre-push: gofmt, vet, staticcheck and the skill test).
# Undo with: git config --unset core.hooksPath
set -euo pipefail
cd -- "$(git rev-parse --show-toplevel)"
chmod +x scripts/githooks/pre-commit scripts/githooks/pre-push
git config core.hooksPath scripts/githooks
printf 'git hooks installed: core.hooksPath = %s\n' "$(git config core.hooksPath)"
