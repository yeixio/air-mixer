# Security policy

## Supported versions

Only the latest release and the `master` branch receive security fixes.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through
[GitHub security advisories](https://github.com/yeixio/air-mixer/security/advisories/new).
Include what you found, how to reproduce it, and the impact you expect. We
aim to acknowledge reports within a week.

## Things to know

- The web UI and REST API have **no authentication** yet. Bind
  `server.host` to `127.0.0.1` or a trusted network, and do not expose port
  9001 to the internet. Authentication is on the [roadmap](ROADMAP.md).
- The RSA private key in `internal/protocols/airplay/keys.go` is the AirPort
  Express key published in 2011. Every third-party AirPlay receiver uses
  it; it is public by design and not a leaked secret.
