# Documentation Maintenance

This page defines the standard for keeping Omni docs accurate as the product
changes.

## Documentation Stack

| Layer | File | Purpose |
| --- | --- | --- |
| README | `README.md` | Minimal project entry point. |
| Site config | `mkdocs.yml` | Navigation, theme, and metadata. |
| Pages | `docs/*.md` | User, operator, and contributor documentation. |
| Dependencies | `docs/requirements.txt` | MkDocs and theme versions. |
| CI | `.github/workflows/docs.yml` | Strict build, link/structure scan, Pages deploy. |

Docs dependencies are installed with `uv`.

## Page Ownership

| Change type | Pages to review |
| --- | --- |
| New CLI command or flag | [CLI Reference](cli.md), [Command Matrix](command-matrix.md), [Runbooks](runbooks.md) when workflow-shaped. |
| New config key | [Configuration](configuration.md), [Schema Reference](schema-reference.md), [State And Files](state-and-files.md) when stateful. |
| Provider behavior change | [Providers](providers.md), [Tools](tools.md), [Troubleshooting](troubleshooting.md). |
| Dotfile behavior change | [Dotfiles](dotfiles.md), [Safety Model](safety.md), [Runbooks](runbooks.md). |
| TUI interaction change | [TUI](tui.md), [Safety Model](safety.md) when risk changes. |
| Test or contributor workflow change | [Development](development.md), [Test Matrix](test-matrix.md), this page. |
| User-facing terminology change | [Glossary](glossary.md), affected guide pages. |

## Update Checklist

1. Identify the behavior surface: config, CLI, TUI, providers, dotfiles, cache,
   services, or docs tooling.
2. Update the narrative guide first.
3. Update the reference page that makes the behavior precise.
4. Update [Runbooks](runbooks.md) if users need an ordered procedure.
5. Update [Command Matrix](command-matrix.md) if a command was added, removed,
   renamed, or reclassified.
6. Keep README minimal; link to docs instead of copying full docs back into it.
7. Run the docs validation commands below.

## CLI Drift Check

When command code changes, compare Cobra command declarations with the docs:

```sh
rg -n 'Use:|Short:|Flags\\(\\)\\.|PersistentFlags\\(\\)\\.' internal/cli
```

Then check:

- every new command appears in [CLI Reference](cli.md)
- every new command has a risk row in [Command Matrix](command-matrix.md)
- new mutating workflows have a recipe or runbook when the sequence is not obvious
- new flags that change safety semantics are called out near the command

Use help output as the final source for syntax:

```sh
omni <command> --help
```

## Local Build

Build strictly in Docker:

```sh
make docs-build
```

If Docker is not on `PATH`, pass `DOCKER=/path/to/docker`.

For iterative local serving, install `docs/requirements.txt` in your own Python
environment and run:

```sh
mkdocs serve
```

## Link And Structure Scan

CI checks:

- every Markdown page has an H1
- docs do not contain unfinished placeholder markers
- relative Markdown links in `README.md` and `docs/*.md` resolve to files

Run the same logic locally when changing links or navigation:

```sh
python3 - <<'PY'
from pathlib import Path
import re
import sys

root = Path.cwd()
docs = sorted((root / "docs").glob("*.md"))
files = [root / "README.md", *docs]
warnings = []
bad_links = []
placeholder_pattern = re.compile("|".join(["TO" + "DO", "TB" + "D", "FIX" + "ME"]))

for file in files:
    text = file.read_text(encoding="utf-8")
    rel = file.relative_to(root)
    if not text.startswith("# "):
        warnings.append(f"{rel}: missing H1")
    if placeholder_pattern.search(text):
        warnings.append(f"{rel}: placeholder marker")
    for line_no, line in enumerate(text.splitlines(), start=1):
        for match in re.finditer(r"\\[[^\\]]+\\]\\(([^)]+)\\)", line):
            href = match.group(1).split("#", 1)[0]
            if not href or href.startswith(("http", "mailto:")):
                continue
            target = (file.parent / href).resolve()
            if not target.exists():
                bad_links.append(f"{rel}:{line_no} {href}")

if warnings:
    print("\\n".join(warnings))
if bad_links:
    print("\\n".join(bad_links))
if warnings or bad_links:
    sys.exit(1)
print("structure: ok")
print("links: ok")
PY
```

## CI Contract

The Docs workflow must:

- install dependencies with `uv`
- run `mkdocs build --strict`
- run the link and structure scan
- upload the `site/` artifact for GitHub Pages
- deploy only from `main`, never from pull requests

The docs build is the proof that navigation, Markdown syntax, MkDocs config,
and internal links are coherent enough to publish.

## Writing Rules

- Prefer commands users can run over long prose.
- Mark destructive or broad repair paths clearly.
- Explain source-of-truth boundaries before repair commands.
- Keep one concept per page section.
- Use tables for references and decision points.
- Avoid duplicating the same long explanation across pages; cross-link instead.
- Preserve CLI/TUI parity in docs when a capability exists on both surfaces.

## Stop Condition

A docs change is ready when:

- the changed behavior has one narrative entry point
- the precise reference page is updated
- operational procedures are updated when needed
- README stayed short
- strict build passes
- link/structure scan passes
