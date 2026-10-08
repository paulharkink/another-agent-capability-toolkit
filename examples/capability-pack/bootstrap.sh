#!/bin/sh
# Run explicitly after cloning your pack. Git's recorded gitlink commits pin
# submodules. The checked-in offline example itself has no external submodules.
set -eu
AACT_PACK_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
git -C "$AACT_PACK_DIR" submodule update --init --recursive
