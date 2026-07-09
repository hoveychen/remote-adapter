# Embedded native artifacts

`make native` copies the platform interceptor here before `make go` builds
`rca`, so the single binary carries it:

- macOS: `rcc_interpose.dylib` (DYLD interpose dylib, from `native/macos`)
- Linux: `rcc_seccomp` (seccomp-user-notify supervisor, from `native/linux`)

At runtime `rca` extracts the artifact to the user cache dir and injects it.
A plain `go build ./cmd/rca` without `make native` still compiles; run mode
then needs `--dylib` / `--supervisor` pointing at an external build.

Everything in this directory except this file is gitignored.
