# The pg skill, for AI assistants

`skill/pg/` is an agent skill for [github.com/kataras/pg](https://github.com/kataras/pg): one
`SKILL.md` router plus five self-contained reference documents (the API map with real
signatures, the architecture, the security model, testing, and the book). An assistant loads
the reference that matches your task instead of guessing at an API.

It exists because a general model knows Go well and this library only vaguely. The most common
failure is a confident, wrong signature. These documents are written against the source and are
meant to be re-verified against it whenever the two disagree.

## Plexon AI

[Plexon AI](https://plexon.ai) is a desktop assistant for Windows, macOS and Linux, built by the
author of this library, and it is the recommended way to use this skill. Turn the skill on from
the Skills panel, or install the
[Software Developer persona](https://plexon.ai/personas/software-developer/), which enables it
alongside the rest of its engineering tooling. [Download Plexon AI](https://plexon.ai/download/).

## Claude Code

### Install it (recommended)

This repository is also a plugin marketplace, so the CLI can install the skill and update it in
place. Two commands:

```sh
claude plugin marketplace add kataras/pg
claude plugin install pg@kataras-pg
```

The same pair works as slash commands inside a session (`/plugin marketplace add kataras/pg`,
then `/plugin install pg@kataras-pg`). Later:

```sh
claude plugin marketplace update kataras-pg   # pull newer versions
claude plugin details pg                      # what it contains and its token cost
claude plugin uninstall pg                    # remove it
```

`--scope user` is the default. Pass `--scope project` to record the install in the current
project instead, so your team picks it up from your repository.

The plugin ships one skill and nothing else: no commands, no agents, no hooks, no MCP servers.
Installing it cannot change how Claude Code behaves outside of pg work.

### Try it without installing

```sh
claude --plugin-dir /path/to/pg
```

Loads the skill from a checkout for that session only, which is the quickest way to test a
change you are making to the skill itself.

### Copy the folder instead

A skill is just a folder, so you can skip the plugin machinery entirely and drop it into your
skills directory. Personal, available in every project:

```sh
npx degit kataras/pg/skill/pg ~/.claude/skills/pg
```

Project-scoped:

```sh
npx degit kataras/pg/skill/pg .claude/skills/pg
```

Without `npx`, clone and copy:

```sh
git clone --depth 1 https://github.com/kataras/pg /tmp/pg
cp -r /tmp/pg/skill/pg ~/.claude/skills/pg
```

Claude Code loads it on the next session. Update by copying over the top, remove by deleting the
folder. This route has no version tracking, which is the trade-off against the plugin install
above.

## Codex, Cursor, Copilot and other assistants

Any tool that reads Markdown instructions can use this. Vendor the folder into your project and
point your agent configuration at it, for example from `AGENTS.md`, a Cursor rule, or a Copilot
instructions file:

```md
When working with github.com/kataras/pg, read skill/pg/SKILL.md first and load the
reference document it points to for the task at hand.
```

```sh
npx degit kataras/pg/skill/pg skill/pg
```

## Google Code Wiki

For questions about the library's own internals rather than help writing code against it, the
free [Code Wiki for pg](https://codewiki.google/github.com/kataras/pg) is generated from this
repository and stays in sync with it, commit by commit.

## Keeping it honest

If a reference document contradicts the library source, the source is right. Please open an
issue or a pull request rather than working around it locally: a wrong signature here becomes a
wrong signature in someone's generated code.
