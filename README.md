# YT Sync Process

- make sure you don't have a `.lbryum/wallets/default_wallet` 
  - delete existing wallet if there's nothing you need there, or better yet, move it somewhere else in case you need it later
- make sure daemon is stopped and can be controlled with `systemctl`
- run `lbry ytsync YOUTUBE_KEY YOUTUBE_CHANNEL_ID LBRY_CHANNEL_NAME --max-tries=5`
  - `max-tries` will retry errors that you will probably get (e.g. failed publishes)
- after sync is complete, daemon will be stopped and wallet will be moved to `~/wallets/`
- now mark content as synced in doc

Running the sync command for a channel that was already started will resume the sync. This can also be used to update a channel with new
content that was put on Youtube since the last sync.

---

Add this to cron to delete synced videos that have been published:

`*/10 * * * * (/bin/ls /tmp/ | /bin/grep -q ytsync && /usr/bin/find /tmp/ytsync* -mmin +20 -delete) || true
