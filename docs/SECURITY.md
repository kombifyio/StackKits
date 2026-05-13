# Security

StackKits is designed around safe defaults:

- generated deployment artifacts must not contain committed secrets,
- public services should be explicit and authenticated by default,
- local-only services should stay local-only,
- examples must use placeholders such as `<token>` or `secret://path`.

Report security issues through GitHub Security Advisories on the public
repository.
