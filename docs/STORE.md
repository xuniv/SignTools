# IPA library, App Store accounts, and model-based decryption

The IPA library, App Store account tools, and model-based decryption introduced in `3.1.0-2` gain personalized-license repair, complete automatic model selection, and richer app/version browsing in `3.1.0-3`. The source integrations use real, pinned submodules:

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

Select an account before browsing. Each account keeps its own Apple storefront, which is used for purchases and downloads. The separate search-country selector can browse another regional catalog; it does not change the account's billing country or grant access to that country's purchases. Country labels come from Apple's storefront catalog when a mapping is available; model selectors and uploaded filenames do not establish an account country.

Available operations include:

- Search by app name or bundle identifier.
- List apps already purchased or acquired by the selected account.
- Explicitly acquire a free app license. Paid apps must already have been purchased through Apple; the upstream purchase operation does not buy paid apps.
- List external version identifiers and retrieve human-readable version metadata and release dates where Apple supplies them.
- Download the latest version or a particular external version identifier.
- Upgrade or downgrade by choosing another version of the same app, keeping each downloaded artifact in the library.

Purchased-app rows automatically load catalog names, bundle identifiers, current versions, artwork, and known pricing where available, while retaining their purchase dates. Missing catalog metadata keeps the owned entry visible with its bundle identifier or `App #<id>` as a fallback. An unknown price is not labelled free. Delisted apps can still reach Apple's version/download endpoints by their numeric app identifier when Apple continues to serve the account's copy.

The versions dialog loads external version identifiers, then automatically fills human-readable versions, names, and release dates in bounded batches. Rows remain searchable and selectable as details arrive. A failed metadata lookup stays attached to its row, and its external build identifier remains available for a specific-version download. Closing the dialog cancels pending requests.

Downloads and decryption run as background tasks with progress, expandable logs, cancellation, and a resulting library artifact. Apple controls which versions are still available and whether an account can download them. Authentication, region, ownership, expired sessions, unavailable versions, and upstream errors remain visible instead of being replaced by an apparent successful result.

On supported hosts, the first App Store operation may download the pinned upstream Apple framework assets and a checksum-verified Unicorn runtime. This is separate from account storage. The runtime needs outbound HTTPS and a writable user cache. Container images set `XDG_CACHE_HOME=/data/cache`, so mounting `/data` also preserves these public caches. Configure `XDG_CACHE_HOME` explicitly if using a different volume or a read-only container filesystem.

## Personalized licenses and existing-download repair

Apple can return personalized SINF license blobs separately from the IPA download. SignTools applies the real upstream license-replication operation before importing that download into the library, checks the declared license paths, and confirms that the written license bytes match the response. Missing, empty, or mismatched required licenses fail the download instead of producing an apparently ready artifact. These packaging checks are separate from the full model/SINF authentication performed during decryption.

For an older library artifact downloaded through ipatool that lacks required personalized licenses, keep its original purchasing account signed in to this browser and retry decryption. SignTools can automatically recover the exact acquired version and retry decryption once. Recovery requires the original account email, app identifier, and external version identifier from `iTunesMetadata.plist`; it does not silently switch to the latest release or acquire another license.

Before accepting the recovered download, the server compares the account, app and external version identifiers, bundle identifier, displayed/build versions, and the paths and SHA-256 hashes of its Mach-O executables and `.supf`/`.supp` FairPlay payloads with the original. A mismatch refuses the repair and removes the rejected replacement. A verified repair creates a separate child artifact linked to the original; the original IPA remains unchanged. If the account or exact-version identity is unavailable, download that version manually with its purchasing account. Automatic recovery does not apply to arbitrary uploaded IPAs.

A missing declared SINF source may also be restored locally from an already-present replica only when its manifest explicitly declares that relationship, all usable declared replicas agree on the same bytes, and a supplied model authenticates the SINF through upstream validation. Unrelated neighboring files or matching selector text alone are insufficient. The decryption result records these license copies. A model cannot recreate absent license bytes when no valid source or account-based recovery is available.

## Models and decryption

Add models exported for your own matching enrollments/accounts. The server validates upstream v1, v2, and v3 model formats. The encrypted browser vault retains each uploaded file's original filename separately from its parsed type and selector. Older saved records without an original filename receive a neutral model identifier; re-upload the original file to retain its name. The interface does not invent a country from a model's selector, type, or filename.

**Automatic** submits every saved model to the server, including entries whose older cached metadata did not mark them supported. The server opens and validates the complete supplied pool, skips malformed, unreadable, duplicate, or unsupported entries with an explanation, and authenticates valid candidates against the IPA's actual SINF licenses using the upstream resolver. Selector-bound v2/v3 candidates are considered only for their matching selectors. A v1 candidate without a selector must first pass complete SINF authentication; only then is a temporary selector-bound envelope created without changing its key/codec bytes, authenticated again, and checked to produce the identical key. The saved model remains unchanged.

An IPA can require different models for its app and extensions. Every authenticated candidate is passed through the upstream model registry, which resolves each required selector and rejects ambiguity. The result distinguishes attempted, skipped, authenticated, and selected models and retains the models that were actually used. A cached browser flag, filename, or selector match by itself cannot establish decryption success.

Selecting a specific model uses only that model. An invalid or mismatched manual selection fails without falling back to the remaining pool. If no supplied model authenticates the required licenses, the task reports the failure; account-based license recovery addresses missing license data and does not turn an unrelated model into a match.

A request accepts up to 32 models of 64 KiB each. Automatic selection submits the whole saved pool, so reduce a larger pool or select a specific model before retrying. IPA inputs are limited to 2 GiB, individual ZIP members to 512 MiB, and total expanded data to 4 GiB; malformed or oversized inputs are rejected before upstream processing.

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
