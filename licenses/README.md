# Supplemental upstream licenses

`victory-vendor.txt` is the unmodified Victory 37.3.6 root license from
<https://raw.githubusercontent.com/FormidableLabs/victory/v37.3.6/LICENSE.txt>.
The published `victory-vendor` npm package includes its vendored D3 licenses but
omits this root notice. Both are included in `THIRD_PARTY_NOTICES.md`.

The notice generator reads other license files from the installed Go and npm
dependencies. Regenerate the notices whenever dependency versions change.
