# Current YT Sync Process

- make sure you have a clean `.lbryum` dir (delete existing dir if there's nothing you need there)
- make sure daemon is stopped and can be controlled with `systemctl`
- run `lbry ytsync YOUTUBE_KEY YOUTUBE_CHANNEL_ID LBRY_CHANNEL_NAME --max-tries=5`
  - `max-tries` will retry errors that you will undoubtedly get
- after sync is complete, daemon will be stopped and wallet will be moved to `~/wallets/`
- now mark content as synced in doc

Running the sync command for a channel that was already started will resume the sync. This can also be used to update a channel with new
content that was put on Youtube since the last sync.

---

Add this to cron to delete synced videos that have been published:

`*/10 * * * * /usr/bin/find /tmp/ ! -readable -prune -o -name '*ytsync*' -mmin +20 -print0  | xargs -0 --no-run-if-empty rm -r`
