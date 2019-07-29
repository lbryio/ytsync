#!/bin/bash

## TODO: Make a bit more aware of the run mode of this appliance in case there is ever a test mode enabled in the start.sh
mountpoint=/home/lbrynet

if ! grep -qs ".* $mountpoint " /proc/mounts; then
    echo "$mountpoint not mounted, refusing to run."
    ## TODO: We should have documentation that this error references directly with a URL as to why it won't run without a volume.
    exit 1
else
    bash -c "$*"
fi
