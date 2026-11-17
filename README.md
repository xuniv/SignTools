<p align="center">
    <img width="256" heigth="256" src="img/logo.png">
    <h1 align="center">SignTools</h1>
    <p align="center">
        A free, self-hosted platform to sign and install iOS apps without a computer
    </p>
    <p align="center">
        <img alt="GitHub" src="https://img.shields.io/github/license/xuniv/SignTools">
        <img alt="GitHub issues" src="https://img.shields.io/github/issues/xuniv/SignTools">
        <img alt="GitHub all releases" src="https://img.shields.io/github/downloads/xuniv/SignTools/total">
        <img alt="GitHub release (latest SemVer)" src="https://img.shields.io/github/v/release/xuniv/SignTools">
    </p>
</p>

## This fork

A PWA interface built with shadcn/ui, grouped app and version browsing, an IPA library, external AltStore sources, browser-managed signing profiles and builders, and optional Web Push completion notifications. Pinned upstream ipatool and unfairplay integrations add account-based App Store downloads and model-based IPA decryption with an encrypted browser vault and passphrase-protected backups. Existing SignTools configuration and builder protocols remain compatible; fork settings live under optional `extra_conf`.

See [fork setup, configuration, and release instructions](docs/FORK.md). Download [stable fork releases](https://github.com/xuniv/SignTools/releases) or use `ghcr.io/xuniv/signtools:latest`. Versions start at `3.1.0-1` and increment the suffix for each fork patch.

See [IPA library, App Store accounts, models, and encrypted backups](docs/STORE.md) for operation and platform details. Source builds require `git clone --recurse-submodules` or `git submodule update --init --recursive`.

## Introduction

SignTools is a sideloading platform that takes a different approach from any similar tools. It consists of two components — a **service** and a **builder**. The builder is a macOS machine which performs signing using official Apple software. Doing so means high reliability and compatibility. The service (this repo) can be hosted anywhere, and it provides a web interface for you to upload, sign, and download apps, using the builder where necessary. Having the web service means that you don't need anything installed on your phone, and you can still sideload without a computer.

## Disclaimer

This project is self-hosted and does not constitute a public service. It does not offer any alternative catalog of applications, nor does it endorse or support any form of piracy. The sole purpose of this project is to enable users to use homebrew apps or tweaks on their devices.

By using this project, you acknowledge and agree that the developers and collaborators cannot be held responsible for any damages, losses, or consequences incurred as a result of your use of this project. Please exercise caution and ensure that you comply with all applicable laws and regulations when using this project.

## Features

- No jailbreak required
- iOS, iPadOS, macOS (native + IPA) supported
- No computer required after an initial setup
- Responsive shadcn/ui interface with dark mode and iOS home-screen installation
- Apps grouped by bundle identifier and version, with search and status filters
- Optional signing-completion Web Push notifications for installed PWAs
- Server and browser-managed AltStore-compatible app sources
- Browser-uploaded certificates and profiles, additional builders, and saved preferences
- Inspect IPA metadata and encryption state before signing, and retain original and processed files in a reusable library
- Search App Store catalogs, browse purchased apps and specific versions, and download using multiple country-aware accounts
- Decrypt matching IPAs with user-provided upstream models on supported hosts
- Encrypt saved account/model records and export passphrase-protected browser backups
- Install an already signed IPA directly without creating a signing job
- Upload unsigned apps, download signed apps
- Inject tweaks as you are signing apps
- Save tweaks to a library and reuse them across apps
- Install signed apps from the website straight to your iOS device via [OTA](https://medium.com/@adrianstanecki/distributing-and-installing-non-market-ipa-application-over-the-air-ota-2e65f5ea4a46)
- Provisioning profiles and developer accounts supported
- Configurable signing including entitlements
- Choose from multiple signing profiles for each app

## [Installation](INSTALL.md)

## [Frequently Asked Questions (FAQ)](FAQ.md)

## License

This project and all of its unlicensed dependencies under the [SignTools](https://github.com/SignTools) organization are licensed under AGPL-3.0. A copy of the license can be found [here](LICENSE). Raise an issue if you are interested in exclusive licensing.
