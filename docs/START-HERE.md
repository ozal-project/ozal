# Start here

Ozal publishes your private applications from a server you control and decides
who gets in. This page tells you what to read, in what order, depending on what
you want to do.

## I want to try it

1. [README](../README.md): what Ozal is, and a local trial in one command.
2. [Quickstart](../QUICKSTART.md): a minimal routing example with no TLS and no sign-in provider.
3. [Publish your first application](operations/first-application.md): a private console, your first administrator, a protected address for someone else.

## I want to run it for real

1. [Install](operations/download-install.md): verify the download and run Ozal as a Linux service.
2. [Publish your first application](operations/first-application.md): sign-in, people and groups, and publishing an app or site.
3. [Point a domain at Ozal](operations/first-application.md#5-point-a-domain-at-ozal): public HTTPS and DNS.
4. [Administer](operations/administration.md): upgrades, backups and recovery, connectors, and publishing from CI.
5. [Security](security.md): what Ozal protects, what it does not, and what you must protect yourself.

Keep [configuration and CLI](reference.md) open while you work; it is the place for
exact fields and commands.

## I want to contribute

1. [Design](architecture/design.md): the pattern every package follows.
2. [Stack](architecture/stack.md): the dependency decisions and why.
3. [Engineering guide](engineering.md): the contracts, and how to verify a change.
4. [Identity](architecture/identity-login.md) and [host sessions](architecture/host-sessions.md): how sign-in and sessions work.
5. [AGENTS.md](../AGENTS.md): the working rules for changes made to this repository.

## I want to know where it is going

1. [VISION.md](../VISION.md): the product direction, what exists today, and what is only a direction.
2. [Release notes](https://github.com/ozal-project/ozal/releases): what each release changed. The [alpha.1 notes](releases/v0.1.0-alpha.1.md) record the first tagged build and its known limits.

## What is public and what is not

This repository publishes the product, its guides and its contracts. Work that is
scheduled, owner decisions, acceptance records and detailed plans are kept by the
maintainer and are not in this repository. The [work status](TO_IMPLEMENT.md) note
explains how to propose work. The [documentation index](README.md) groups the
guides by task.
