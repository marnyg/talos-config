#!/usr/bin/env bash
# Post-process uniffi-bindgen-go output for iroh-ffi. Two known generator
# defects (uniffi-bindgen-go v0.7.1+v0.31.0), both fixed textually here
# rather than by forking the generator. Each edit is exact-match; if the
# generator changes shape the sed no-ops and the drift gate / go vet will
# tell. Idempotent.
#
#   1. `HashMap<Vec<u8>, T>` is rendered as `map[[]byte]T` — not valid Go
#      (slices are not comparable). Rendered as `map[string]T` with the
#      key converted at the FFI boundary. Only EndpointOptions.protocols
#      (ALPN → handler) has this shape.
#   2. `IrohError.Error()` returns the literal "IrohError" by value
#      (go vet: copylocks, and useless messages). Delegate to Message()
#      via a pointer receiver.
#   3. `package_name` in uniffi.toml only names the output directory; the
#      package clause is always the crate namespace (`iroh_ffi`). Rename
#      to `iroh` so the import path and identifier agree.
set -euo pipefail
f="${1:?usage: fixup.sh <iroh_ffi.go>}"

sed -i \
  -e 's/^package iroh_ffi$/package iroh/' \
  -e 's/map\[\[\]byte\]ProtocolCreator/map[string]ProtocolCreator/g' \
  -e 's/^\t\tresult\[key\] = value$/\t\tresult[string(key)] = value/' \
  -e 's/^\t\tFfiConverterBytesINSTANCE.Write(writer, key)$/\t\tFfiConverterBytesINSTANCE.Write(writer, []byte(key))/' \
  -e 's/^\t\tFfiDestroyerBytes{}.Destroy(key)$/\t\tFfiDestroyerBytes{}.Destroy([]byte(key))/' \
  "$f"

# (2) two-line replacement: receiver + body.
perl -0pi -e 's/func \(_self IrohError\) Error\(\) string \{\n\treturn "IrohError"\n\}/func (_self *IrohError) Error() string {\n\treturn _self.Message()\n}/' "$f"

if grep -q 'map\[\[\]byte\]' "$f"; then
  echo "fixup.sh: unhandled map[[]byte] key remains in $f" >&2
  exit 1
fi
