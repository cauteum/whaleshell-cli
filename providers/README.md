# Providers (local copies)

Canonical builtin profiles live in **`cautem-providers/profiles/`**.

This directory keeps copies for older docs/paths; prefer importing from:

```bash
cautem provider profile import ../cautem-providers/profiles/github.yaml
```

`provider.FindBuiltinDir()` resolves `cautem-providers/profiles` first.
