#!/bin/sh
# Starts the newest installed build.
#
# A self-update writes the new binary under /data/bin and exits; this script
# then picks it up on the next start. Keeping the choice here means the
# container restarts onto the updated build without being recreated, while
# /data stays the single volume that has to be backed up.
set -eu

IMAGE_BINARY=/usr/local/bin/obblog
DATA_BINARY=/data/bin/obblog

binary=$IMAGE_BINARY
if [ -x "$DATA_BINARY" ]; then
    # Refuse a binary the updater left half-written: it is renamed into place
    # atomically, so a partial file here means something went wrong.
    if "$DATA_BINARY" -version >/dev/null 2>&1; then
        binary=$DATA_BINARY
    else
        echo "updater: /data/bin/obblog failed to run, falling back to the image build" >&2
    fi
fi

exec "$binary" "$@"