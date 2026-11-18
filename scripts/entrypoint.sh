#!/bin/sh
# Starts the newest installed build.
#
# A self-update stages the new binary under <data>/bin and replaces the running
# process. This launcher is what makes the staged copy survive a container
# restart: it is chosen here, so /data stays the single volume to back up.
set -eu

IMAGE_BINARY=/usr/local/bin/obblog

# Resolve the data directory from -data, matching the server's own default.
# Hardcoding /data would miss custom mounts and silently start the old build.
data_dir=/data
previous=
for arg in "$@"; do
    if [ "$previous" = "-data" ]; then
        data_dir=$arg
        break
    fi
    case $arg in
        -data=*) data_dir=${arg#-data=}; break ;;
    esac
    previous=$arg
done

staged=$data_dir/bin/obblog
binary=$IMAGE_BINARY
if [ -x "$staged" ]; then
    # The updater renames the file into place only after verification, so a
    # binary that cannot even report its version means something went wrong
    # outside that path and the image build is the safer choice.
    if "$staged" -version >/dev/null 2>&1; then
        binary=$staged
    else
        echo "updater: $staged failed to run, falling back to the image build" >&2
    fi
fi

exec "$binary" "$@"