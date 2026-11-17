# Ephemeral ipatool keyring adapter

This module preserves the `Item`, `Metadata`, error and `ArrayKeyring` contracts
from [ByteNess keyring v1.9.0](https://github.com/byteness/keyring/tree/v1.9.0).
`array.go` is copied from that MIT-licensed release; the original license is
included in this directory.

SignTools imports ipatool's real Go keychain adapter but replaces its keyring
dependency with this memory-only subset. Desktop keychain, file, secret-service
and 1Password backends are deliberately absent. Their unused dependencies would
otherwise introduce native keychain access and prevent CGO-free server builds.
No `Open` API or persistent storage backend is provided.

An array keyring holds plaintext bytes in process memory, so it is not itself a
secure persistent credential store. Each SignTools invocation owns a fresh
instance, restores it from a client-bound server AES-GCM seal, and returns its
updated state inside a new seal. Only the browser retains that encrypted state;
the adapter never writes credentials, provisioning state or cookies to disk.
