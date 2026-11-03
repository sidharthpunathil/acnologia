# cJSON

The SDK parses and builds JSON with cJSON, copied here so the build downloads nothing.

- Version: 1.7.18 (`CJSON_VERSION_MAJOR.MINOR.PATCH` in `cJSON.h`)
- Source: https://github.com/DaveGamble/cJSON, tag `v1.7.18`, files `cJSON.c`, `cJSON.h` and `LICENSE`, unchanged
- License: MIT, see [LICENSE](LICENSE)
- SHA-256:
  - `cJSON.c` 75c51de8fa40ac9d7a99319c6330719bd692eb81c0a869265f3d4c682533f9b9
  - `cJSON.h` 0578cc29132912edbc88f83207a8fc76e5db3db0605497e909a9384ef3cc474b
  - `LICENSE` a36dda207c36db5818729c54e7ad4e8b0c6fba847491ba64f372c1a2037b6d5c

cJSON is compiled into the library and is not part of its public header. A program that links its own cJSON as well gets two sets of `cJSON_*` symbols: drop one copy, or build the SDK against yours by replacing these files with it.

To update, replace the three files with those of a newer tag, update the version and hashes above, and run the tests.
