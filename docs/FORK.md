# Fork configuration, PWA, and releases

This fork extends SignTools 3.1.0. Existing `builder`, `basic_auth`, signing profiles, environment variables, storage, builder protocols, and configuration files continue to work. The optional `extra_conf` section contains fork settings. Leaving it out keeps the existing authentication and builder behavior.

## Optional configuration

Append this section to an existing `signer-cfg.yml`; it is not a replacement for the complete upstream configuration in [INSTALL.md](../INSTALL.md).

```yaml
extra_conf:
  # Use a browser login form with the existing basic_auth credentials.
  # HTTP Basic authentication remains available to clients and scripts.
  login_page: false

  # Private server secret. A random 128-character value is saved if absent.
  # Keep the generated value when migrating accounts/models to another server.
  # enc_key: YOUR_128_CHARACTER_SECRET

  builder_aliases:
    GitHub: GitHub Actions
    Semaphore: Semaphore CI
    SelfHosted: Local Mac
    backup-mac: Backup Mac

  # Builder/profile IDs or names. Browser preferences override these defaults.
  default_builder: GitHub
  default_profile: Personal

  # Additional builders have unique IDs and independent tokens/keys.
  builders:
    - id: backup-github
      name: Backup GitHub
      type: github
      github:
        org_name: YOUR_GITHUB_OWNER
        repo_name: SignTools-CI
        workflow_file_name: sign.yml
        token: YOUR_ADDITIONAL_GITHUB_TOKEN
        ref: master
    - id: backup-mac
      name: Backup Mac
      type: selfhosted
      selfhosted:
        url: http://192.168.1.134:8090
        key: YOUR_BUILDER_KEY

  # These defaults appear alongside repositories saved in this browser.
  repositories:
    - name: Example source
      url: https://example.com/source.json

  webpush:
    enable: false
    # Contact information included in VAPID authorization.
    subject: mailto:admin@example.com
    # Optional: provide a matching VAPID key pair, or let the server create one.
    # public_key: YOUR_VAPID_PUBLIC_KEY
    # private_key: YOUR_VAPID_PRIVATE_KEY
```

`type` accepts `github`, `selfhosted`, or `semaphore`. A Semaphore entry uses a `semaphore` object with the same `org_name`, `project_name`, `token`, `ref`, and `secret_name` fields as the existing `builder.semaphore` section. Additional builders are enabled by default; set an entry's `enable: false` to keep it disabled. Legacy IDs remain `GitHub`, `Semaphore`, and `SelfHosted`, regardless of their display aliases.

Keep enabled legacy builders when using an existing configuration. Extra builders also work on their own. Each configured builder must be reachable by the server and able to obtain jobs using the existing SignTools builder protocol.

## Browser settings and credentials

The web interface can add GitHub, Semaphore, and self-hosted builders without editing YAML. Browser-created builders are stored server-side in `save_dir/extra-builders.yml` with owner-only file permissions; the API returns display metadata, never tokens or builder keys. Builders defined in the server's configuration are managed through that configuration. Browser-created builders can be removed in the interface.

Signing profiles can be uploaded as a `.p12` certificate, its password, and either a `.mobileprovision` file or developer-account credentials. Profiles use the existing SignTools storage layout and validation. Profiles supplied by environment variables and profiles still referenced by apps cannot be removed through the browser.

Preferred builder and profile IDs, theme, and external source URLs are stored in this browser's `localStorage`. Builder tokens, passwords, certificates, and server private keys are not stored there. App Store sessions and decryption models use an encrypted browser vault with a separate browser key; see [accounts, models, and backups](STORE.md). Browser preferences take priority over server defaults, and missing choices fall back to available entries.

## Finding apps and using external sources

The app library groups entries by the original IPA's bundle identifier, then by version. Builds made with different profiles remain visible together even when signing changes their bundle identifiers. Search and status filters help locate a particular app or signing attempt.

The Sources screen supports AltStore-compatible JSON sources, including modern `versions` arrays and legacy entries with `version` and `downloadURL`. Server defaults and sources saved in this browser appear together. Add and remove sources in the interface, browse an app's versions, and choose an IPA to sign using the existing builder and profile flow.

Repository metadata is fetched by the server, so sources do not need browser CORS support. Repository sources and imported IPAs require public HTTPS URLs on port 443. Internal network destinations are rejected, including after redirects. The separate manual IPA URL field retains its existing HTTP/HTTPS behavior for private self-hosted downloads. Adding a browser source does not publish it to other browsers or rewrite server configuration.

