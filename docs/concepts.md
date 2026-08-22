# Concepts

Omni has one durable configuration for tools and dotfiles. Agent packages and
runtime state are delegated to APM.

| Surface | Owner | Durable state |
| --- | --- | --- |
| Tools | Omni/providers | `settings.json`, provider state |
| Dotfiles | Omni | `settings.json`, dotfiles repository |
| Agent packages, MCP, plugins, marketplaces | APM | `~/.apm/apm.yml`, `apm.lock.yaml`, `marketplaces.json` |

`omni agents sync` is the integration boundary. It directly invokes APM in the
global workspace; it does not render agent state or maintain a parallel agent
store or per-agent assignment model.

## Hosts

Hosts select Omni settings and tool configuration. APM agent targets are
resolved by APM and are not stored as Omni host assignments.

## Groups

Groups organize Omni tools and dotfiles. They do not assign agent resources.

## The three stores

Omni settings, provider state, and APM state are separate; APM is the only
store for agent resources.
