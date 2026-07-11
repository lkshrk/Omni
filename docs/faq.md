# FAQ

## Why does Omni store provider lists instead of one provider?

Package names are not portable across package managers. Omni stores the
concrete provider candidates it has actually learned for a logical tool, in
priority order.

The first provider is the normal install target. Additional providers are
high-confidence alternatives from search/import metadata.

## How do I prefer one provider over another?

Set provider priority globally or for one host:

```json
{
  "host_settings": {
    "server": {
      "provider_priority": ["apt", "brew", "npm", "pip"]
    }
  }
}
```

## Why does a tool show a different installed provider?

The configured provider is desired state. The installed provider in cache is
observed ownership from the machine. They can differ after manual package work,
provider migration, or fallback install.

Refresh first, then use switch/reinstall actions to repair drift.

## Why does Omni need a host before most tool commands?

Tool desired state depends on the active host. Omni must know which reusable
groups and host settings apply before it can decide which tools should exist.

Run:

```sh
omni bootstrap
omni hosts ensure "$(hostname -s)"
```

## Can I share one `settings.json` across machines?

Yes. Keep portable defaults in `settings`, reusable tool sets in `groups`, and
machine-specific choices in `host_settings` and the protected host group.

For one machine's local-only tool, place it in that host group. For a tool every
developer machine should have, place it in a reusable group and assign that
group to each host.

## Can I delete the cache?

Yes. The cache is derived local state:

```sh
omni settings reset-cache
omni tools refresh
```

Do not treat cache deletion as a config reset. It does not remove
`settings.json` or the dotfiles repo.

## Does `tools import` install anything?

No. Import scans installed tools and writes config. Run refresh or sync
afterward when you want local observed state updated:

```sh
omni tools import
omni tools refresh
omni tools sync
```

## What is the difference between `tools add` and `tools set`?

`tools set` creates or updates only the logical tool spec:

```sh
omni tools set ripgrep --provider brew
```

`tools add` creates the spec and assigns it to a group in one flow:

```sh
omni tools add ripgrep --provider brew --group dev
```

In noninteractive scripts, pass `--group` to `tools add` so Omni does not need
to prompt for an assignment target.

## What is the difference between `tools install` and `tools sync`?

`tools install <tool>` installs one named tool now. `tools sync` applies the
configured desired state for the active host or a chosen group, so it can
install several missing tools and optionally prune with `--prune`.

## What is the difference between `sync` and `reconcile`?

`tools sync` installs missing configured tools. `reconcile` is broader: it can
sync tools, claim discovered tools, upgrade tools, repair dotfile links, and
commit dotfile repo changes.

Use `doctor` first when you only want diagnosis.

## Does `auto_import` control `reconcile`?

No. `settings.auto_import` defaults to `false` and affects scoped plain sync
paths. `omni tools sync --all` and `omni reconcile` are explicit broad commands:
they claim discovered installed tools into the current machine group as part of
their normal behavior.

## Why does dotfile sync require GNU Stow?

Stow gives Omni a predictable package-directory contract. The dotfiles repo
contains packages; each package maps to one or more links in the home directory.
That is safer and more auditable than modeling arbitrary symlinks.

## What should I do when dotfile sync reports a conflict?

Inspect first:

```sh
omni dots status <name>
```

Then pick the source of truth:

```sh
omni dots resolve <name> --use-repo
omni dots resolve <name> --use-local
```

`--use-repo` replaces the local target with the repo version. `--use-local`
copies the local target into the repo.

## Why did a Python library get imported as ignored?

Some provider scans can distinguish CLI packages from pure libraries. Omni is a
tool manager, so non-CLI packages may be kept visible but ignored instead of
being treated as managed command-line tools. Discovery scans also skip non-CLI
pip packages, so libraries such as `asyncpg` usually appear only as suppressible
orphans until you ignore them from the Tools tab or add them to `ignore.tools`.

## How do I make CI or tests deterministic?

Use explicit paths and hostname:

```sh
OMNI_HOSTNAME=testhost \
  omni --config "$PWD/settings.json" \
       --cache-dir "$PWD/.omni-cache" \
       hosts ensure testhost
```

Avoid integration tests against a real home directory or real package-manager
state. Use the project integration target documented in
[Development](development.md).
