# Upstream workflows, switched off in this fork

These workflows come from the upstream OpenOMS project. They publish release
images, run scheduled scans and call a Claude GitHub app, all of which need
secrets and settings that this fork does not have, so they would fail on every
push. They are kept here unchanged; move a file back to `.github/workflows/`
to switch it on again.

`ci.yml` (build, lint, tests) and `e2e.yml` (manual) remain active.
