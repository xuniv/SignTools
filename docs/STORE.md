# IPA library, App Store accounts, and model-based decryption

Version `3.1.0-2` adds an IPA library, App Store account tools, and model-based decryption to the existing signing interface. The source integrations use real, pinned submodules:

| Integration            | Source                                                    | Pinned revision                            |
| ---------------------- | --------------------------------------------------------- | ------------------------------------------ |
| App Store operations   | [majd/ipatool](https://github.com/majd/ipatool)           | `b352566155a667d8aaeabaa7ecec4966080f92ab` |
| Model-based decryption | [DrWhax/unfairplay](https://github.com/DrWhax/unfairplay) | `d7503082a59e3dcfdd3ea7a70434dcecaaaf07df` |

Both submodules follow upstream `main` for deliberate maintainer updates. Normal builds use the exact recorded gitlink revisions. They do not download a newer branch during a release.

## Import and inspect IPAs

The service can run without an enabled builder or signing profile for library and install-only use. Configure a builder and profile when you want to sign an app.

An imported IPA appears in the library independently of a signing job. Upload an IPA, inspect its bundle identifier, display name, version, build version, platform, and encryption state, then choose whether to sign, decrypt, or install it as it is. XML and binary `Info.plist` metadata are supported. The library keeps source files and resulting files as separate artifacts, so the original download remains available.

Encryption state comes from the executable's Mach-O encryption commands. **Encrypted**, **decrypted**, **unencrypted**, and **unknown** are distinct states; a missing or unreadable executable is not proof of decryption. Signing changes do not automatically remove FairPlay encryption.

**Install only** preserves the IPA's existing signature and packages it for the normal iOS installation prompt without selecting a builder or signing profile. The device still checks the package's signature, provisioning, and any App Store license. Hosting an IPA or opening an installation URL does not supply a missing license or decrypt an encrypted executable.

## App Store accounts and country selection

The App Store screen supports multiple saved accounts. Sign in with the account's email and password, and provide an Apple two-factor code when requested. Account state is kept in the encrypted browser vault rather than an operating-system keychain or a server-side account database.

Select an account and country/storefront before browsing. Country selection controls regional search and availability; it does not change the Apple account's billing country or grant access to another country's purchases.

Available operations include:

- Search by app name or bundle identifier.
- List apps already purchased or acquired by the selected account.
- Explicitly acquire a free app license. Paid apps must already have been purchased through Apple; the upstream purchase operation does not buy paid apps.
- List external version identifiers and retrieve human-readable version metadata and release dates where Apple supplies them.
- Download the latest version or a particular external version identifier.
- Upgrade or downgrade by choosing another version of the same app, keeping each downloaded artifact in the library.

Downloads and decryption run as background tasks with progress, expandable logs, cancellation, and a resulting library artifact. Apple controls which versions are still available and whether an account can download them. Authentication, region, ownership, expired sessions, unavailable versions, and upstream errors remain visible instead of being replaced by an apparent successful result.

On supported hosts, the first App Store operation may download the pinned upstream Apple framework assets and a checksum-verified Unicorn runtime. This is separate from account storage. The runtime needs outbound HTTPS and a writable user cache. Container images set `XDG_CACHE_HOME=/data/cache`, so mounting `/data` also preserves these public caches. Configure `XDG_CACHE_HOME` explicitly if using a different volume or a read-only container filesystem.

## Models and decryption

Add a model exported for your own matching enrollment/account. Supported models contain the upstream v2/v3 selector information (`FPSENR2` or `FPSENR3`). The UI displays sanitized model information and keeps the model in the encrypted browser vault.

Choose an encrypted library IPA and run decryption. The server selects the matching model by its exact selector, then lets upstream validate the complete license/model association. You can also choose a particular model explicitly. A model without selector information, a mismatched model, or an unsupported format fails with a useful error. The server does not try every model until one appears to work.

A request accepts up to 32 models of 64 KiB each. IPA inputs are limited to 2 GiB, individual ZIP members to 512 MiB, and total expanded data to 4 GiB; malformed or oversized inputs are rejected before upstream processing.

Models are opened only for the requested operation, written to private temporary files if upstream requires a path, and removed afterward. The server does not retain a model library or export the model's contents in logs. Decrypted output is imported as a new artifact linked to its original.

The result reports what upstream actually verified, including encrypted images, decrypted pages, and whether pre-encryption hashes established `plaintextVerified`. Older IPAs may not contain those hashes; successful processing then retains `plaintextVerified: false` rather than claiming hash verification. Account/license metadata stripping is an explicit operation option and is reported in the result.

Native enrollment capture and macOS application conversion are separate upstream workflows; this web interface accepts an existing matching model and an IPA. It does not invent a model from an arbitrary IPA or make every download automatically decryptable.

## Encrypted browser vault and backups

The vault uses two independent encryption layers:

1. The server seals account sessions and model blobs with AES-256-GCM. Its AES key is derived with SHA-256 from `extra_conf.enc_key`. If missing, SignTools generates a random 128-character value and persists it in the private configuration file with owner-only permissions. API responses never include this server key.
2. The browser wraps sealed values with AES-256-GCM using its own random 256-bit key stored in `localStorage`. Saved account details, model entries, and their sealed contents are encrypted records. Passwords and plaintext model bytes are not written to local storage.

The browser key is stored on the same device as its encrypted records, so this layer protects stored representations and backup handling; it is not a separate password lock against code running in that browser origin. Use HTTPS or localhost for the required Web Crypto APIs.

In the vault's backup controls, choose **Export** and a passphrase of at least ten characters. The downloaded `.json.zip` contains an encrypted JSON envelope, not plaintext accounts or an exposed browser key. PBKDF2-SHA256 with 600,000 iterations, a random salt, and AES-256-GCM protect the backup payload. The protected payload includes the browser key, encrypted accounts/models, and all `signtools.*` preferences, including repository lists and selected builders/profiles.

To restore, import that backup and enter its passphrase. The interface authenticates the payload and saved records before changing current browser data. A wrong passphrase, damaged file, or invalid record keeps the current browser state. A successful restore replaces the current SignTools browser settings with the saved settings.

A backup moves browser state, not server data. IPAs, signing profiles/certificates, configured builders, and the server's private `enc_key` remain on the server. Restore to another browser connected to the same server. When migrating the server itself, securely preserve the original configuration, `enc_key`, and data directory as well; changing that key invalidates previously sealed sessions/models even if their browser backup is intact. Apple can still expire or revoke an otherwise restored session, requiring sign-in again.

## Platform support

| Host                          | Signing service and IPA library | App Store engine                               | Model-based IPA decryption             |
| ----------------------------- | ------------------------------- | ---------------------------------------------- | -------------------------------------- |
| Linux amd64/arm64             | Available                       | Available, requires libc and runtime downloads | Available                              |
| macOS amd64/arm64             | Available                       | Available, requires runtime downloads          | Available                              |
| Windows amd64/arm64           | Available                       | Available, requires runtime downloads          | Unsupported by upstream                |
| Legacy 32-bit release targets | Available                       | Unsupported by the pinned upstream runtime     | Follows upstream's Linux/macOS support |

The existing ten binary targets and five Linux container targets remain available. A legacy build reports that the App Store engine is unavailable on its architecture while retaining the existing SignTools features. The 64-bit Linux images use Debian slim to provide the shared-library loader required by upstream's runtime; legacy 32-bit images use Alpine. A static Go build flag alone does not eliminate the 64-bit runtime's shared-library requirements.

## Build and update the integrations

Clone with submodules, or initialize them before an existing source build:

```sh
git clone --recurse-submodules https://github.com/xuniv/SignTools.git
cd SignTools
git submodule update --init --recursive
bun install --frozen-lockfile --ignore-scripts
bun run build
go build -trimpath -buildvcs=false -o SignTools .
```

After switching branches or release tags, run `git submodule update --init --recursive` again. Downloading a GitHub source ZIP alone does not populate submodules. Binary releases and container images include the compiled integrations.

Maintainers can intentionally update a submodule to the latest upstream `main`, inspect the changed revision, update integration tests and displayed revisions, then commit the new gitlink. Release CI, container builds, and release repairs check out submodules recursively at the recorded revisions. No account credentials or live Apple purchases are needed by the normal automated test suite.

Upstream notices are embedded in the binary and available at `/licenses/upstream.txt`; container copies also live under `/usr/share/licenses/SignTools`. Both integrations retain their upstream notices in their submodules. SignTools itself remains AGPL-3.0.
