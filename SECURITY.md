# Security Policy

## Supported versions

api-kit is released as `v0.x`. Security fixes go into the latest minor
release only; please upgrade to it before reporting.

## Reporting a vulnerability

If you have discovered a vulnerability, please reach out privately via email
to opensource@mittwald.de instead of opening a public issue. Include the
affected package, version, and a description or proof of concept. We will
acknowledge your report and keep you informed about the fix.

Packages that handle credentials (`auth`), send requests built from
templates (`engine`, `expr`) or read definitions from files (`definition`)
are especially sensitive; reports about them are very welcome.
