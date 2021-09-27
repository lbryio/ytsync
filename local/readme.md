# Running ytsync locally

## Requirements

- LBRY SDK (what do we actually need this for?)
- youtube-dl
- enough space to cache stuff


## Process

### Ensuring requirements are met

- claim channel if there isn't one yet
  - or easier, just error if no channel 
- enough lbc in wallet?

### Options to figure out what's already synced

- simplest: assume nothing is synced yet
- assume everything before some video is synced
- get/put sync info from Odysee by proving you have private key for channel
- tag videos as having been synced from youtube so we can ensure accuracy
- hardest: scan channel and try to match up which videos are not synced yet

### Central DB

- prove you have a channel's private key to get info about that channel
- proper queue instead of sleeping for N minutes between syncs



### Syncing a single video

- downloading it
- thumbnails
- metadata
- having enough LBC for publish(es)
- automated error handling
- getting a human involved for errors that can't be handled automatically
- reflecting

### Continuous Sync

- running in background
- storing local state
- interactions with our central ytsync db
- dealing with yt throttling


### Debugging

- dry-running the whole thing