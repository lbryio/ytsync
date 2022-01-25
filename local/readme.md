# Running ytsync locally

## Requirements

- LBRY SDK (what do we actually need this for?)
- youtube-dl
- enough space to cache stuff
- YouTube data API key


## Process

### Ensuring requirements are met

- claim channel if there isn't one yet
  - or easier, just error if no channel 
- enough lbc in wallet?

### Getting a YouTube API key

To access the YouTube data API, you will first need some kind of google account.

The API has two methods of authentication, OAuth2 and API keys. This application uses API keys.
These API keys are basically like passwords, and so once obtained, they should not be shared.

The instructions for obtaining an API key are copied below from [here](https://developers.google.com/youtube/registering_an_application):


1. Open the [Credentials page](https://console.developers.google.com/apis/credentials) in the API Console.
2. Create an API key in the Console by clicking **Create credentials > API key**. You can restrict the key before using it in production by clicking **Restrict key** and selecting one of the **Restrictions**.

To keep your API keys secure, follow the [best practices for securely using API keys](https://cloud.google.com/docs/authentication/api-keys).

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
