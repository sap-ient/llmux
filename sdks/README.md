# llmux language packages

Thin, native packages that let you use llmux **locally** in any language — no
server to run. They bundle the gateway binary and manage it for you.

The architecture is deliberate: one Go binary is **both** the hosted server and
the locally-embedded sidecar. The packages here are tiny wrappers (~one file
each) that start the binary on a local port and hand you a `base_url` for your
existing OpenAI client. Streaming works natively in every language because each
just reads its own local socket — no FFI, no per-language stream glue.

| Package | Mechanism | Streaming |
|---------|-----------|-----------|
| **python** | spawns the bundled binary on `127.0.0.1:<port>` | native (your OpenAI client) |
| **node** | spawns the bundled binary on `127.0.0.1:<port>` | native |
| **go** | runs the gateway **in-process** (imports `core/`) | native |
| **ruby** | spawns the bundled binary on `127.0.0.1:<port>` | native |
| **php** | spawns the bundled binary on `127.0.0.1:<port>` | native |
| **rust** | spawns the bundled binary on `127.0.0.1:<port>` | native |
| **java** | spawns the bundled binary on `127.0.0.1:<port>` | native |
| **dotnet** | spawns the bundled binary on `127.0.0.1:<port>` | native |
| **elixir** | spawns the bundled binary on `127.0.0.1:<port>` | native |

Every spawning package follows the same contract: resolve the binary
(`LLMUX_BINARY` → bundled `bin/llmux` → `llmux` on `PATH`), pick a free
`127.0.0.1` port, launch the binary with `LLMUX_ADDR=127.0.0.1:<port>`
(inheriting the environment so provider keys pass through), poll `/health` until
ready, then expose `base_url()` and `openai_base_url()` (→ `…/v1`, default API
key `"llmux-local"`). Start is lazy, singleton, concurrency-safe, and the child
is terminated at process exit. Where a popular OpenAI SDK exists, an OPTIONAL
convenience constructor returns a client already pointed at the gateway
(Ruby → `ruby-openai`, PHP → `openai-php/client`, Rust → `async-openai` behind a
feature, Java → `openai-java`, .NET → the official `OpenAI` nuget).

## Binary distribution

For local development, run `make sdk-bins` to build the binary into each
package's `bin/` directory (`priv/bin/` for Elixir). The `bin/` payloads are
gitignored — only the wrapper source is committed. Real releases produce
per-OS/arch binaries in CI and ship them inside the package artifacts:

| Package | Ships the binary via |
|---------|----------------------|
| python | platform wheels (`llmux/bin/llmux`) |
| node | npm `optionalDependencies` (`bin/llmux`) |
| go | n/a — embeds the gateway in-process |
| ruby | platform gems (`bin/llmux`) |
| php | composer package / release archive (`bin/llmux`) |
| rust | `bin/llmux` next to `Cargo.toml` (or a build/install step) |
| java | jar-sibling `bin/` or `LLMUX_HOME/bin/llmux` |
| dotnet | nuget `contentFiles` (`bin/llmux`) |
| elixir | `priv/bin/llmux` packaged in the hex archive |

Override the binary path anytime with `LLMUX_BINARY=/path/to/llmux`.

## Testing

Every package has a real test suite covering the sidecar contract: binary
resolution (`LLMUX_BINARY` → bundled → PATH → clear error), URL formatting
(`openai_base_url() == base_url() + "/v1"`), health-poll readiness (200) and
timeout (never-200 / unreachable), lazy singleton (no double-spawn), cleanup
(child terminated / port freed), plus an integration test gated on the real
binary. The non-integration tests drive a **fake fixture** — a tiny HTTP server
that honors `LLMUX_ADDR` and serves `/health` — so they need no real gateway and
no network beyond localhost.

Run everything available with `make sdk-test` from the repo root (it builds the
real binary once into `/tmp`, exports `LLMUX_BINARY`, and skips toolchains that
aren't installed). Per language:

| Package | Framework | How to run |
|---------|-----------|------------|
| python | stdlib `unittest` | `cd sdks/python && python3 -m unittest discover -s tests` |
| node | built-in `node --test` | `cd sdks/node && node --test` |
| go | stdlib `testing` | `go test ./sdks/go/...` |
| ruby | stdlib `minitest` | `cd sdks/ruby && ruby -Ilib -Itest test/test_llmux.rb` |
| rust | `cargo test` | `cd sdks/rust && cargo test` |
| java | JUnit 5 (CI) + a dependency-free runnable check | `cd sdks/java && mvn test` · or `sh run-java-check.sh` |
| php | PHPUnit | `cd sdks/php && composer install && vendor/bin/phpunit` |
| dotnet | xUnit | `cd sdks/dotnet && dotnet test tests/Llmux.Tests.csproj` |
| elixir | ExUnit | `cd sdks/elixir && mix test` |

Notes:
- **Integration tests** auto-skip when no real binary is resolvable. To force
  them, set `LLMUX_BINARY` (python/node/ruby/rust/java) or `LLMUX_BINARY_REAL`
  (php/dotnet/elixir, so it doesn't collide with the fake-fixture override) to a
  built gateway: `GOFLAGS=-mod=mod GOPROXY=off go build -o /tmp/llmux-bin ./cmd/llmux`.
- **java** has no committed JUnit jars; the always-runnable check is plain
  `javac`/`java` via `run-java-check.sh` (used by `make sdk-test`), while the
  JUnit suite (`src/test/java/.../LlmuxTest.java`, wired in `pom.xml`) runs under
  Maven in CI.
- **php / dotnet / elixir** fixture tests shell out to `python3` (or `python`)
  for the fake `/health` server and skip gracefully if it is absent.

## Provider keys

All packages inherit provider API keys from the environment
(`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, …), so the embedded
gateway auto-detects providers exactly like the standalone binary.

## Proven

Each package has been run end-to-end making a real chat completion through llmux:
Python sidecar, Node sidecar, and Go in-process — all from this one Go codebase.
The Ruby, PHP, Rust, Java, .NET, and Elixir wrappers implement the identical
sidecar contract (binary resolution, free-port, spawn with `LLMUX_ADDR`, health
poll, lazy singleton, exit cleanup).