Installation opens inside the interface. The installation page presents an explicit **Install** action that opens iOS's `itms-services` prompt. A separate page is available for browsers that require a new window. iOS still controls the final installation confirmation.

Builder activity is collapsed initially. Self-hosted logs are proxied by the server using its stored builder key. GitHub activity is read using the server's stored token; shared workflow activity is labelled accordingly and may include another job when multiple signing requests share a workflow. The UI never claims that an unrelated workflow run belongs to a specific app.

## iOS home-screen installation and Web Push

1. Serve SignTools over HTTPS with a stable public `server_url` and persistent `save_dir`.
2. On iOS or iPadOS 16.4 or newer, open SignTools in Safari, choose **Share → Add to Home Screen**, and launch the new icon.
3. Enable `extra_conf.webpush.enable` on the server. Set `subject` to your contact URL or `mailto:` address.
4. In the installed app, tap the notification action and grant permission. iOS requires a user gesture; simply opening a web page cannot grant permission automatically.
5. Start signing. The installed app subscribes to completion for selected apps. A notification opens the signed app's same-origin installation page, where the user confirms installation.

The server can generate and persist VAPID keys when a pair is not configured. The generated key pair and subscriptions are saved in `save_dir/push/state.json` with owner-only permissions. Keep `save_dir` across restarts and deployments so existing subscriptions remain valid. Expired subscriptions are removed. Push delivery requires outbound HTTPS access to the browser's push service; the notification is delivered by that service and can be delayed by the device.

The service worker provides installability, an offline explanation, and notification handling. It does not cache authenticated pages, IPAs, certificates, or credentials. Signing, browsing, and installation require a connection to the server.

## Login page

Set both `basic_auth.enable: true` and `extra_conf.login_page: true` to use a login page with the same configured username and password. The server issues an HttpOnly, SameSite session cookie after a successful login, marked Secure when served over HTTPS. Sessions expire after seven days and are revoked by logout, a server restart, or a credential change. API clients can continue sending HTTP Basic authorization. Without `login_page`, the existing browser HTTP Basic challenge remains in use. If Basic authentication is disabled, enabling the login-page setting does not invent a separate account system.

## Stable fork releases

Fork releases follow `v<upstream-version>-<patch>`:

- First release based on upstream 3.1.0: `v3.1.0-1`.
- Next release based on the same version: `v3.1.0-2`.
- After updating `UPSTREAM_VERSION` to `3.2.0`: `v3.2.0-1`.

The suffix is a stable fork patch even though generic semantic-version parsers describe a hyphen suffix as a prerelease. GitHub releases are explicitly published with `prerelease: false` and marked latest. Container tags include the complete version, such as `ghcr.io/xuniv/signtools:3.1.0-1`, and `latest` is promoted after the GitHub release succeeds.

To publish, open **Actions → Release → Run workflow** on `master`, or run:

```sh
gh workflow run release.yml --repo xuniv/SignTools --ref master
```

The workflow verifies formatting, TypeScript, frontend tests, the embedded web build, Go tests, and release-version tests. Container builds also verify that the native image starts successfully with its runtime libraries. It then builds binaries with `checksums.txt` and a multi-platform GHCR container before creating the GitHub release. All publication uses the repository's `GITHUB_TOKEN`; DockerHub credentials are unnecessary. If GHCR package visibility is private on its first publication, its owner can change it to public in the package settings.

The release workflow is manual and serialized. It does not create local-author commits, add coauthor trailers, or open automated release PRs. Existing commits keep their original signatures and attribution. New changes should be committed through a GitHub server-signed path when GitHub's default signature is required. Binary builds omit local paths and VCS build metadata.

To preview the next version without publishing:

```sh
git fetch --tags
python3 scripts/release-version.py
python3 scripts/release-version_test.py
```

A normal push runs validation without creating a release. GitHub releases, not an edited local version file, are the source of truth for the incremental patch counter.

## Building locally

Use Go 1.27.4 and Bun 1.4.4 (matching the Dockerfile and CI). Clone with `--recurse-submodules`, or initialize the pinned upstream sources, then run:

```sh
git submodule update --init --recursive
bun install --frozen-lockfile --ignore-scripts
bun run fmt:check
bun run check
bun test src/web
bun run build
go test ./...
go build -trimpath -buildvcs=false -o SignTools .
```

The [App Store/model guide](STORE.md) describes runtime platform support, writable caches, and preserving `enc_key` during server migrations.

The frontend is bundled locally into the Go binary. No UI scripts, stylesheets, or fonts are fetched from third-party CDNs at runtime. Upstream AGPL licensing and existing attribution remain in place.
