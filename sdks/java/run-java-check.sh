#!/bin/sh
# Dependency-free Java SDK check: compiles the SDK + LlmuxSmokeTest with javac
# and runs it with java. Stages a fake llmux binary (a tiny python HTTP server
# honoring LLMUX_ADDR) and points LLMUX_BINARY at it, unless LLMUX_BINARY is
# already set (e.g. to the real gateway).
#
# Usage:  sh run-java-check.sh
set -eu

cd "$(dirname "$0")"

OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

echo "compiling..."
javac -d "$OUT" \
  src/main/java/to/llmux/*.java \
  src/test/java/to/llmux/LlmuxSmokeTest.java

# Stage a fake binary unless a real one was provided.
if [ "${LLMUX_BINARY:-}" = "" ]; then
  PY="$(command -v python3 || command -v python || true)"
  if [ -n "$PY" ]; then
    WRAP="$OUT/llmux"
    FIX="$(pwd)/src/test/fixtures/fake_llmux.py"
    printf '#!/bin/sh\nexec "%s" "%s"\n' "$PY" "$FIX" > "$WRAP"
    chmod +x "$WRAP"
    LLMUX_BINARY="$WRAP"
    export LLMUX_BINARY
    echo "using fake binary: $WRAP"
  else
    echo "python not found; running the no-binary subset only"
  fi
else
  echo "using LLMUX_BINARY=$LLMUX_BINARY"
fi

echo "running LlmuxSmokeTest..."
java -cp "$OUT" to.llmux.LlmuxSmokeTest
