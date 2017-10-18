# Current YT Sync Process

- start daemon with clean `.lbryum` dir
- `wallet_unused_address` to get an address
- send credits to address. make sure the send is split into 50 or so txns, and that there are enough credits to publish all the videos
- wait for credits to arrive and be confirmed
- run `lbry ytsync YOUTUBE_KEY YOUTUBE_CHANNEL_ID LBRY_CHANNEL_NAME --max-tries=5`
  - `max-tries` will retry errors that you will undoubtedly get
- after sync is done, stop daemon and move `.lbryum` dir somewhere safe
- mark content as synced in doc
