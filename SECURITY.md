# Security

`contextd` holds context you write and, in server mode, mediates access to it for a team. A bug in it can expose files, tokens or an audit trail. Reports are welcome and taken seriously.

## Reporting a vulnerability

**Report privately**, through GitHub's advisory form:

**https://github.com/orkcom-tech/contextverse/security/advisories/new**

That opens a private thread with the maintainers. Please do not open a public issue, and please do not post proof-of-concept code publicly before a fix is available.

Include what you would want if you were fixing it: the version (`contextd version`), the mode (solo, client or server), what an attacker can reach, and the smallest reproduction you have.

## What to expect

This is a small project, so the honest version rather than a service-level promise: you will get a human reply, and if the report is valid you will be credited in the advisory and the changelog unless you would rather not be.

## Scope

In scope — anything that lets someone read or change context they should not, escape a path ACL, forge or break the audit chain, extract a token, drive the local console from another origin, or make a client trust a server it should not.

Out of scope — findings that require an attacker who already has your shell or your filesystem. `contextd` protects a space from the network and from other users; it does not defend against someone who is already you on your own machine.

Two known and deliberate properties, so you need not report them:

- The local web console (`contextd ui`) binds to loopback and grants full read/write to the space it serves. That is what it is for, and why it is off by default and prints its own warning.
- A model handed your context can ignore it. `contextd` guarantees delivery, not obedience.
