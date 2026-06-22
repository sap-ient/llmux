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
