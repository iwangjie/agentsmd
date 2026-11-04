# Security

## Reporting a vulnerability

Report security issues privately through
[GitHub's private vulnerability reporting](https://github.com/iwangjie/agentsmd/security/advisories/new)
rather than in a public issue. Please include the version, macOS version and
the steps to reproduce.

Expect a first reply within a week.

## Threat model

AgentsMD edits a local file and talks to a local model gateway. It stores no
credentials, has no telemetry and makes no network requests except to the
gateway configured in Settings.

Two inputs are treated as untrusted:

- **The prompt file.** It may be edited by other tools, and it is rendered in a
  webview. The Markdown renderer runs with raw HTML disabled and escapes every
  character it does not generate itself.
- **Model output.** It is written to disk and rendered, so it goes through the
  same escaping path as the author's text. A model answer cannot inject markup
  into the UI.

The gateway API key is read from `MAGPIE_API_KEY` and sent only to the
configured base URL.
