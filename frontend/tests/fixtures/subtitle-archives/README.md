These tiny archives are read-only extraction fixtures; no user data is included.

- `normal.7z`: solid LZMA2 archive created with 7-Zip 25.01. Contains one UTF-8 SRT subtitle and `note.txt`.
- `encrypted-header.7z`, `encrypted-data.7z`: the same inputs encrypted with the test-only password `fixture`, with and without header encryption. Both must be rejected without prompting for a password.
- `normal-v4.rar`, `normal-v5.rar`: adapted from the MIT-licensed [libarchive-wasm test archives](https://github.com/ofk/libarchive-wasm/tree/main/archives). `example/README.md` was renamed to `example/readm.ass` in archive headers, and the header CRCs were recomputed. Compressed contents remain unchanged, allowing real RAR4/RAR5 decompression tests without requiring a proprietary RAR writer.
