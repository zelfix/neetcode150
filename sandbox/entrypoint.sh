#!/bin/sh
# Seed a cold per-user cache from the one baked into the image, then get out of
# the way. Per-user caches are separate on purpose, so the first run for each
# person would otherwise pay the full cold-build cost.
set -e
if [ -d /gocache ] && [ -z "$(ls -A /gocache 2>/dev/null)" ]; then
  cp -a /gocache-warm/. /gocache/ 2>/dev/null || true
fi
exec "$@"
