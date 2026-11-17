These are entirely synthetic FairPlay fixtures, containing no real application,
account, enrollment state, hardware identity, or private capture. User 42 / key 7
and user 84 / key 7 are invented selectors. Every model key and codec is public
synthetic data from upstream's tests.

Generated against unfairplay revision
`d7503082a59e3dcfdd3ea7a70434dcecaaaf07df`, using its public test helpers
`syntheticCurrentSINFFixture`, `selectorBoundModelPack`, and `multiSINFImage`
in a temporary copy of the upstream checkout. The vendored upstream is unchanged.
`verified.ipa` contains one ARM64 image with a 4 KiB page of `nop` instructions,
encrypted using the synthetic inner key `{1,2,3,0,...}`. It includes independent
ordinary and pre-encryption SHA-256 CodeDirectory tables. `unverified.ipa` differs
only by setting the optional pre-encryption hash-table offset to zero.
`multiple-selectors.ipa` also has a widget with selector 42 / key 8, inner key
`{9,8,7,0,...}`, and a page of ARM64 `ret` instructions.

The models use the upstream v2 public synthetic codec. `wrong-key-model.bin`
changes the codec AES key while retaining its selector and valid structure.
`unbound-v1-model.bin` uses the same codec without a selector. SignTools accepts
it only after complete upstream SINF authentication and lossless selector binding,
then revalidates identical key recovery and performs normal upstream decryption.
None of these models can
decrypt a real App Store application.
