# Security policy

Acnologia holds API keys for other people, so security reports get priority.

## Reporting a vulnerability

Please report privately through GitHub:
[Report a vulnerability](https://github.com/sidharthpunathil/acnologia/security/advisories/new)
(Security tab, then Report a vulnerability).

Do not open a public issue, pull request or discussion for a vulnerability.

Include what you can of:

- the affected version or commit,
- which principal can do what (for example an app token holder, an admin, a
  peer server, a person with a claim link, an anonymous visitor),
- steps to reproduce against a local instance with dummy keys,
- the impact you observed.

You will get an acknowledgement within a few days. Once a fix is released we
credit reporters who want to be credited.

## Scope

In scope: the server in `server/`, the dashboard in `web/`, the `acn` CLI,
the install scripts, the SDKs in `sdk/` and the release workflows.

Please test only against an instance you run yourself, with keys you own.
Never test against someone else's deployment or use real third party
credentials that are not yours.

## How Acnologia protects keys

The [security model](docs/security.md) describes the roles, hidden keys, what
each kind of share can see, what the AI model can see, the access password
and the known limits. Reading it first helps you tell an intended behaviour
from a bug.
