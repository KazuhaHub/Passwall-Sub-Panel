# Fixed domain-list-community release fixture

`dlc-20261004053124.yml.gz` is a deterministic gzip copy (mtime zero) of
[release 20261004053124's dlc.dat_plain.yml](https://github.com/v2fly/domain-list-community/releases/download/20261004053124/dlc.dat_plain.yml).
Its decompressed bytes are unchanged (3,614,228 bytes). The adjacent checksum
file is the release's original `.sha256sum`:

`c0f7da9a7f95c86b354002650e8268b9d6bb0b638229274d3afa5651a7cf74b8`.

The regression runs offline and verifies the checksum before parsing. It covers
the complete export, including ASCII punycode compatibility and the public
suffix `domain:hsbc` in category-finance. Separate small fixtures cover multiple
attributes, negative attributes and both supported attribute separators.

The upstream data is distributed under the MIT license; its copyright and
license text are preserved in `LICENSE.v2fly`.
